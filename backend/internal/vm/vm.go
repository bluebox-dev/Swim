// Package vm drives libvirt VMs (virsh) and their offline snapshots (qemu-img).
//
// Snapshots are internal qcow2 snapshots taken while the VM is shut off, plus a
// copy of the UEFI NVRAM and the swtpm state, because libvirt refuses internal
// snapshots for pflash/UEFI guests.
package vm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

// ValidName guards every VM/snapshot name before it reaches a command line.
func ValidName(s string) bool { return nameRe.MatchString(s) }

var ErrNotOff = errors.New("VM must be shut off for this operation")

type Libvirt struct {
	URI     string
	SnapDir string // where NVRAM/TPM copies live, e.g. /srv/swim/vms/snapshots
}

func run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, s)
	}
	return s, nil
}

func (l Libvirt) virsh(ctx context.Context, args ...string) (string, error) {
	return run(ctx, 30*time.Second, "virsh", append([]string{"-c", l.URI}, args...)...)
}

func (l Libvirt) List(ctx context.Context) ([]string, error) {
	out, err := l.virsh(ctx, "list", "--all", "--name")
	return strings.Fields(out), err
}

func (l Libvirt) Exists(ctx context.Context, name string) bool {
	if !ValidName(name) {
		return false
	}
	_, err := l.virsh(ctx, "dominfo", name)
	return err == nil
}

func (l Libvirt) State(ctx context.Context, name string) (string, error) {
	return l.virsh(ctx, "domstate", name)
}

type Disk struct {
	Device string `json:"device"` // disk | cdrom
	Target string `json:"target"`
	Source string `json:"source"`
}

func (l Libvirt) Disks(ctx context.Context, name string) ([]Disk, error) {
	out, err := l.virsh(ctx, "domblklist", name, "--details")
	if err != nil {
		return nil, err
	}
	var ds []Disk
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] == "Type" || strings.HasPrefix(f[0], "-") {
			continue
		}
		d := Disk{Device: f[1], Target: f[2]}
		if len(f) >= 4 && f[3] != "-" {
			d.Source = strings.Join(f[3:], " ")
		}
		ds = append(ds, d)
	}
	return ds, nil
}

// MainDisk returns the first non-cdrom disk image path.
func (l Libvirt) MainDisk(ctx context.Context, name string) (string, error) {
	ds, err := l.Disks(ctx, name)
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Device == "disk" && d.Source != "" {
			return d.Source, nil
		}
	}
	return "", fmt.Errorf("%s: no disk", name)
}

// VNCPort is only known while the VM runs (autoport).
func (l Libvirt) VNCPort(ctx context.Context, name string) (int, error) {
	out, err := l.virsh(ctx, "vncdisplay", name)
	if err != nil {
		return 0, err
	}
	_, num, ok := strings.Cut(strings.TrimSpace(out), ":")
	n, perr := strconv.Atoi(num)
	if !ok || perr != nil {
		return 0, fmt.Errorf("unexpected vncdisplay %q", out)
	}
	return 5900 + n, nil
}

var nvramRe = regexp.MustCompile(`<nvram[^>]*>([^<]+)</nvram>`)

func (l Libvirt) nvramAndTPM(ctx context.Context, name string) (nvram, tpmDir string, err error) {
	xml, err := l.virsh(ctx, "dumpxml", name)
	if err != nil {
		return "", "", err
	}
	if m := nvramRe.FindStringSubmatch(xml); m != nil {
		nvram = m[1]
	}
	uuid, err := l.virsh(ctx, "domuuid", name)
	if err != nil {
		return "", "", err
	}
	return nvram, filepath.Join("/var/lib/libvirt/swtpm", strings.TrimSpace(uuid)), nil
}

// Action runs a power action: start | shutdown (ACPI) | poweroff | reset.
func (l Libvirt) Action(ctx context.Context, name, action string) error {
	verb := map[string]string{"start": "start", "shutdown": "shutdown", "poweroff": "destroy", "reset": "reset"}[action]
	if verb == "" {
		return fmt.Errorf("unknown action %q", action)
	}
	if verb == "start" {
		// a CD image that was removed from disk makes libvirt refuse to start the VM:
		// eject it instead of failing (the Builder page then shows the drive empty)
		if ds, err := l.Disks(ctx, name); err == nil {
			for _, d := range ds {
				if d.Device == "cdrom" && d.Source != "" {
					if _, serr := os.Stat(d.Source); errors.Is(serr, os.ErrNotExist) {
						if err := l.SetMedia(ctx, name, ""); err != nil {
							return fmt.Errorf("the ISO %s no longer exists and could not be ejected: %w", filepath.Base(d.Source), err)
						}
					}
				}
			}
		}
	}
	_, err := l.virsh(ctx, verb, name)
	if err != nil && strings.Contains(err.Error(), `Failed to get "write" lock`) {
		return fmt.Errorf("the disk of %s is open in another program (an image capture, a test clone or a qemu-nbd "+
			"connection) — wait for the capture to finish or disconnect it, then start again", name)
	}
	return err
}

// SetMedia inserts iso into the first cdrom, or ejects it when iso is "".
func (l Libvirt) SetMedia(ctx context.Context, name, iso string) error {
	ds, err := l.Disks(ctx, name)
	if err != nil {
		return err
	}
	target := ""
	for _, d := range ds {
		if d.Device == "cdrom" {
			target = d.Target
			break
		}
	}
	if target == "" {
		return errors.New("VM has no cdrom drive")
	}
	args := []string{"change-media", name, target, "--config"}
	if st, _ := l.State(ctx, name); st == "running" {
		args = append(args, "--live")
	}
	if iso == "" {
		args = append(args, "--eject", "--force")
	} else {
		args = append(args, iso, "--update", "--force")
	}
	_, err = l.virsh(ctx, args...)
	return err
}

type Snapshot struct {
	Name string    `json:"name"`
	Date time.Time `json:"date"`
}

func Snapshots(ctx context.Context, disk string) ([]Snapshot, error) {
	out, err := run(ctx, 30*time.Second, "qemu-img", "info", "--force-share", "--output=json", disk)
	if err != nil {
		return nil, err
	}
	var info struct {
		Snapshots []struct {
			Name    string `json:"name"`
			DateSec int64  `json:"date-sec"`
		} `json:"snapshots"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return nil, err
	}
	snaps := []Snapshot{}
	for _, s := range info.Snapshots {
		snaps = append(snaps, Snapshot{Name: s.Name, Date: time.Unix(s.DateSec, 0).UTC()})
	}
	return snaps, nil
}

func (l Libvirt) requireOff(ctx context.Context, name string) error {
	st, err := l.State(ctx, name)
	if err != nil {
		return err
	}
	if st != "shut off" {
		return ErrNotOff
	}
	return nil
}

func (l Libvirt) snapPath(vmName, snap string) string { return filepath.Join(l.SnapDir, vmName, snap) }

// CreateSnapshot: qcow2 internal snapshot + NVRAM + TPM state copy. VM must be off.
func (l Libvirt) CreateSnapshot(ctx context.Context, name, snap string) error {
	if !ValidName(snap) {
		return errors.New("invalid snapshot name")
	}
	if err := l.requireOff(ctx, name); err != nil {
		return err
	}
	disk, err := l.MainDisk(ctx, name)
	if err != nil {
		return err
	}
	existing, err := Snapshots(ctx, disk)
	if err != nil {
		return err
	}
	for _, s := range existing {
		if s.Name == snap {
			return fmt.Errorf("snapshot %q already exists", snap)
		}
	}
	nvram, tpm, err := l.nvramAndTPM(ctx, name)
	if err != nil {
		return err
	}
	dir := l.snapPath(name, snap)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// NVRAM only exists once the VM has booted at least once.
	if _, statErr := os.Stat(nvram); nvram != "" && statErr == nil {
		if _, err := run(ctx, time.Minute, "cp", "-a", nvram, filepath.Join(dir, "nvram.fd")); err != nil {
			return err
		}
	}
	if _, statErr := os.Stat(tpm); statErr == nil {
		if _, err := run(ctx, time.Minute, "cp", "-a", tpm, filepath.Join(dir, "swtpm")); err != nil {
			return err
		}
	}
	_, err = run(ctx, 10*time.Minute, "qemu-img", "snapshot", "-c", snap, disk)
	return err
}

// RevertSnapshot restores disk, NVRAM and TPM state. VM must be off.
func (l Libvirt) RevertSnapshot(ctx context.Context, name, snap string) error {
	if !ValidName(snap) {
		return errors.New("invalid snapshot name")
	}
	if err := l.requireOff(ctx, name); err != nil {
		return err
	}
	disk, err := l.MainDisk(ctx, name)
	if err != nil {
		return err
	}
	if _, err := run(ctx, 10*time.Minute, "qemu-img", "snapshot", "-a", snap, disk); err != nil {
		return err
	}
	nvram, tpm, err := l.nvramAndTPM(ctx, name)
	if err != nil {
		return err
	}
	dir := l.snapPath(name, snap)
	if nvram != "" {
		if _, err := os.Stat(filepath.Join(dir, "nvram.fd")); err == nil {
			if _, err := run(ctx, time.Minute, "cp", filepath.Join(dir, "nvram.fd"), nvram); err != nil {
				return err
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "swtpm")); err == nil {
		if err := os.RemoveAll(tpm); err != nil {
			return err
		}
		if _, err := run(ctx, time.Minute, "cp", "-a", filepath.Join(dir, "swtpm"), tpm); err != nil {
			return err
		}
	}
	return nil
}

func (l Libvirt) DeleteSnapshot(ctx context.Context, name, snap string) error {
	if !ValidName(snap) {
		return errors.New("invalid snapshot name")
	}
	if err := l.requireOff(ctx, name); err != nil {
		return err
	}
	disk, err := l.MainDisk(ctx, name)
	if err != nil {
		return err
	}
	if _, err := run(ctx, 10*time.Minute, "qemu-img", "snapshot", "-d", snap, disk); err != nil {
		return err
	}
	return os.RemoveAll(l.snapPath(name, snap))
}
