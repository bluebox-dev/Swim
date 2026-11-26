//go:build windows

// Command swim-pe runs inside SWIM WinPE (started by winpeshl.ini). It works
// offline from the staging partition: verify → point of no return → partition
// → apply image (wimgapi) → inject drivers + agent + unattend → bcdboot → reboot.
// Progress is posted to the server when the network works; the outcome is always
// written to <staging>\swim\job\status.json for the agent to report afterwards.
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	peEntry  = "{5a1f0c3e-0000-4000-8000-5717aa000001}"
	typeESP  = typeESPGUID
	typeMSR  = "e3c9e316-0b5c-4db8-817d-f92df00215ae"
	maxTries = 2 // attempts after the point of no return
)

type fileRef struct {
	Version string `json:"version"`
	Path    string `json:"path,omitempty"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

type diskProfile struct {
	Mode      string `json:"mode"`
	SystemGB  int    `json:"system_gb"`
	DataLabel string `json:"data_label"`
}

type jobFile struct {
	JobID       string      `json:"job_id"`
	Server      string      `json:"server"`
	Pin         string      `json:"cert_sha256"`
	PEToken     string      `json:"pe_token"`
	Ticket      string      `json:"reimage_ticket"`
	Image       fileRef     `json:"image"`
	ImageFile   string      `json:"image_file"`
	Disk        diskProfile `json:"disk"`
	StagingGUID string      `json:"staging_guid"`
	OSGUID      string      `json:"os_guid"`
	ESPGUID     string      `json:"esp_guid"`
	OldBootID   string      `json:"old_boot_id"`
	Hostname    string      `json:"hostname"`
	// "bios" = legacy BIOS on an MBR disk: partitions are named "mbr-<disk signature>-<offset>",
	// "esp_guid" is the active system partition and boot files are written for BIOS.
	Firmware string `json:"firmware,omitempty"`
}

type status struct {
	JobID    string    `json:"job_id"`
	Status   string    `json:"status"` // pe | first_boot | aborted | failed
	Phase    string    `json:"phase"`
	Message  string    `json:"message"`
	Attempts int       `json:"attempts"` // post-PONR attempts
	PONR     bool      `json:"ponr"`
	Updated  time.Time `json:"updated"`
	Reported bool      `json:"reported"`
}

var (
	root    string // staging root, e.g. D:\
	job     jobFile
	st      status
	logBuf  bytes.Buffer
	httpc   *http.Client
	netOK   bool
	started = time.Now()
	bios    bool // the system disk is MBR and the machine boots through legacy BIOS
)

// bcdStore is the boot configuration of the installed system on the mounted system partition (Y:).
func bcdStore() string {
	if bios {
		return `Y:\Boot\BCD`
	}
	return `Y:\EFI\Microsoft\Boot\BCD`
}

func firmwareArg() string {
	if bios {
		return "BIOS"
	}
	return "UEFI"
}

func logf(format string, a ...any) {
	line := fmt.Sprintf("%s  %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
	fmt.Println(line)
	logBuf.WriteString(line + "\r\n")
	if root != "" {
		_ = os.WriteFile(filepath.Join(root, "swim", "job", "pe.log"), logBuf.Bytes(), 0o600)
	}
}

func saveStatus() {
	st.Updated = time.Now().UTC()
	b, _ := json.MarshalIndent(st, "", "  ")
	if root != "" {
		_ = os.WriteFile(filepath.Join(root, "swim", "job", "status.json"), b, 0o600)
	}
}

// report posts progress when the network is up; failures are ignored (offline is fine).
func report(statusName, phase string, pct int, msg, level string) {
	logf("[%s] %s", phase, msg)
	if httpc == nil || job.Server == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"status": statusName, "phase": phase, "progress": pct, "message": msg, "level": level})
	req, _ := http.NewRequest(http.MethodPost, job.Server+"/v1/pe/jobs/"+job.JobID+"/events", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+job.PEToken)
	req.Header.Set("Content-Type", "application/json")
	if resp, err := httpc.Do(req); err == nil {
		resp.Body.Close()
		if !netOK {
			netOK = true
			logf("network: reporting to %s", job.Server)
		}
	}
}

func run(timeout time.Duration, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return out.String(), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(out.String()))
		}
		return out.String(), nil
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return out.String(), fmt.Errorf("%s timed out", name)
	}
}

func diskpart(script string) (string, error) {
	f := filepath.Join(os.TempDir(), "swim-diskpart.txt")
	if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
		return "", err
	}
	logf("diskpart:\r\n%s", script)
	out, err := run(10*time.Minute, "diskpart.exe", "/s", f)
	if err == nil && strings.Contains(out, "DiskPart has encountered an error") {
		err = fmt.Errorf("diskpart error: %s", strings.TrimSpace(out))
	}
	return out, err
}

func reboot() {
	logf("rebooting")
	_, _ = run(time.Minute, "wpeutil.exe", "reboot")
	time.Sleep(time.Minute)
}

// abort: nothing was changed yet; the one-time boot entry is already consumed,
// so the next boot is the original Windows.
func abort(msg string) {
	st.Status, st.Message = "aborted", msg
	saveStatus()
	report("aborted", st.Phase, 0, "ABORTED before any change: "+msg+" — returning to the original Windows", "error")
	reboot()
}

// fail after the point of no return: retry once from partitioning, then stop
// here (WinPE stays the default boot) and wait for a technician.
func fail(msg string) {
	st.Status, st.Message = "failed", msg
	saveStatus()
	report("failed", st.Phase, 0, fmt.Sprintf("FAILED (attempt %d/%d): %s", st.Attempts, maxTries, msg), "error")
	if st.Attempts < maxTries {
		logf("retrying in 20 s")
		time.Sleep(20 * time.Second)
		reboot()
	}
	waitForServer()
}

// retryPlan is what the server hands out when an admin presses "retry".
type retryPlan struct {
	Action string  `json:"action"`
	Image  fileRef `json:"image"`
	WinPE  fileRef `json:"winpe"`
	Ticket string  `json:"ticket"`
}

// waitForServer: the old Windows is gone and this attempt failed. Stay here and ask the
// server every 20 s what to do; an admin can retry with another image (or this one)
// from the web console. Nothing is changed on disk while waiting.
func waitForServer() {
	logf("waiting for instructions from %s (an admin can press \"retry\" on this job). This screen stays on.", job.Server)
	lastNote := time.Time{}
	for i := 0; ; i++ {
		if time.Since(lastNote) > 10*time.Minute {
			report("", st.Phase, 0, "WinPE is waiting for an admin to retry this job (old Windows is gone; the machine stays in WinPE)", "warn")
			lastNote = time.Now()
		}
		if i%15 == 14 && !netOK {
			_, _ = run(3*time.Minute, "wpeutil.exe", "InitializeNetwork")
		}
		var plan retryPlan
		if err := getJSON("/v1/pe/jobs/"+job.JobID+"/next", &plan); err == nil && plan.Action == "retry" {
			doRetry(plan)
		}
		time.Sleep(20 * time.Second)
	}
}

func getJSON(p string, v any) error {
	if httpc == nil || job.Server == "" {
		return errors.New("offline")
	}
	req, _ := http.NewRequest(http.MethodGet, job.Server+p, nil)
	req.Header.Set("Authorization", "Bearer "+job.PEToken)
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	netOK = true
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// doRetry downloads the new image (and this job's refreshed WinPE), rewrites job.json
// and reboots into a fresh attempt. The plan is saved first, so a power cut resumes it.
func doRetry(plan retryPlan) {
	planPath := filepath.Join(root, "swim", "job", "retry.json")
	if b, err := json.Marshal(plan); err == nil {
		_ = os.WriteFile(planPath, b, 0o600)
	}
	st.Phase = "retry_download"
	saveStatus()
	newRel := filepath.Join("swim", "images", path.Base(plan.Image.Path))
	if old := filepath.Join(root, job.ImageFile); job.ImageFile != "" && !strings.EqualFold(job.ImageFile, newRel) {
		_ = os.Remove(old) // make room: staging holds one image
	}
	_ = os.MkdirAll(filepath.Join(root, "swim", "images"), 0o755)
	if err := download(plan.Image, filepath.Join(root, newRel), "image "+plan.Image.Version); err != nil {
		report("failed", "retry_download", 0, "retry: "+err.Error()+" — still waiting, press retry again", "error")
		_ = os.Remove(planPath)
		return
	}
	// same verification record the agent writes (inventory shows it as sha256_ok)
	if fi, err := os.Stat(filepath.Join(root, newRel)); err == nil {
		rec, _ := json.Marshal(map[string]any{"version": plan.Image.Version, "sha256": plan.Image.SHA256, "size": fi.Size(), "mtime_unix": fi.ModTime().Unix()})
		_ = os.WriteFile(filepath.Join(root, newRel)+".json", rec, 0o600)
	}
	// a WinPE the agent built from this machine's own WinRE stays: the server's generic
	// one may not be accepted by this machine's boot manager under Secure Boot
	_, statErr := os.Stat(filepath.Join(root, "swim", "winpe", "local-winre.txt"))
	if plan.WinPE.Path != "" && statErr == nil {
		logf("keeping this machine's own WinRE-based WinPE (not replacing it with %s)", plan.WinPE.Version)
	} else if plan.WinPE.Path != "" {
		tmp := filepath.Join(root, "swim", "winpe", "boot.wim.new")
		if err := download(plan.WinPE, tmp, "WinPE "+plan.WinPE.Version); err != nil {
			report("failed", "retry_download", 0, "retry: "+err.Error()+" — still waiting, press retry again", "error")
			_ = os.Remove(planPath)
			return
		}
		dst := filepath.Join(root, "swim", "winpe", "boot.wim")
		_ = os.Remove(dst)
		if err := os.Rename(tmp, dst); err != nil {
			report("failed", "retry_download", 0, "retry: replace WinPE: "+err.Error(), "error")
			_ = os.Remove(planPath)
			return
		}
	}
	job.Image = fileRef{Version: plan.Image.Version, Path: plan.Image.Path, SHA256: plan.Image.SHA256, Size: plan.Image.Size}
	job.ImageFile = newRel
	if plan.Ticket != "" {
		job.Ticket = plan.Ticket
	}
	b, _ := json.MarshalIndent(job, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "swim", "job", "job.json"), b, 0o600); err != nil {
		report("failed", "retry_download", 0, "retry: write job.json: "+err.Error(), "error")
		return
	}
	st = status{JobID: job.JobID, Status: "pe", Phase: "retry", PONR: true}
	saveStatus()
	_ = os.Remove(planPath)
	report("pe", "retry", 2, "retry ready ("+plan.Image.Version+") — restarting WinPE for a new attempt", "info")
	reboot()
}

// download fetches a server file to dst with SHA-256 verification; an existing file
// with the right hash is kept.
func download(ref fileRef, dst, what string) error {
	if sum, err := hashFile(dst); err == nil && strings.EqualFold(sum, ref.SHA256) {
		return nil
	}
	var free, total, totalFree uint64
	if p, err := windows.UTF16PtrFromString(root); err == nil && windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree) == nil &&
		ref.Size > 0 && free < uint64(ref.Size)+(256<<20) {
		return fmt.Errorf("staging partition has %.1f GB free, %s needs %.1f GB", float64(free)/(1<<30), what, float64(ref.Size)/(1<<30))
	}
	tr := httpc.Transport.(*http.Transport).Clone()
	cl := &http.Client{Transport: tr} // no overall timeout: images are large
	req, _ := http.NewRequest(http.MethodGet, job.Server+"/v1/pe/jobs/"+job.JobID+"/files/"+ref.Path, nil)
	req.Header.Set("Authorization", "Bearer "+job.PEToken)
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", what, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", what, resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	h := sha256.New()
	var n int64
	last := time.Now()
	buf := make([]byte, 4<<20)
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			if _, err := f.Write(buf[:k]); err != nil {
				f.Close()
				return fmt.Errorf("write %s: %w", what, err)
			}
			h.Write(buf[:k])
			n += int64(k)
			if time.Since(last) > 30*time.Second {
				pct := 0
				if ref.Size > 0 {
					pct = int(n * 100 / ref.Size)
				}
				report("", "retry_download", 1, fmt.Sprintf("downloading %s: %.1f / %.1f GB (%d%%)", what, float64(n)/(1<<30), float64(ref.Size)/(1<<30), pct), "info")
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return fmt.Errorf("download %s: %w", what, rerr)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(sum, ref.SHA256) {
		_ = os.Remove(dst)
		return fmt.Errorf("%s is damaged (sha %.16s, want %.16s)", what, sum, ref.SHA256)
	}
	report("", "retry_download", 1, fmt.Sprintf("%s downloaded and verified (%.1f GB)", what, float64(n)/(1<<30)), "info")
	return nil
}

func main() {
	fmt.Println("==============================================================")
	fmt.Println("  SWIM WinPE — reinstalling Windows. Do not turn off the PC.")
	fmt.Println("==============================================================")

	// 1. drivers (VirtIO etc.) so the disk and NIC are visible, then PnP + network
	_, _ = run(3*time.Minute, "wpeinit.exe")
	_ = filepath.Walk(`X:\swim\drivers`, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.EqualFold(filepath.Ext(p), ".inf") {
			if _, err := run(2*time.Minute, "drvload.exe", p); err != nil {
				logf("drvload %s: %v", filepath.Base(p), err)
			} else {
				logf("driver loaded: %s", filepath.Base(p))
			}
		}
		return nil
	})
	go func() { _, _ = run(3*time.Minute, "wpeutil.exe", "InitializeNetwork") }()

	// 2. find the staging partition (letters differ in WinPE)
	for i := 0; i < 60 && root == ""; i++ {
		for c := 'C'; c <= 'Z'; c++ {
			if _, err := os.Stat(string(c) + `:\swim\job\job.json`); err == nil {
				root = string(c) + `:\`
				break
			}
		}
		if root == "" {
			time.Sleep(2 * time.Second)
		}
	}
	if root == "" {
		logf("no SWIM job found on any disk — cannot continue. Rebooting to the original Windows.")
		time.Sleep(15 * time.Second)
		reboot()
	}
	b, err := os.ReadFile(filepath.Join(root, "swim", "job", "job.json"))
	if err == nil {
		err = json.Unmarshal(b, &job)
	}
	if err != nil {
		logf("job.json unreadable: %v", err)
		reboot()
	}
	// Windows reports GUIDs as "{...}", the partition table as bare lowercase: normalise once.
	norm := func(g string) string { return strings.Trim(strings.ToLower(strings.TrimSpace(g)), "{}") }
	job.StagingGUID, job.OSGUID, job.ESPGUID = norm(job.StagingGUID), norm(job.OSGUID), norm(job.ESPGUID)
	if sb, err := os.ReadFile(filepath.Join(root, "swim", "job", "status.json")); err == nil {
		_ = json.Unmarshal(sb, &st)
	}
	if st.JobID != job.JobID {
		st = status{JobID: job.JobID}
	}
	pin := strings.ToLower(job.Pin)
	httpc = &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no cert")
			}
			s := sha256.Sum256(raw[0])
			if hex.EncodeToString(s[:]) != pin {
				return errors.New("pin mismatch")
			}
			return nil
		}}}}
	logf("job %s on %s (staging %s), image %s, disk mode %s", job.JobID, job.Hostname, root, job.Image.Version, job.Disk.Mode)
	if b, err := os.ReadFile(filepath.Join(root, "swim", "job", "retry.json")); err == nil {
		var plan retryPlan
		if json.Unmarshal(b, &plan) == nil && plan.Image.Path != "" {
			logf("resuming an interrupted retry (%s)", plan.Image.Version)
			for i := 0; i < 30 && !netOK; i++ { // network comes up in the background
				report("", "retry", 1, "resuming the retry after a restart ("+plan.Image.Version+")", "info")
				time.Sleep(3 * time.Second)
			}
			doRetry(plan)
		}
	}
	if st.Status == "first_boot" {
		// booted again from a rescue disk that comes first in the boot order
		logf("this job already finished — rebooting into the new Windows")
		if !bootNextWindows() {
			report("", "first_boot", 92, "Windows is installed but this machine booted the rescue disk again — remove the rescue disk and restart", "warn")
		}
		reboot()
	}
	if st.PONR {
		st.Attempts++
		if st.Attempts > maxTries {
			fail("too many attempts")
		}
	}
	st.Status = "pe"
	saveStatus()

	// give the network a moment so the server sees progress (not required)
	for i := 0; i < 10 && !netOK; i++ {
		report("pe", "pe_start", 1, "SWIM WinPE started", "info")
		if !netOK {
			time.Sleep(3 * time.Second)
		}
	}
	if !netOK {
		logf("network not available — continuing offline; the agent reports afterwards")
	}

	// 3. verify (still safe: nothing changed)
	st.Phase = "verify"
	disk, parts, err := findDisk(job.StagingGUID)
	if err != nil {
		if !st.PONR {
			abort("disk layout: " + err.Error())
		}
		fail("disk layout: " + err.Error())
	}
	esp, osp := partByGUID(parts, job.ESPGUID), partByGUID(parts, job.OSGUID)
	if esp == nil || (job.Disk.Mode == "refresh" && osp == nil && !st.PONR) {
		abort("system (boot) or Windows partition not found on disk " + fmt.Sprint(disk))
	}
	if bios {
		logf("legacy BIOS / MBR disk: boot files go to partition %d as BIOS boot files", esp.Number)
		// On BIOS only "keep the partitions, format Windows" is supported, and the boot files
		// must live on their own partition: formatting Windows would otherwise take the boot
		// menu with it and a power cut would leave nothing to start from.
		if job.Disk.Mode != "refresh" {
			abort("re-partitioning is not supported on legacy BIOS (MBR) disks — choose to keep the partitions")
		}
		if osp != nil && osp.Number == esp.Number {
			abort("this BIOS machine boots from the Windows partition itself (no separate system partition) — not supported")
		}
	}
	if !st.PONR {
		report("pe", "verify", 3, "verifying image SHA-256 ("+job.Image.Version+")", "info")
		sum, err := hashFile(filepath.Join(root, job.ImageFile))
		if err != nil || !strings.EqualFold(sum, job.Image.SHA256) {
			abort(fmt.Sprintf("image on the staging partition is damaged (sha %.16s): %v", sum, err))
		}
		report("pe", "verify", 10, "image verified", "info")
		if hasDrivers() {
			// prove that offline servicing (DISM from the image) works here BEFORE wiping anything
			if osp != nil {
				if err := dismSelfTest(disk, osp); err != nil {
					abort("offline driver servicing is not possible in this WinPE: " + err.Error())
				}
				report("pe", "verify", 12, "offline driver servicing check passed", "info")
			}
		}
	}

	// 4. point of no return: from here WinPE is the default boot so a power cut resumes here
	if _, err := diskpart(fmt.Sprintf("select disk %d\nselect partition %d\nremove all noerr\nassign letter=Y noerr\n", disk, esp.Number)); err != nil {
		if !st.PONR {
			abort("cannot mount the system (boot) partition: " + err.Error())
		}
		fail("cannot mount the system (boot) partition: " + err.Error())
	}
	store := bcdStore()
	if _, err := os.Stat(store); err != nil && !st.PONR {
		abort("boot configuration not found at " + store)
	}
	if bios && !st.PONR {
		// the new Windows' boot files must fit on the (often 50 MB) system partition
		var free, total, totalFree uint64
		if yp, _ := windows.UTF16PtrFromString(`Y:\`); windows.GetDiskFreeSpaceEx(yp, &free, &total, &totalFree) == nil {
			logf("system partition: %d MB, %d MB free", total>>20, totalFree>>20)
			if totalFree < 8<<20 {
				abort(fmt.Sprintf("the system (boot) partition has only %d MB free — not enough for the new boot files", totalFree>>20))
			}
		}
	}
	if _, err := run(time.Minute, "bcdedit.exe", "/store", store, "/default", peEntry); err != nil {
		if !st.PONR {
			abort("cannot set WinPE as default boot: " + err.Error())
		}
	}
	if !st.PONR {
		st.PONR, st.Attempts = true, 1
		saveStatus()
	}
	st.Phase = "ponr"
	report("pe", "ponr", 14, "point of no return passed — WinPE is now the default boot until Windows is installed", "warn")

	// 5. partition
	st.Phase = "partition"
	saveStatus()
	if err := partition(disk, parts); err != nil {
		fail("partitioning: " + err.Error())
	}
	report("pe", "partition", 18, "disk partitioned ("+job.Disk.Mode+")", "info")

	// 6. apply
	st.Phase = "apply"
	saveStatus()
	// progress from bytes written on W: (wimgapi's own progress messages are not reliable here)
	stopProgress := make(chan struct{})
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		expect := float64(job.Image.Size) * 2.3 // typical LZX expansion; only for the percentage
		for {
			select {
			case <-stopProgress:
				return
			case <-t.C:
				var free, total, totalFree uint64
				wp, _ := windows.UTF16PtrFromString(`W:\`)
				if windows.GetDiskFreeSpaceEx(wp, &free, &total, &totalFree) == nil {
					used := float64(total - totalFree)
					pct := 18 + int(min(used/expect, 0.99)*62)
					report("pe", "apply", pct, fmt.Sprintf("applying %s: %.1f GB written (%s)", job.Image.Version, used/(1<<30),
						time.Since(started).Round(time.Second)), "info")
				}
			}
		}
	}()
	err = applyWIM(filepath.Join(root, job.ImageFile), `W:\`, func(p int) {
		report("pe", "apply", 18+p*62/100, fmt.Sprintf("applying %s: %d%%", job.Image.Version, p), "info")
	})
	close(stopProgress)
	if err != nil {
		fail("apply image: " + err.Error())
	}
	report("pe", "apply", 80, "image applied", "info")

	// 7. drivers + agent + unattend into the new Windows
	st.Phase = "inject"
	saveStatus()
	// Belt and braces: images are captured without CompactOS files now (the server refuses
	// older ones), but if any are left, WinPE can only run them with WOF attached to W:.
	_, _ = run(time.Minute, "fltmc.exe", "load", "wof")
	if out, err := run(time.Minute, "fltmc.exe", "attach", "wof", "W:"); err != nil {
		logf("fltmc attach wof W: (best effort): %v %s", err, lastLines(out, 2))
	}
	if hasDrivers() {
		out, err := run(30*time.Minute, `W:\Windows\System32\Dism.exe`, "/Image:W:\\", "/Add-Driver", `/Driver:X:\swim\drivers`, "/Recurse",
			`/ScratchDir:W:\`, "/English")
		if err != nil {
			// the new image's DISM may not run in a WinPE of an older Windows (e.g. Windows 11
			// installed from a Windows 10 recovery environment): try this WinPE's own DISM
			logf("DISM of the new image failed (%v) — trying the WinPE's DISM", err)
			out, err = run(30*time.Minute, "dism.exe", "/Image:W:\\", "/Add-Driver", `/Driver:X:\swim\drivers`, "/Recurse", `/ScratchDir:W:\`, "/English")
		}
		if err != nil {
			fail("inject drivers: " + err.Error())
		}
		logf("%s", lastLines(out, 6))
		report("pe", "inject", 84, "storage/network drivers added to the new Windows", "info")
	}
	if err := injectAgent(); err != nil {
		fail("inject agent: " + err.Error())
	}
	report("pe", "inject", 88, "SWIM agent installed into the new Windows (re-enrolls with the reimage ticket)", "info")

	// 8. boot files for the new Windows; drop the WinPE entry
	st.Phase = "boot_config"
	saveStatus()
	if _, err := run(5*time.Minute, `W:\Windows\System32\bcdboot.exe`, `W:\Windows`, "/s", "Y:", "/f", firmwareArg()); err != nil {
		if _, err2 := run(5*time.Minute, "bcdboot.exe", `W:\Windows`, "/s", "Y:", "/f", firmwareArg()); err2 != nil {
			fail("bcdboot: " + err.Error())
		}
	}
	_, _ = run(time.Minute, "bcdedit.exe", "/store", store, "/delete", peEntry, "/f")
	_, _ = run(time.Minute, "bcdedit.exe", "/store", store, "/delete", "{ramdiskoptions}", "/f")
	if job.OldBootID != "" {
		_, _ = run(time.Minute, "bcdedit.exe", "/store", store, "/delete", job.OldBootID, "/f")
	}
	// one-time boot into the new Windows even when a rescue disk is first in the boot order
	bootNextWindows()
	_, _ = diskpart(fmt.Sprintf("select disk %d\nselect partition %d\nremove letter=Y noerr\n", disk, esp.Number))

	st.Status, st.Phase, st.Message = "first_boot", "first_boot", fmt.Sprintf("Windows installed in %s", time.Since(started).Round(time.Second))
	saveStatus()
	report("pe", "first_boot", 92, "Windows installed in "+time.Since(started).Round(time.Second).String()+" — first boot (OOBE + agent) starting", "info")
	reboot()
}

// bootNextWindows sets UEFI BootNext to the "Windows Boot Manager" firmware entry that
// bcdboot registered for the system disk. Returns false when no such entry was found.
func bootNextWindows() bool {
	if bios {
		return true // BIOS has no firmware boot entries: the disk's boot manager starts its default, the new Windows
	}
	out, err := run(time.Minute, "bcdedit.exe", "/enum", "firmware", "/v")
	if err != nil {
		logf("bcdedit /enum firmware: %v", err)
		return false
	}
	var id string
	for _, blk := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n\n") {
		if !strings.Contains(blk, "Windows Boot Manager") || !strings.Contains(strings.ToLower(blk), `\efi\microsoft\boot\bootmgfw.efi`) {
			continue
		}
		for _, line := range strings.Split(blk, "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[0] == "identifier" && strings.HasPrefix(f[1], "{") {
				id = f[1]
			}
		}
		if id != "" {
			break
		}
	}
	if id == "" {
		logf("no Windows Boot Manager firmware entry found")
		return false
	}
	if _, err := run(time.Minute, "bcdedit.exe", "/set", "{fwbootmgr}", "bootsequence", id); err != nil {
		logf("set BootNext %s: %v", id, err)
		return false
	}
	logf("next boot: %s (Windows Boot Manager)", id)
	return true
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r", "")), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " | ")
}

func hasDrivers() bool {
	found := false
	_ = filepath.Walk(`X:\swim\drivers`, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.EqualFold(filepath.Ext(p), ".inf") {
			found = true
		}
		return nil
	})
	return found
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// dismSelfTest runs the CURRENT Windows' DISM against itself offline. If that
// works in this WinPE, the new image's DISM will too.
func dismSelfTest(disk int, osp *part) error {
	if _, err := diskpart(fmt.Sprintf("select disk %d\nselect partition %d\nremove all noerr\nassign letter=V noerr\n", disk, osp.Number)); err != nil {
		return err
	}
	defer diskpart(fmt.Sprintf("select disk %d\nselect partition %d\nremove letter=V noerr\n", disk, osp.Number))
	out, err := run(10*time.Minute, `V:\Windows\System32\Dism.exe`, "/Image:V:\\", "/Get-Drivers", `/ScratchDir:`+root, "/English")
	if err != nil {
		return err
	}
	logf("DISM self-test: %s", lastLines(out, 2))
	return nil
}

// partition prepares W: for the new Windows according to the disk profile.
func partition(disk int, parts []part) error {
	var b strings.Builder
	fmt.Fprintf(&b, "select disk %d\n", disk)
	switch job.Disk.Mode {
	case "refresh":
		osp := partByGUID(parts, job.OSGUID)
		if osp == nil {
			// an earlier attempt already re-created it: find the partition labelled by us
			osp = largestData(parts)
		}
		if osp == nil {
			return errors.New("Windows partition not found")
		}
		fmt.Fprintf(&b, "select partition %d\nremove all noerr\nformat quick fs=ntfs label=\"Windows\" override\nassign letter=W\n", osp.Number)
	case "repartition":
		staging := partByGUID(parts, job.StagingGUID)
		if staging == nil {
			return errors.New("staging partition not identified — refusing to repartition")
		}
		var start uint64 = 1 << 20
		var doomed []int
		for _, p := range parts {
			if p.Type == typeESP || p.Type == typeMSR {
				if e := p.Offset + p.Length; e > start {
					start = e
				}
				continue
			}
			if p.ID == job.StagingGUID || p.Number == staging.Number {
				continue // never delete the partition we are running from
			}
			doomed = append(doomed, p.Number)
		}
		// highest number first, so a renumbering after a delete cannot hit the wrong partition
		sort.Sort(sort.Reverse(sort.IntSlice(doomed)))
		for _, n := range doomed {
			fmt.Fprintf(&b, "select partition %d\ndelete partition override\n", n)
		}
		const mb = 1 << 20
		start = (start + mb - 1) / mb * mb
		end := staging.Offset
		if end <= start+40<<30 {
			return fmt.Errorf("only %d GB before the staging partition — need at least 40 GB", (end-start)>>30)
		}
		sysMB := (end - start) / mb
		if job.Disk.SystemGB > 0 && uint64(job.Disk.SystemGB)*1024 < sysMB {
			sysMB = uint64(job.Disk.SystemGB) * 1024
		}
		fmt.Fprintf(&b, "create partition primary size=%d offset=%d\nformat quick fs=ntfs label=\"Windows\"\nassign letter=W\n", sysMB, start/1024)
		if job.Disk.DataLabel != "" {
			dStart := start + sysMB*mb
			if dataMB := (end - dStart) / mb; dataMB >= 1024 {
				label := strings.NewReplacer(`"`, "", "\n", "").Replace(job.Disk.DataLabel)
				fmt.Fprintf(&b, "create partition primary size=%d offset=%d\nformat quick fs=ntfs label=\"%s\"\nassign letter=V\nremove letter=V noerr\n",
					dataMB, dStart/1024, label)
			} else {
				logf("no room for a data partition (%d MB left)", (end-dStart)/mb)
			}
		}
	default:
		return fmt.Errorf("unknown disk mode %q", job.Disk.Mode)
	}
	out, err := diskpart(b.String())
	logf("%s", lastLines(out, 8))
	if err != nil {
		return err
	}
	if _, err := os.Stat(`W:\`); err != nil {
		return errors.New("W: not available after partitioning")
	}
	return nil
}

func injectAgent() error {
	dir := `W:\Program Files\SWIM`
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	src, err := os.ReadFile(filepath.Join(root, "swim", "agent", "swim-agent.exe"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "swim-agent.exe"), src, 0o755); err != nil {
		return err
	}
	data := `W:\ProgramData\SWIM`
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	bs, _ := json.MarshalIndent(map[string]string{"server": job.Server, "cert_sha256": job.Pin, "reimage_ticket": job.Ticket}, "", "  ")
	if err := os.WriteFile(filepath.Join(data, "bootstrap.json"), bs, 0o600); err != nil {
		return err
	}
	if _, err := run(time.Minute, "reg.exe", "load", `HKLM\SWIMSYS`, `W:\Windows\System32\config\SYSTEM`); err != nil {
		return err
	}
	key := `HKLM\SWIMSYS\ControlSet001\Services\SWIMAgent`
	var firstErr error
	for _, v := range [][]string{
		{"Type", "REG_DWORD", "16"}, {"Start", "REG_DWORD", "2"}, {"ErrorControl", "REG_DWORD", "1"},
		{"ImagePath", "REG_EXPAND_SZ", `"C:\Program Files\SWIM\swim-agent.exe" run`},
		{"ObjectName", "REG_SZ", "LocalSystem"}, {"DisplayName", "REG_SZ", "SWIM Agent"},
	} {
		if _, err := run(time.Minute, "reg.exe", "add", key, "/v", v[0], "/t", v[1], "/d", v[2], "/f"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// keep the new Windows (and the SWIM staging partition) unencrypted until a
	// BitLocker policy with key escrow is applied (spec 08); WinPE must read staging later
	if _, err := run(time.Minute, "reg.exe", "add", `HKLM\SWIMSYS\ControlSet001\Control\BitLocker`, "/v", "PreventDeviceEncryption", "/t", "REG_DWORD", "/d", "1", "/f"); err != nil && firstErr == nil {
		firstErr = err
	}
	_, _ = run(time.Minute, "reg.exe", "unload", `HKLM\SWIMSYS`)
	if firstErr != nil {
		return firstErr
	}
	ua, err := os.ReadFile(filepath.Join(root, "swim", "job", "unattend.xml"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(`W:\Windows\Panther`, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(`W:\Windows\Panther\unattend.xml`, ua, 0o600); err != nil {
		return err
	}
	return nil
}

// ───────── disk layout (IOCTL_DISK_GET_DRIVE_LAYOUT_EX) ─────────

func partByGUID(ps []part, g string) *part { return partByID(ps, g) }

func largestData(ps []part) *part {
	var best *part
	for i := range ps {
		p := &ps[i]
		if p.Type == typeESP || p.Type == typeMSR || p.ID == strings.Trim(strings.ToLower(job.StagingGUID), "{}") {
			continue
		}
		if best == nil || p.Length > best.Length {
			best = p
		}
	}
	return best
}

func layout(n int) ([]part, error) {
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\PhysicalDrive%d`, n))
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, 48+144*128)
	var ret uint32
	if err := windows.DeviceIoControl(h, 0x00070050, nil, 0, &buf[0], uint32(len(buf)), &ret, nil); err != nil {
		return nil, err
	}
	ps, _, err := parseLayout(buf)
	return ps, err
}

func findDisk(stagingGUID string) (int, []part, error) {
	want := strings.Trim(strings.ToLower(stagingGUID), "{}")
	for n := 0; n < 16; n++ {
		ps, err := layout(n)
		if err != nil {
			continue
		}
		for _, p := range ps {
			if p.ID == want {
				bios = strings.HasPrefix(p.ID, "mbr-")
				for _, q := range ps {
					logf("disk %d partition %d: %.1f GB type %.8s id %s", n, q.Number, float64(q.Length)/(1<<30), q.Type, q.ID)
				}
				return n, ps, nil
			}
		}
	}
	return 0, nil, errors.New("staging partition " + want + " not found on any disk (storage driver missing?)")
}

// ───────── apply with wimgapi.dll (present in WinPE; no DISM needed) ─────────

var (
	wimgapi          = windows.NewLazySystemDLL("wimgapi.dll")
	pWIMCreateFile   = wimgapi.NewProc("WIMCreateFile")
	pWIMSetTempPath  = wimgapi.NewProc("WIMSetTemporaryPath")
	pWIMLoadImage    = wimgapi.NewProc("WIMLoadImage")
	pWIMApplyImage   = wimgapi.NewProc("WIMApplyImage")
	pWIMCloseHandle  = wimgapi.NewProc("WIMCloseHandle")
	pWIMRegisterCB   = wimgapi.NewProc("WIMRegisterMessageCallback")
	pWIMUnregisterCB = wimgapi.NewProc("WIMUnregisterMessageCallback")
)

const (
	wimMsgProgress  = 0x9476 + 1 // WM_APP + 0x1476 + 1
	wimGenericRead  = 0x80000000
	wimOpenExisting = 3
)

// enablePrivileges turns on what wimgapi needs to restore files with ACLs.
func enablePrivileges(names ...string) {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return
	}
	defer tok.Close()
	for _, n := range names {
		var luid windows.LUID
		np, _ := windows.UTF16PtrFromString(n)
		if windows.LookupPrivilegeValue(nil, np, &luid) != nil {
			continue
		}
		tp := windows.Tokenprivileges{PrivilegeCount: 1}
		tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
		_ = windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil)
	}
}

func applyWIM(wimPath, target string, progress func(int)) error {
	enablePrivileges("SeBackupPrivilege", "SeRestorePrivilege", "SeSecurityPrivilege", "SeTakeOwnershipPrivilege")
	wp, _ := windows.UTF16PtrFromString(wimPath)
	var created uint32
	h, _, e := pWIMCreateFile.Call(uintptr(unsafe.Pointer(wp)), wimGenericRead, wimOpenExisting, 0, 0, uintptr(unsafe.Pointer(&created)))
	if h == 0 {
		return fmt.Errorf("WIMCreateFile: %v", e)
	}
	defer pWIMCloseHandle.Call(h)
	tp, _ := windows.UTF16PtrFromString(target)
	if r, _, e := pWIMSetTempPath.Call(h, uintptr(unsafe.Pointer(tp))); r == 0 {
		return fmt.Errorf("WIMSetTemporaryPath: %v", e)
	}
	last := -1
	cb := syscall.NewCallback(func(msg, wParam, lParam, user uintptr) uintptr {
		if msg == wimMsgProgress {
			if p := int(wParam); p != last && (p%5 == 0 || p == 100) {
				last = p
				progress(p)
			}
		}
		return 0
	})
	pWIMRegisterCB.Call(h, cb, 0)
	defer pWIMUnregisterCB.Call(h, cb)
	img, _, e := pWIMLoadImage.Call(h, 1)
	if img == 0 {
		return fmt.Errorf("WIMLoadImage: %v", e)
	}
	defer pWIMCloseHandle.Call(img)
	if r, _, e := pWIMApplyImage.Call(img, uintptr(unsafe.Pointer(tp)), 0); r == 0 {
		return fmt.Errorf("WIMApplyImage: %v", e)
	}
	return nil
}
