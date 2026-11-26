//go:build windows

package agent

// localWinPE builds the job's WinPE from THIS machine's own recovery environment
// (Winre.wim) instead of the server's generic one. WinRE is serviced together with the
// machine's boot manager and Secure Boot state, so the boot manager accepts its
// winload.efi — a generic WinPE from another build can be refused under Secure Boot
// (seen on a fresh Windows 11 25H2 install: the one-time boot fell straight back).
// Steps: copy Winre.wim → DISM mount → add swim-pe.exe, winpeshl.ini and this machine's
// storage/network drivers → commit → staging\swim\winpe\boot.wim.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var reLocation = regexp.MustCompile(`(?im)^\s*Windows RE location:\s*(\S.*?)\s*$`)

// winreSource returns the path of this machine's Winre.wim.
func winreSource(ctx context.Context) (string, error) {
	out, _ := runCmd(ctx, 2*time.Minute, "reagentc.exe", "/info")
	if m := reLocation.FindStringSubmatch(out); m != nil {
		p := filepath.Join(strings.TrimSpace(m[1]), "Winre.wim")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// WinRE disabled: reagentc keeps the image in the OS volume
	p := filepath.Join(os.Getenv("SystemRoot"), "System32", "Recovery", "Winre.wim")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", errors.New("this machine has no Windows Recovery Environment image (reagentc /info shows none)")
}

func streamCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(t, 0o755)
		}
		return streamCopy(p, t)
	})
}

// localWinPE writes staging\swim\winpe\boot.wim and returns the WinRE version used.
func (j *reimageJob) localWinPE(ctx context.Context, peExe string) (string, error) {
	src, err := winreSource(ctx)
	if err != nil {
		return "", err
	}
	// work on C: (plenty of room, and the mount expands to a few GB), then move the result
	work := filepath.Join(os.Getenv("SystemRoot"), "Temp", "swim-pe-build")
	mnt := filepath.Join(work, "mnt")
	wim := filepath.Join(work, "boot.wim")
	_, _ = runCmd(ctx, 5*time.Minute, "dism.exe", "/Unmount-Image", "/MountDir:"+mnt, "/Discard") // leftovers of an earlier try
	_, _ = runCmd(ctx, 5*time.Minute, "dism.exe", "/Cleanup-Mountpoints")
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(mnt, 0o700); err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	if err := streamCopy(src, wim); err != nil {
		return "", fmt.Errorf("copy %s: %w", src, err)
	}
	info, _ := runCmd(ctx, 2*time.Minute, "dism.exe", "/English", "/Get-ImageInfo", "/ImageFile:"+wim, "/Index:1")
	ver := ""
	if m := regexp.MustCompile(`(?m)^Version\s*:\s*(\S+)`).FindStringSubmatch(info); m != nil {
		ver = m[1]
		if sp := regexp.MustCompile(`(?m)^ServicePack Build\s*:\s*(\d+)`).FindStringSubmatch(info); sp != nil {
			ver += "." + sp[1]
		}
	}
	if out, err := runCmd(ctx, 15*time.Minute, "dism.exe", "/English", "/Mount-Image", "/ImageFile:"+wim, "/Index:1", "/MountDir:"+mnt); err != nil {
		return "", fmt.Errorf("mount WinRE: %v %s", err, lastLine(out))
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = runCmd(context.Background(), 10*time.Minute, "dism.exe", "/Unmount-Image", "/MountDir:"+mnt, "/Discard")
		}
	}()
	sys32 := filepath.Join(mnt, "Windows", "System32")
	if err := streamCopy(peExe, filepath.Join(sys32, "swim-pe.exe")); err != nil {
		return "", fmt.Errorf("add swim-pe: %w", err)
	}
	// replaces WinRE's own winpeshl.ini (which would start the recovery UI)
	if err := os.WriteFile(filepath.Join(sys32, "winpeshl.ini"), []byte("[LaunchApps]\r\n%SYSTEMROOT%\\System32\\swim-pe.exe\r\n"), 0o644); err != nil {
		return "", err
	}
	if drv := filepath.Join(j.root, "swim", "drivers"); dirSize(drv) > 0 {
		if err := copyTree(drv, filepath.Join(mnt, "swim", "drivers")); err != nil {
			return "", fmt.Errorf("add drivers: %w", err)
		}
	}
	// mark it, so a later retry keeps this machine-matched WinPE instead of the server's
	_ = os.WriteFile(filepath.Join(mnt, "swim", "local-winre.txt"), []byte("built from this machine's WinRE "+ver+"\r\n"), 0o644)
	if out, err := runCmd(ctx, 20*time.Minute, "dism.exe", "/English", "/Unmount-Image", "/MountDir:"+mnt, "/Commit"); err != nil {
		return "", fmt.Errorf("commit WinPE: %v %s", err, lastLine(out))
	}
	committed = true
	dst := filepath.Join(j.root, "swim", "winpe", "boot.wim")
	_ = os.MkdirAll(filepath.Dir(dst), 0o700)
	_ = os.Remove(dst)
	if err := streamCopy(wim, dst); err != nil {
		return "", fmt.Errorf("store WinPE: %w", err)
	}
	_ = os.WriteFile(filepath.Join(j.root, "swim", "winpe", "local-winre.txt"), []byte(ver+"\r\n"), 0o600)
	return ver, nil
}

func lastLine(s string) string {
	l := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r", "")), "\n")
	return l[len(l)-1]
}
