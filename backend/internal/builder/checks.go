package builder

// Offline image checks, run on the capture overlay (read-only mount) right before the
// WIM is written. They stand in for the VM-based tests T-01…T-09 (spec 02 §5) that this
// lab cannot run, and catch the mistakes that otherwise only show up on a client after
// the point of no return.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type checkResult struct {
	ID       string
	Name     string
	Passed   bool
	Critical bool // a failed critical check fails the capture
	Detail   string
}

// imageChecks mounts the NTFS partition read-only and inspects it.
func imageChecks(part string) ([]checkResult, error) {
	mnt, err := os.MkdirTemp("", "swim-check-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(mnt)
	if b, err := exec.Command("mount", "-t", "ntfs-3g", "-o", "ro,streams_interface=windows", part, mnt).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mount %s: %v: %s", part, err, b)
	}
	defer exec.Command("umount", mnt).Run() //nolint:errcheck
	return runChecks(mnt), nil
}

func runChecks(root string) []checkResult {
	p := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	var out []checkResult
	add := func(id, name string, critical, ok bool, detail string) {
		out = append(out, checkResult{id, name, ok, critical, detail})
	}

	// S-01 generalized by sysprep (otherwise every client gets the same SID / skips OOBE)
	state := readText(p("Windows/Setup/State/State.ini"))
	switch {
	case state == "":
		add("S-01", "Windows was generalized (sysprep /generalize)", true, false, "Windows/Setup/State/State.ini not found")
	case strings.Contains(state, "IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE"):
		add("S-01", "Windows was generalized (sysprep /generalize)", true, true, "ImageState = GENERALIZE_RESEAL_TO_OOBE")
	default:
		add("S-01", "Windows was generalized (sysprep /generalize)", true, false,
			"ImageState is "+between(state, "ImageState=", "\n")+" — run sysprep /generalize /oobe /shutdown on the Golden Master")
	}

	// S-02 files WinPE and the firmware need exist and are plain files (not WOF / links)
	var missing []string
	for _, f := range []string{"Windows/System32/ntoskrnl.exe", "Windows/System32/winload.efi", "Windows/System32/Dism.exe",
		"Windows/System32/bcdboot.exe", "Windows/Boot/EFI/bootmgfw.efi", "Windows/System32/config/SYSTEM", "Windows/System32/config/SOFTWARE"} {
		if why := plainFile(p(f)); why != "" {
			missing = append(missing, f+" ("+why+")")
		}
	}
	add("S-02", "boot and servicing files present and readable", true, len(missing) == 0, strings.Join(missing, "; "))

	// S-03 no SWIM agent identity in the image (each client must enroll on its own)
	var leftovers []string
	for _, f := range []string{"ProgramData/SWIM/state.json", "ProgramData/SWIM/agent.key", "ProgramData/SWIM/bootstrap.json"} {
		if _, err := os.Lstat(p(f)); err == nil {
			leftovers = append(leftovers, f)
		}
	}
	add("S-03", "no SWIM agent identity baked in", false, len(leftovers) == 0,
		strings.Join(leftovers, ", ")+map[bool]string{true: "", false: " — excluded from the WIM by the capture config (\\ProgramData\\SWIM)"}[len(leftovers) == 0])

	// S-04 no plain-text passwords left by sysprep/unattend files
	var secrets []string
	for _, f := range []string{"Windows/Panther/unattend.xml", "Windows/Panther/Unattend.xml", "Windows/Panther/autounattend.xml",
		"Windows/System32/Sysprep/unattend.xml", "Windows/System32/Sysprep/Panther/unattend.xml", "unattend.xml", "autounattend.xml",
		"swim/sysprep-unattend.xml"} {
		t := strings.ToLower(readText(p(f)))
		if strings.Contains(t, "<password>") && strings.Contains(t, "<plaintext>true</plaintext>") {
			secrets = append(secrets, f)
		}
	}
	add("S-04", "no plain-text passwords in unattend files", false, len(secrets) == 0,
		strings.Join(secrets, ", ")+map[bool]string{true: "", false: " — every client would carry this password; remove the file or use an encrypted value"}[len(secrets) == 0])

	// S-05 no servicing left half done (pending.xml makes the first boot slow or fail)
	_, perr := os.Lstat(p("Windows/WinSxS/pending.xml"))
	add("S-05", "no pending Windows servicing (WinSxS/pending.xml)", false, perr != nil,
		map[bool]string{true: "", false: "updates were installed without the reboot that finishes them — boot the Golden Master once more before sysprep"}[perr != nil])

	// S-06 no page/hibernation file in the capture (excluded anyway; reported for size)
	var big []string
	for _, f := range []string{"pagefile.sys", "hiberfil.sys", "swapfile.sys"} {
		if fi, err := os.Lstat(p(f)); err == nil {
			big = append(big, fmt.Sprintf("%s %.1f GB", f, float64(fi.Size())/1e9))
		}
	}
	add("S-06", "page/hibernation files (excluded from the WIM)", false, true, strings.Join(big, ", "))

	return out
}

// plainFile returns "" if p is a regular, non-reparse file that can be read.
func plainFile(p string) string {
	fi, err := os.Lstat(p)
	if err != nil {
		return "missing"
	}
	if !fi.Mode().IsRegular() {
		return "not a plain file"
	}
	var b [64]byte
	if n, err := unix.Lgetxattr(p, "system.ntfs_reparse_data", b[:]); err == nil && n >= 4 {
		if binary.LittleEndian.Uint32(b[:]) == 0x80000017 {
			return "still CompactOS/WOF-compressed"
		}
		return "reparse point"
	}
	f, err := os.Open(p)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	defer f.Close()
	if _, err := f.Read(b[:2]); err != nil {
		return "unreadable: " + err.Error()
	}
	return ""
}

// readText reads a small text file that may be UTF-16 ("" if missing).
func readText(p string) string {
	b, err := os.ReadFile(p)
	if err != nil || len(b) > 1<<20 {
		return ""
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE || bytes.Count(b, []byte{0}) > len(b)/3 {
		b = bytes.ReplaceAll(b, []byte{0}, nil)
		b = bytes.TrimPrefix(b, []byte{0xFF, 0xFE})
	}
	return strings.ReplaceAll(string(b), "\r", "")
}

func between(s, a, b string) string {
	if i := strings.Index(s, a); i >= 0 {
		s = s[i+len(a):]
		if j := strings.Index(s, b); j >= 0 {
			s = s[:j]
		}
		return strings.TrimSpace(s)
	}
	return "unknown"
}

// recordChecks stores the results and returns an error if a critical check failed.
func (w *Worker) recordChecks(ctx context.Context, imageID string, res []checkResult, logf func(string, ...any)) error {
	var crit []string
	for _, r := range res {
		mark := "PASS"
		if !r.Passed {
			mark = map[bool]string{true: "FAIL", false: "WARN"}[r.Critical]
		}
		logf("check %s %s: %s %s", r.ID, mark, r.Name, r.Detail)
		_, _ = w.Pool.Exec(ctx, `INSERT INTO image_tests(image_id, test_id, passed, duration_s, at) VALUES ($1,$2,$3,0,$4)`,
			imageID, r.ID, r.Passed, time.Now())
		status := map[bool]string{true: "ok", false: "warning"}[r.Passed]
		if !r.Passed && r.Critical {
			status = "failed"
			crit = append(crit, r.ID+" "+r.Name+": "+r.Detail)
		}
		w.event(ctx, imageID, "check "+r.ID, status, strings.TrimSpace(r.Name+" — "+r.Detail))
	}
	if len(crit) > 0 {
		return fmt.Errorf("image checks failed: %s", strings.Join(crit, "; "))
	}
	return nil
}
