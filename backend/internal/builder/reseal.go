package builder

// Reseal: make sure the captured Windows is generalized (sysprep /generalize /oobe).
//
// The Golden Master has no QEMU guest agent, and running sysprep on it would change the
// master itself. Instead the capture overlay is booted once as a throwaway VM with no
// network: Windows runs a one-shot command before logon (HKLM\SYSTEM\Setup CmdLine,
// SetupType=2) that starts sysprep, which shuts the VM down. Only the overlay changes;
// the Golden Master disk is never written and needs no snapshot/revert.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const resealScript = `@echo off
rem SWIM builder: one-shot reseal (started by HKLM\SYSTEM\Setup CmdLine before logon)
reg add HKLM\SYSTEM\Setup /v SetupType /t REG_DWORD /d 0 /f >nul
reg add HKLM\SYSTEM\Setup /v CmdLine /t REG_SZ /d "" /f >nul
echo %DATE% %TIME% sysprep starting > C:\Windows\Setup\Scripts\swim-reseal.log
C:\Windows\System32\Sysprep\sysprep.exe /generalize /oobe /shutdown /quiet
echo %DATE% %TIME% sysprep returned %ERRORLEVEL% >> C:\Windows\Setup\Scripts\swim-reseal.log
rem sysprep shuts the machine down itself; reaching this line means it failed
shutdown /s /t 5 /f
`

const resealReg = `Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SYSTEM\Setup]
"SetupType"=dword:00000002
"CmdLine"="cmd.exe /c C:\\Windows\\Setup\\Scripts\\swim-reseal.cmd"

; Windows 11 turns on BitLocker device encryption by itself on any boot with a TPM —
; the reseal VM would encrypt the overlay and nothing could be captured. Kept in the
; image on purpose: SWIM's staging partition and WinPE need unencrypted disks too.
[HKEY_LOCAL_MACHINE\SYSTEM\ControlSet001\Control\BitLocker]
"PreventDeviceEncryption"=dword:00000001
`

// generalized reports whether the mounted Windows volume is resealed for OOBE.
func generalized(root string) (bool, string) {
	st := readText(filepath.Join(root, "Windows", "Setup", "State", "State.ini"))
	if st == "" {
		return false, "State.ini missing"
	}
	state := between(st, "ImageState=", "\n")
	return state == "IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE", state
}

// withMount mounts an NTFS partition with ntfs-3g and runs fn on it.
func withMount(part string, rw bool, fn func(root string) error) error {
	mnt, err := os.MkdirTemp("", "swim-ntfs-")
	if err != nil {
		return err
	}
	defer os.Remove(mnt)
	opts := "ro,streams_interface=windows"
	if rw {
		opts = "rw,streams_interface=windows"
	}
	if b, err := exec.Command("mount", "-t", "ntfs-3g", "-o", opts, part, mnt).CombinedOutput(); err != nil {
		return fmt.Errorf("mount %s: %v: %s", part, err, b)
	}
	ferr := fn(mnt)
	if b, err := exec.Command("umount", mnt).CombinedOutput(); err != nil && ferr == nil {
		ferr = fmt.Errorf("umount %s: %v: %s", mnt, err, b)
	}
	return ferr
}

// prepareReseal writes the one-shot script and the Setup CmdLine into the volume.
// Returns false (nothing to do) when it is already generalized.
func prepareReseal(part string, logf func(string, ...any)) (bool, error) {
	need := false
	err := withMount(part, true, func(root string) error {
		ok, state := generalized(root)
		if ok {
			logf("Windows is already generalized (%s)", state)
			return nil
		}
		need = true
		logf("Windows is NOT generalized (ImageState %s) — resealing it in a throwaway VM", state)
		dir := filepath.Join(root, "Windows", "Setup", "Scripts")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		script := strings.ReplaceAll(resealScript, "\n", "\r\n")
		if err := os.WriteFile(filepath.Join(dir, "swim-reseal.cmd"), []byte(script), 0o644); err != nil {
			return err
		}
		reg := filepath.Join(os.TempDir(), "swim-reseal.reg")
		if err := os.WriteFile(reg, []byte(resealReg), 0o644); err != nil {
			return err
		}
		defer os.Remove(reg)
		hive := filepath.Join(root, "Windows", "System32", "config", "SYSTEM")
		if b, err := exec.Command("hivexregedit", "--merge", "--prefix", `HKEY_LOCAL_MACHINE\SYSTEM`, hive, reg).CombinedOutput(); err != nil {
			return fmt.Errorf("hivexregedit: %v: %s", err, b)
		}
		return nil
	})
	return need, err
}

// finishReseal checks the result after the VM shut down, copies the logs into the build
// log and removes the helper files from the volume.
func finishReseal(part string, logf func(string, ...any)) error {
	return withMount(part, true, func(root string) error {
		dir := filepath.Join(root, "Windows", "Setup", "Scripts")
		if l := readText(filepath.Join(dir, "swim-reseal.log")); l != "" {
			logf("reseal log: %s", strings.ReplaceAll(strings.TrimSpace(l), "\n", " | "))
		}
		_ = os.Remove(filepath.Join(dir, "swim-reseal.cmd"))
		_ = os.Remove(filepath.Join(dir, "swim-reseal.log"))
		if ents, err := os.ReadDir(dir); err == nil && len(ents) == 0 {
			_ = os.Remove(dir)
		}
		ok, state := generalized(root)
		if ok {
			logf("reseal ok: ImageState %s", state)
			return nil
		}
		// sysprep explains itself in setuperr.log
		errs := readText(filepath.Join(root, "Windows", "System32", "Sysprep", "Panther", "setuperr.log"))
		lines := strings.Split(strings.TrimSpace(errs), "\n")
		if len(lines) > 8 {
			lines = lines[len(lines)-8:]
		}
		return fmt.Errorf("sysprep did not generalize Windows (ImageState %s). setuperr.log: %s", state, strings.Join(lines, " | "))
	})
}

var (
	reUUID     = regexp.MustCompile(`(?s)\s*<uuid>.*?</uuid>`)
	reName     = regexp.MustCompile(`<name>[^<]*</name>`)
	reCdrom    = regexp.MustCompile(`(?s)\s*<disk[^>]*device='cdrom'.*?</disk>`)
	reIface    = regexp.MustCompile(`(?s)\s*<interface .*?</interface>`)
	reBootCD   = regexp.MustCompile(`\s*<boot dev='cdrom'/>`)
	reNvram    = regexp.MustCompile(`(<nvram[^>]*>)[^<]*(</nvram>)`)
	reDiskSrc  = regexp.MustCompile(`(?s)(<disk[^>]*device='disk'.*?<source file=')[^']*(')`)
	reTPMState = regexp.MustCompile(`(?s)\s*<source type='dir'[^>]*/>`)
	reGraphics = regexp.MustCompile(`<graphics [^>]*>`)
	reOnEvent  = regexp.MustCompile(`\s*<on_(poweroff|reboot|crash)>[^<]*</on_(poweroff|reboot|crash)>`)
	reAttrPort = regexp.MustCompile(` (port|autoport)='[^']*'`)
)

// resealDomainXML derives the throwaway VM from the Golden Master definition: same
// firmware/CPU/disk bus, no CD-ROM, no network, its own name, NVRAM and TPM.
func resealDomainXML(gmXML, name, overlay, gmDisk string) (string, error) {
	x := reName.ReplaceAllString(gmXML, "<name>"+name+"</name>")
	x = reUUID.ReplaceAllString(x, "")
	x = reCdrom.ReplaceAllString(x, "")
	x = reIface.ReplaceAllString(x, "")
	x = reBootCD.ReplaceAllString(x, "")
	x = reTPMState.ReplaceAllString(x, "")
	x = reGraphics.ReplaceAllStringFunc(x, func(g string) string {
		g = reAttrPort.ReplaceAllString(g, "")
		return strings.Replace(g, "<graphics ", "<graphics port='-1' autoport='yes' ", 1)
	})
	x = reNvram.ReplaceAllString(x, "${1}/var/lib/libvirt/qemu/nvram/"+name+"_VARS.fd${2}")
	// Windows reboots (not powers off) after the Setup CmdLine phase: turn any reboot into
	// a stop, after Windows has flushed the disk — the next boot would start OOBE and use
	// up the reseal.
	x = reOnEvent.ReplaceAllString(x, "")
	x = strings.Replace(x, "</domain>", "  <on_poweroff>destroy</on_poweroff>\n  <on_reboot>destroy</on_reboot>\n  <on_crash>destroy</on_crash>\n</domain>", 1)
	if !reDiskSrc.MatchString(x) {
		return "", fmt.Errorf("Golden Master definition has no file disk")
	}
	x = reDiskSrc.ReplaceAllString(x, "${1}"+overlay+"${2}")
	if strings.Contains(x, "'"+gmDisk+"'") || strings.Count(x, "<source file=") != 1 {
		return "", fmt.Errorf("unexpected disk layout in the Golden Master definition")
	}
	return x, nil
}

// runResealVM boots the overlay once and waits until sysprep powers it off.
func (w *Worker) runResealVM(ctx context.Context, gm, gmDisk, overlay, name string, logf func(string, ...any)) error {
	virsh := func(timeout time.Duration, args ...string) (string, error) {
		c, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		b, err := exec.CommandContext(c, "virsh", append([]string{"-c", w.LV.URI}, args...)...).CombinedOutput()
		if err != nil {
			return string(b), fmt.Errorf("virsh %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(b)))
		}
		return strings.TrimSpace(string(b)), nil
	}
	gmXML, err := virsh(30*time.Second, "dumpxml", "--inactive", gm)
	if err != nil {
		return err
	}
	x, err := resealDomainXML(gmXML, name, overlay, gmDisk)
	if err != nil {
		return err
	}
	f := filepath.Join(os.TempDir(), name+".xml")
	if err := os.WriteFile(f, []byte(x), 0o600); err != nil {
		return err
	}
	defer os.Remove(f)
	if _, err := virsh(time.Minute, "define", f); err != nil {
		return err
	}
	// never --remove-all-storage: the overlay is removed by capture() itself
	defer func() {
		_, _ = virsh(time.Minute, "destroy", name)
		if _, err := virsh(time.Minute, "undefine", name, "--nvram"); err != nil {
			logf("cleanup: %v", err)
		}
	}()
	if _, err := virsh(time.Minute, "start", name); err != nil {
		return err
	}
	logf("reseal VM %s started (no network); waiting for sysprep to shut it down (up to 45 min)", name)
	deadline := time.Now().Add(45 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Second):
		}
		w.progress(ctx, "sysprep", stagePct("sysprep", min(time.Since(deadline.Add(-45*time.Minute)).Seconds()/180, 0.95)),
			"sysprep is generalizing Windows in a throwaway VM")
		st, err := virsh(30*time.Second, "domstate", name)
		if err == nil && st == "shut off" {
			logf("reseal VM powered off after sysprep")
			return nil
		}
	}
	return fmt.Errorf("reseal VM did not shut down within 45 min (sysprep stuck? open its console: virsh vncdisplay %s)", name)
}

// withNBD attaches a qcow2 file to a free /dev/nbdN, finds the Windows partition and runs fn.
func withNBD(ctx context.Context, qcow string, logf func(string, ...any), fn func(part string) error) error {
	dev, err := freeNBD()
	if err != nil {
		return err
	}
	if b, err := exec.CommandContext(ctx, "qemu-nbd", "--format=qcow2", "--connect="+dev, qcow).CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-nbd: %v: %s", err, b)
	}
	defer exec.Command("qemu-nbd", "--disconnect", dev).Run() //nolint:errcheck
	part, _, err := windowsPartition(dev)
	if err != nil {
		return err
	}
	return fn(part)
}

// reseal generalizes the overlay if needed (see the file comment).
func (w *Worker) reseal(ctx context.Context, imageID, gm, gmDisk, overlay string, logf func(string, ...any)) error {
	var need bool
	if err := withNBD(ctx, overlay, logf, func(part string) (err error) {
		need, err = prepareReseal(part, logf)
		return err
	}); err != nil {
		return err
	}
	if !need {
		return nil
	}
	name := "swim-reseal-" + strings.TrimPrefix(imageID, "img_")
	if err := w.runResealVM(ctx, gm, gmDisk, overlay, name, logf); err != nil {
		return err
	}
	return withNBD(ctx, overlay, logf, func(part string) error { return finishReseal(part, logf) })
}
