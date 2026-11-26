//go:build windows

package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/nacl/box"
	"golang.org/x/sys/windows"

	"swim/pkg/manifest"
)

// SWIM-PE boot entry (fixed GUID, spec 05) and the staging volume label.
const (
	peEntry      = "{5a1f0c3e-0000-4000-8000-5717aa000001}"
	stagingLabel = "SWIMSTAGE"
)

var guidRe = regexp.MustCompile(`\{[0-9a-fA-F-]{36}\}`)

// JobFile is S:\swim\job\job.json — everything swim-pe needs, offline.
type JobFile struct {
	JobID       string               `json:"job_id"`
	Server      string               `json:"server"`
	Pin         string               `json:"cert_sha256"`
	PEToken     string               `json:"pe_token"`
	Ticket      string               `json:"reimage_ticket"`
	Image       manifest.FileRef     `json:"image"`
	ImageFile   string               `json:"image_file"` // relative to the staging root
	Disk        manifest.DiskProfile `json:"disk"`
	StagingGUID string               `json:"staging_guid"`
	OSGUID      string               `json:"os_guid"`
	ESPGUID     string               `json:"esp_guid"`
	OldBootID   string               `json:"old_boot_id"`
	Hostname    string               `json:"hostname"`
	CreatedAt   time.Time            `json:"created_at"`
	// Windows boot counter at hand-off: a different value later means the machine restarted
	// (does not depend on the clock, which some machines get wrong after a reboot)
	WinBootID int64 `json:"win_boot_id,omitempty"`
	// how many times this job has restarted towards WinPE (1 = the first hand-off)
	HandoffTries int `json:"handoff_tries,omitempty"`
}

// maxHandoffTries: a restart that comes back into the old Windows is tried again on its
// own this many times in total before the job is given up. Nothing on the disk has changed
// at that point, so trying again is safe; some machines (seen on virtual ones) miss the
// one-time boot now and then.
const maxHandoffTries = 3

func runCmd(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := command(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func ps(ctx context.Context, timeout time.Duration, script string) (string, error) {
	return runCmd(ctx, timeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		"[Console]::OutputEncoding=[Text.Encoding]::UTF8; $ErrorActionPreference='Stop'; "+script)
}

func openSecrets(st *State, sealed string) (manifest.ReimageSecrets, error) {
	var sec manifest.ReimageSecrets
	plain, err := openSealed(st, sealed)
	if err != nil {
		return sec, err
	}
	return sec, json.Unmarshal(plain, &sec)
}

// openSealed opens a NaCl sealed box addressed to this machine's X25519 key.
func openSealed(st *State, sealed string) ([]byte, error) {
	privB, err := unseal(st.SealedX25519)
	if err != nil {
		return nil, fmt.Errorf("agent key: %w", err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(privB)
	if err != nil {
		return nil, err
	}
	var pk, sk [32]byte
	copy(pk[:], priv.PublicKey().Bytes())
	copy(sk[:], privB)
	blob, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, err
	}
	plain, ok := box.OpenAnonymous(nil, blob, &pk, &sk)
	if !ok {
		return nil, errors.New("cannot open sealed secrets (wrong machine key)")
	}
	return plain, nil
}

// startReimage validates the job and runs staging in the background, so the
// command channel stays free (progress goes to the job's event stream).
func startReimage(ctx context.Context, ec *execCtx, cmd manifest.Command) (string, error) {
	var a manifest.ReimageArgs
	if err := json.Unmarshal(cmd.Args, &a); err != nil || a.JobID == "" {
		return "", errors.New("invalid reimage arguments")
	}
	sec, err := openSecrets(ec.st, a.Sealed)
	if err != nil {
		return "", err
	}
	reimageActive.Store(true)
	go func() {
		j := &reimageJob{ec: ec, a: a, sec: sec}
		if err := j.run(ctx); err != nil {
			reimageActive.Store(false)
			ec.lg.Printf("reimage %s: %v", a.JobID, err)
			j.event("failed", j.phase, nil, err.Error(), "error")
		}
	}()
	return "reimage job " + a.JobID + " started: preparing this machine (staging, download, drivers) before the reboot into WinPE", nil
}

type reimageJob struct {
	ec    *execCtx
	a     manifest.ReimageArgs
	sec   manifest.ReimageSecrets
	phase string
	root  string // staging root, e.g. E:\
	// quiet: not a reimage job (pre-staging) — no job events; progress goes to onProgress
	quiet      bool
	onProgress func(pct int)
}

var errCancelled = errors.New("job cancelled by an administrator")

func (j *reimageJob) event(status, phase string, pct *int, msg, level string) bool {
	j.phase = phase
	if j.quiet {
		if pct != nil && j.onProgress != nil {
			j.onProgress(*pct)
		}
		j.ec.lg.Printf("prestage: [%s] %s", phase, msg)
		return false
	}
	var out struct {
		Cancel bool `json:"cancel"`
	}
	_ = j.ec.c.PostJSON("/v1/agent/jobs/"+j.a.JobID+"/events", j.ec.tok,
		map[string]any{"status": status, "phase": phase, "progress": pct, "message": msg, "level": level}, &out)
	j.ec.lg.Printf("job %s: [%s] %s", j.a.JobID, phase, msg)
	return out.Cancel
}

func pct(n int) *int { return &n }

func (j *reimageJob) run(ctx context.Context) error {
	if j.event("staging", "preflight", pct(1), "checking this machine", "info") {
		return errCancelled
	}
	// 1. preflight: UEFI + BitLocker
	if out, err := runCmd(ctx, time.Minute, "bcdedit", "/enum", "{current}"); err != nil || !strings.Contains(strings.ToLower(out), "winload.efi") {
		return errors.New("this machine does not boot in UEFI mode — SWIM WinPE needs UEFI")
	}
	if out, _ := runCmd(ctx, time.Minute, "manage-bde", "-status", "C:"); strings.Contains(out, "Protection On") {
		if _, err := runCmd(ctx, time.Minute, "manage-bde", "-protectors", "-disable", "C:", "-RebootCount", "3"); err != nil {
			return fmt.Errorf("suspend BitLocker: %w", err)
		}
		j.event("", "preflight", nil, "BitLocker suspended for 3 reboots", "info")
	}

	// 2. staging partition
	need := j.a.Image.Size + 3<<30 // image + WinPE + drivers + agent + headroom
	vol, err := j.ensureStaging(ctx, need)
	if err != nil {
		return err
	}
	j.root = vol.Letter + `:\`
	if out, err := ps(ctx, 2*time.Minute, `(Get-CimInstance -Namespace root/cimv2/Security/MicrosoftVolumeEncryption -ClassName Win32_EncryptableVolume -Filter "DriveLetter='`+vol.Letter+`:'").ConversionStatus`); err == nil {
		if cs := strings.TrimSpace(out); cs != "" && cs != "0" {
			return fmt.Errorf("staging partition %s: is BitLocker-encrypted (conversion status %s) — WinPE could not read it; decrypt it (manage-bde -off %s:) and retry", vol.Letter, cs, vol.Letter)
		}
	}
	j.event("", "staging", pct(5), fmt.Sprintf("staging partition %s ready (%.1f GB)", vol.Letter+":", float64(vol.Size)/(1<<30)), "info")

	// 3. downloads (image is the big one; resume-safe: a complete, verified file is reused)
	imgRel := filepath.Join("swim", "images", j.a.Image.Version+".wim")
	if err := j.fetch(j.a.Image, imgRel, 5, 80, "image "+j.a.Image.Version); err != nil {
		return err
	}
	writeImageRecord(filepath.Join(j.root, imgRel), j.a.Image)
	if err := j.fetch(j.a.BootSDI, filepath.Join("swim", "winpe", "boot.sdi"), 80, 81, "boot.sdi"); err != nil {
		return err
	}
	if err := j.fetch(j.a.Agent, filepath.Join("swim", "agent", "swim-agent.exe"), 81, 83, "agent "+j.a.Agent.Version); err != nil {
		return err
	}

	// 4. this machine's storage + network drivers → per-job WinPE (and the new Windows)
	if j.event("", "drivers", pct(84), "exporting storage and network drivers", "info") {
		return errCancelled
	}
	pe, err := j.driversAndWinPE(ctx)
	if err != nil {
		return err
	}
	// Preferred: WinPE from this machine's own WinRE (accepted by its boot manager under
	// Secure Boot). Fallback: the server's WinPE with this machine's drivers.
	local := false
	if j.a.PEExe.Path != "" {
		exe := filepath.Join("swim", "winpe", "swim-pe.exe")
		if err := j.fetch(j.a.PEExe, exe, 86, 87, "swim-pe "+j.a.PEExe.Version); err != nil {
			j.event("", "drivers", nil, "swim-pe download failed, using the server WinPE: "+err.Error(), "warn")
		} else if j.event("", "drivers", pct(87), "building WinPE from this machine's recovery environment (WinRE)", "info") {
			return errCancelled
		} else if ver, err := j.localWinPE(ctx, filepath.Join(j.root, exe)); err != nil {
			j.event("", "drivers", nil, "WinPE from this machine's WinRE failed, using the server WinPE: "+err.Error(), "warn")
		} else {
			local = true
			j.event("", "drivers", pct(95), "WinPE built from this machine's WinRE "+ver+" (+ swim-pe "+j.a.PEExe.Version+")", "info")
		}
	}
	if !local {
		_ = os.Remove(filepath.Join(j.root, "swim", "winpe", "local-winre.txt"))
		if err := j.fetch(pe, filepath.Join("swim", "winpe", "boot.wim"), 88, 95, "WinPE "+pe.Version); err != nil {
			return err
		}
	}

	// 5. job files for swim-pe
	if j.event("", "prepare", pct(96), "writing job and unattend files", "info") {
		return errCancelled
	}
	oldBoot, _ := runCmd(ctx, time.Minute, "bcdedit", "/v", "/enum", "{current}")
	jf := JobFile{JobID: j.a.JobID, Server: j.ec.st.Server, Pin: j.ec.st.Pin, PEToken: j.sec.PEToken, Ticket: j.sec.Ticket,
		Image: j.a.Image, ImageFile: imgRel, Disk: j.a.Disk, StagingGUID: vol.GUID, OSGUID: vol.OSGUID, ESPGUID: vol.ESPGUID,
		OldBootID: guidRe.FindString(oldBoot), Hostname: j.a.Hostname, CreatedAt: time.Now().UTC(), WinBootID: winBootID()}
	jobDir := filepath.Join(j.root, "swim", "job")
	_ = os.RemoveAll(jobDir)
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(jf, "", "  ")
	if err := os.WriteFile(filepath.Join(jobDir, "job.json"), b, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(jobDir, "unattend.xml"), []byte(unattendXML(j.a, j.sec.AdminPassword)), 0o600); err != nil {
		return err
	}
	_, _ = runCmd(ctx, time.Minute, "icacls", filepath.Join(j.root, "swim"), "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F")

	// 6. last check before touching boot configuration
	if j.event("", "prepare", pct(98), "configuring one-time boot into SWIM WinPE", "info") {
		return errCancelled
	}
	if err := j.bootIntoPE(ctx, vol.Letter); err != nil {
		return err
	}
	j.event("handoff", "rebooting", pct(100), "rebooting into SWIM WinPE in 15 s — the rest runs offline", "info")
	_, err = runCmd(ctx, time.Minute, "shutdown.exe", "/r", "/t", "15", "/c", "SWIM: Windows is being reinstalled. Do not turn off the computer.", "/d", "p:4:1")
	return err
}

type stagingVol struct {
	Letter, GUID, OSGUID, ESPGUID string
	Size                          int64
}

// ensureStaging finds or creates the SWIMSTAGE partition by shrinking C:.
func (j *reimageJob) ensureStaging(ctx context.Context, need int64) (*stagingVol, error) {
	needGB := (need + (1 << 30) - 1) >> 30
	if needGB < 20 {
		needGB = 20
	}
	script := fmt.Sprintf(`
$need = %d * 1GB
$os = Get-Partition -DriveLetter C
$esp = Get-Partition -DiskNumber $os.DiskNumber | Where-Object { $_.GptType -eq '{c12a7328-f81f-11d2-ba4b-00a0c93ec93b}' } | Select-Object -First 1
$st = Get-Volume | Where-Object { $_.FileSystemLabel -eq '%s' } | Select-Object -First 1
$created = $false
if (-not $st) {
  $sup = Get-PartitionSupportedSize -DriveLetter C
  if (($os.Size - $sup.SizeMin) -lt ($need + 10GB)) { throw "not enough free space on C: to create a $([int]($need/1GB)) GB staging partition" }
  Resize-Partition -DriveLetter C -Size ($os.Size - $need)
  $p = New-Partition -DiskNumber $os.DiskNumber -Size ($need - 16MB) -AssignDriveLetter
  $null = Format-Volume -Partition $p -FileSystem NTFS -NewFileSystemLabel '%s' -Confirm:$false
  $st = Get-Volume -Partition $p
  $created = $true
}
$sp = Get-Partition -Volume $st
if (-not $sp.DriveLetter -or $sp.DriveLetter -eq [char]0) { $sp | Add-PartitionAccessPath -AssignDriveLetter; $sp = Get-Partition -Volume $st }
if ($sp.Size -lt ($need - 64MB)) { throw "existing staging partition is too small ($([int]($sp.Size/1GB)) GB, need $([int]($need/1GB)) GB)" }
[pscustomobject]@{ letter=[string]$sp.DriveLetter; guid=$sp.Guid; size=$sp.Size; os=$os.Guid; esp=$esp.Guid; created=$created } | ConvertTo-Json -Compress`,
		needGB, stagingLabel, stagingLabel)
	j.event("", "staging", pct(2), fmt.Sprintf("preparing %d GB staging partition (shrinking C: if needed)", needGB), "info")
	out, err := ps(ctx, 10*time.Minute, script)
	if err != nil {
		return nil, fmt.Errorf("staging partition: %w", err)
	}
	var v struct {
		Letter, GUID, OS, ESP string
		Size                  int64
		Created               bool
	}
	i := strings.Index(out, "{")
	if i < 0 {
		return nil, fmt.Errorf("staging partition: unexpected output: %s", out)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out[i:])), &v); err != nil {
		return nil, fmt.Errorf("staging partition: %v: %s", err, out)
	}
	if v.Letter == "" || v.GUID == "" || v.OS == "" || v.ESP == "" {
		return nil, fmt.Errorf("staging partition: incomplete layout info: %s", out)
	}
	return &stagingVol{Letter: v.Letter, GUID: strings.ToLower(v.GUID), OSGUID: strings.ToLower(v.OS), ESPGUID: strings.ToLower(v.ESP), Size: v.Size}, nil
}

// fetch downloads a server file into the staging root and verifies SHA-256.
// An existing file with the right hash is reused (job re-run after a failure).
func (j *reimageJob) fetch(f manifest.FileRef, rel string, from, to int, label string) error {
	dst := filepath.Join(j.root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if sum, _, err := fileSHA256(dst); err == nil && strings.EqualFold(sum, f.SHA256) {
		j.event("", "downloading", pct(to), label+" already staged (SHA-256 ok)", "info")
		return nil
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if j.event("", "downloading", pct(from), fmt.Sprintf("downloading %s (%.2f GB)", label, float64(f.Size)/(1<<30)), "info") {
			return errCancelled
		}
		sum, err := j.download(f, dst, from, to, label)
		if err == nil && strings.EqualFold(sum, f.SHA256) {
			j.event("", "downloading", pct(to), label+" downloaded, SHA-256 verified", "info")
			return nil
		}
		if err == nil {
			err = fmt.Errorf("SHA-256 mismatch (got %s)", sum[:16])
		}
		if errors.Is(err, errCancelled) {
			return err
		}
		j.event("", "downloading", nil, fmt.Sprintf("%s: attempt %d failed: %v", label, attempt, err), "warn")
		time.Sleep(time.Duration(attempt) * 10 * time.Second)
	}
	return fmt.Errorf("could not download %s", label)
}

func (j *reimageJob) download(f manifest.FileRef, dst string, from, to int, label string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, j.ec.c.Server+"/v1/agent/files/"+strings.TrimLeft(f.Path, "/"), nil)
	req.Header.Set("Authorization", "Bearer "+j.ec.tok)
	hc := *j.ec.c.hc
	hc.Timeout = 6 * time.Hour
	var resp *http.Response
	var err error
	// the server serves only a few large downloads at a time (503 = wait for a free slot);
	// waiting does not count as a failed attempt
	for waited := time.Duration(0); ; {
		resp, err = hc.Do(req.Clone(req.Context()))
		if err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusTooManyRequests {
			break
		}
		resp.Body.Close()
		if waited > 6*time.Hour {
			return "", errors.New("the server stayed busy for 6 hours")
		}
		wait := time.Duration(30+rand.IntN(60)) * time.Second // spread retries of many machines
		if j.event("", "downloading", nil, fmt.Sprintf("server busy serving other machines — waiting for a download slot (%s)", label), "info") {
			return "", errCancelled
		}
		time.Sleep(wait)
		waited += wait
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	h := sha256.New()
	buf := make([]byte, 4<<20)
	var done int64
	lastPct, lastSend := -1, time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return "", err
			}
			h.Write(buf[:n])
			done += int64(n)
			if f.Size > 0 && time.Since(lastSend) > 5*time.Second {
				p := from + int(float64(to-from)*float64(done)/float64(f.Size))
				if p != lastPct {
					lastPct, lastSend = p, time.Now()
					if j.event("", "downloading", pct(p), fmt.Sprintf("downloading %s: %.1f / %.1f GB", label, float64(done)/(1<<30), float64(f.Size)/(1<<30)), "info") {
						return "", errCancelled
					}
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSHA256(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// driversAndWinPE exports third-party storage/network drivers, uploads them,
// and gets back a per-job boot.wim that contains them.
func (j *reimageJob) driversAndWinPE(ctx context.Context) (manifest.FileRef, error) {
	dir := filepath.Join(j.root, "swim", "drivers")
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0o700)
	list, err := ps(ctx, 5*time.Minute, `Get-CimInstance Win32_PnPSignedDriver | Where-Object { $_.InfName -like 'oem*.inf' -and $_.DeviceClass -in 'SCSIADAPTER','HDC','NET','DISKDRIVE' } | Select-Object -ExpandProperty InfName -Unique`)
	if err != nil {
		return manifest.FileRef{}, fmt.Errorf("list drivers: %w", err)
	}
	var infs []string
	for _, l := range strings.Fields(list) {
		if strings.HasPrefix(strings.ToLower(l), "oem") && strings.HasSuffix(strings.ToLower(l), ".inf") {
			infs = append(infs, l)
			sub := filepath.Join(dir, strings.TrimSuffix(l, ".inf"))
			_ = os.MkdirAll(sub, 0o700)
			if _, err := runCmd(ctx, 5*time.Minute, "pnputil.exe", "/export-driver", l, sub); err != nil {
				j.event("", "drivers", nil, "export "+l+" failed: "+err.Error(), "warn")
			}
		}
	}
	j.event("", "drivers", pct(85), fmt.Sprintf("exported %d driver package(s): %s", len(infs), strings.Join(infs, ", ")), "info")

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	files := 0
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err == nil {
			_, err = w.Write(b)
			files++
		}
		return err
	})
	if files == 0 { // nothing machine-specific: still send a marker so the server can build the per-job WinPE
		w, _ := zw.Create("none.txt")
		_, _ = w.Write([]byte("no third-party storage or network drivers on this machine\n"))
	}
	_ = zw.Close()

	req, _ := http.NewRequest(http.MethodPost, j.ec.c.Server+"/v1/agent/jobs/"+j.a.JobID+"/drivers", &zbuf)
	req.Header.Set("Authorization", "Bearer "+j.ec.tok)
	req.Header.Set("Content-Type", "application/zip")
	hc := *j.ec.c.hc
	hc.Timeout = 20 * time.Minute
	resp, err := hc.Do(req)
	if err != nil {
		return manifest.FileRef{}, fmt.Errorf("upload drivers: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return manifest.FileRef{}, fmt.Errorf("server could not build WinPE: HTTP %d %s", resp.StatusCode, body)
	}
	var ref manifest.FileRef
	if err := json.Unmarshal(body, &ref); err != nil || ref.Path == "" {
		return manifest.FileRef{}, fmt.Errorf("bad WinPE reply: %s", body)
	}
	return ref, nil
}

// bootIntoPE creates the RAM-disk WinPE entry and sets it as a ONE-TIME boot.
// If anything goes wrong before swim-pe passes the point of no return, the
// next reboot lands back in this Windows.
func (j *reimageJob) bootIntoPE(ctx context.Context, letter string) error {
	return configurePEBoot(ctx, letter)
}

func configurePEBoot(ctx context.Context, letter string) error {
	dev := "ramdisk=[" + letter + `:]\swim\winpe\boot.wim,{ramdiskoptions}`
	_, _ = runCmd(ctx, time.Minute, "bcdedit", "/delete", peEntry, "/f")
	// "bcdedit /enum {ramdiskoptions}" exits 0 even when the object is missing, so just
	// create it and ignore "already exists"; the /set below fails loudly if it is really absent.
	_, _ = runCmd(ctx, time.Minute, "bcdedit", "/create", "{ramdiskoptions}", "/d", "SWIM WinPE")
	steps := [][]string{
		{"/set", "{ramdiskoptions}", "ramdisksdidevice", "partition=" + letter + ":"},
		{"/set", "{ramdiskoptions}", "ramdisksdipath", `\swim\winpe\boot.sdi`},
		{"/create", peEntry, "/d", "SWIM WinPE", "/application", "osloader"},
		{"/set", peEntry, "device", dev},
		{"/set", peEntry, "osdevice", dev},
		{"/set", peEntry, "path", `\windows\system32\boot\winload.efi`},
		{"/set", peEntry, "systemroot", `\windows`},
		{"/set", peEntry, "winpe", "yes"},
		{"/set", peEntry, "detecthal", "yes"},
		{"/bootsequence", peEntry},
	}
	for _, s := range steps {
		if _, err := runCmd(ctx, time.Minute, "bcdedit", s...); err != nil {
			_, _ = runCmd(ctx, time.Minute, "bcdedit", "/delete", peEntry, "/f")
			return fmt.Errorf("boot configuration: %w", err)
		}
	}
	return nil
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xmlEscapeTo(&b, s)
	return b.String()
}

func xmlEscapeTo(b *bytes.Buffer, s string) error {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return nil
}

// unattendXML: computer name, local administrator, no OOBE questions. Windows
// replaces the password with *SENSITIVE*DATA*DELETED* in its cached copy.
func unattendXML(a manifest.ReimageArgs, password string) string {
	const comp = `processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS"`
	return `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup" ` + comp + `>
      <ComputerName>` + xmlEscape(a.Hostname) + `</ComputerName>
      <TimeZone>` + xmlEscape(a.TimeZone) + `</TimeZone>
    </component>
    <component name="Microsoft-Windows-SecureStartup-FilterDriver" ` + comp + `>
      <PreventDeviceEncryption>true</PreventDeviceEncryption>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-International-Core" ` + comp + `>
      <InputLocale>0409:00000409;041e:0000041e</InputLocale><SystemLocale>en-US</SystemLocale><UILanguage>en-US</UILanguage><UserLocale>en-US</UserLocale>
    </component>
    <component name="Microsoft-Windows-Shell-Setup" ` + comp + `>
      <OOBE><HideEULAPage>true</HideEULAPage><HideOnlineAccountScreens>true</HideOnlineAccountScreens><HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE><HideOEMRegistrationScreen>true</HideOEMRegistrationScreen><ProtectYourPC>3</ProtectYourPC></OOBE>
      <UserAccounts><LocalAccounts><LocalAccount wcm:action="add">
        <Name>` + xmlEscape(a.Admin) + `</Name><Group>Administrators</Group>
        <Password><Value>` + xmlEscape(password) + `</Value><PlainText>true</PlainText></Password>
      </LocalAccount></LocalAccounts></UserAccounts>
    </component>
  </settings>
</unattend>
`
}

// ───────── report the outcome after the reboot(s) ─────────

// stagingRoot finds the SWIMSTAGE volume by label (letters change between Windows and WinPE).
func stagingRoot() string {
	for c := 'C'; c <= 'Z'; c++ {
		root := string(c) + `:\`
		if _, err := os.Stat(filepath.Join(root, "swim", "job", "job.json")); err == nil {
			return root
		}
	}
	return ""
}

// reportFinishedJob runs at agent start: if swim-pe left a result on the staging
// partition (completed in the new Windows, or aborted back in the old one), send it.
func reportFinishedJob(c *Client, tok string, lg interface{ Printf(string, ...any) }) {
	root := stagingRoot()
	if root == "" {
		// a staging partition without a job (left by an install an older agent finished,
		// or orphaned): nothing needs it, give the space back to C:
		if r := stagingByLabel(); r != "" {
			if _, ok := readPrestageMarker(r); !ok { // a partition kept for a pre-staged image stays
				releaseAfterInstall(lg)
			}
		}
		return
	}
	stPath := filepath.Join(root, "swim", "job", "status.json")
	b, err := os.ReadFile(stPath)
	if errors.Is(err, os.ErrNotExist) {
		reportMissedHandoff(c, tok, root, stPath, lg)
		return
	}
	if err != nil {
		return
	}
	var st struct {
		JobID    string `json:"job_id"`
		Status   string `json:"status"`
		Phase    string `json:"phase"`
		Message  string `json:"message"`
		Reported bool   `json:"reported"`
	}
	if json.Unmarshal(b, &st) != nil || st.JobID == "" {
		return
	}
	if st.Reported {
		// an install that an older agent finished with "keep installed only": give the space back
		if st.Status == "completed" || st.Status == "first_boot" {
			if !keepPrestagedAfterInstall(root, lg) {
				releaseAfterInstall(lg)
			}
		}
		return
	}
	status := st.Status
	if status == "first_boot" {
		status = "completed"
	}
	logB, _ := os.ReadFile(filepath.Join(root, "swim", "job", "pe.log"))
	if len(logB) > 256<<10 {
		logB = logB[len(logB)-256<<10:]
	}
	msg := st.Message
	if status == "completed" {
		host, _ := os.Hostname()
		msg = "new Windows is up (" + host + "), agent re-enrolled"
	}
	err = c.PostJSON("/v1/agent/jobs/"+st.JobID+"/events", tok, map[string]any{"status": status, "phase": "done", "progress": 100,
		"message": msg, "level": map[bool]string{true: "info", false: "error"}[status == "completed"], "log": string(logB)}, nil)
	if err != nil {
		lg.Printf("job %s: could not report result yet: %v", st.JobID, err)
		return
	}
	lg.Printf("job %s: reported %s", st.JobID, status)
	b2 := bytes.Replace(b, []byte(`"reported": false`), []byte(`"reported": true`), 1)
	if bytes.Equal(b2, b) {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["reported"] = true
		b2, _ = json.MarshalIndent(m, "", "  ")
	}
	_ = os.WriteFile(stPath, b2, 0o600)
	// secrets are no longer needed
	_ = os.Remove(filepath.Join(root, "swim", "job", "unattend.xml"))
	if status == "completed" {
		// the new Windows is confirmed: keep only a pre-staged image, otherwise give the space back
		if !keepPrestagedAfterInstall(root, lg) {
			releaseAfterInstall(lg)
		}
	}
}

// releaseAfterInstall deletes the staging partition and returns its space to C:.
func releaseAfterInstall(lg interface{ Printf(string, ...any) }) {
	out, err := releaseStaging(context.Background())
	if err != nil {
		lg.Printf("staging not released: %v", err)
		return
	}
	lg.Printf("%s", out)
}

// winBootID is Windows' boot counter (Memory Management\PrefetchParameters\BootId), 0 if unknown.
func winBootID() int64 {
	out, err := ps(context.Background(), 30*time.Second,
		`(Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters').BootId`)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return n
}

var procGetTickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

// reportMissedHandoff handles "the machine restarted but came back into the old Windows":
// job.json was written before the reboot, yet WinPE never ran (swim-pe writes status.json
// as soon as it starts). Without this the job would sit at "handoff" forever. Nothing on
// the disk changed, so the job is aborted with the reason and the boot configuration is
// attached for diagnosis; the staged image stays for a retry.
func reportMissedHandoff(c *Client, tok, root, stPath string, lg interface{ Printf(string, ...any) }) {
	jp := filepath.Join(root, "swim", "job", "job.json")
	fi, err := os.Stat(jp)
	if err != nil {
		return
	}
	var jf JobFile
	if b, err := os.ReadFile(jp); err != nil || json.Unmarshal(b, &jf) != nil || jf.JobID == "" {
		return
	}
	if jf.WinBootID > 0 {
		if cur := winBootID(); cur == 0 || cur == jf.WinBootID {
			return // same boot as the hand-off: the reboot has not happened yet
		}
	} else {
		ms, _, _ := procGetTickCount64.Call() // milliseconds since this boot (amd64: fits the return register)
		boot := time.Now().Add(-time.Duration(ms) * time.Millisecond)
		if !boot.After(fi.ModTime()) {
			return // no reboot since the hand-off yet (still counting down, or the reboot is pending)
		}
	}
	ctx := context.Background()
	var diag strings.Builder
	for _, args := range [][]string{{"/enum", "{fwbootmgr}"}, {"/enum", "{bootmgr}", "/v"}, {"/enum", peEntry, "/v"},
		{"/enum", "{ramdiskoptions}", "/v"}, {"/enum", "firmware"}} {
		out, err := runCmd(ctx, time.Minute, "bcdedit", args...)
		fmt.Fprintf(&diag, "==== bcdedit %s (err=%v) ====\n%s\n", strings.Join(args, " "), err, out)
	}
	sb, _ := runCmd(ctx, time.Minute, "powershell", "-NoProfile", "-Command", "try { Confirm-SecureBootUEFI } catch { $_.Exception.Message }")
	fmt.Fprintf(&diag, "==== Secure Boot ====\n%s\n", sb)
	events := "/v1/agent/jobs/" + jf.JobID + "/events"
	tries := max(jf.HandoffTries, 1)
	if tries < maxHandoffTries && !reimageActive.Load() && stagedFilesPresent(root, jf) {
		// Try the restart again by itself. The server hears about it first: if that fails
		// (network not up yet) nothing is changed and the next agent start decides again.
		var out struct {
			Cancel bool `json:"cancel"`
		}
		note := fmt.Sprintf("the restart came back into the old Windows instead of SWIM WinPE — restarting into WinPE again automatically (attempt %d of %d)", tries+1, maxHandoffTries)
		var err error
		for i := 0; i < 8; i++ { // right after a start the network may need a moment
			if err = c.PostJSON(events, tok, map[string]any{"status": "handoff", "phase": "rebooting", "progress": 100,
				"message": note, "level": "warn", "log": diag.String()}, &out); err == nil {
				break
			}
			time.Sleep(15 * time.Second)
		}
		if err != nil {
			lg.Printf("job %s: missed hand-off, could not report yet: %v", jf.JobID, err)
			return
		}
		if !out.Cancel {
			if err := configurePEBoot(ctx, strings.TrimSuffix(root, `:\`)); err != nil {
				lg.Printf("job %s: automatic retry of the hand-off could not be configured: %v", jf.JobID, err)
			} else {
				jf.HandoffTries, jf.WinBootID = tries+1, winBootID()
				if nb, err := json.MarshalIndent(jf, "", "  "); err == nil {
					_ = os.WriteFile(jp, nb, 0o600)
				}
				lg.Printf("job %s: missed hand-off — restarting into SWIM WinPE again (attempt %d of %d)", jf.JobID, tries+1, maxHandoffTries)
				if _, err := runCmd(ctx, time.Minute, "shutdown.exe", "/r", "/f", "/t", "15", "/c", "SWIM: Windows is being reinstalled. Do not turn off the computer.", "/d", "p:4:1"); err == nil {
					return
				}
				lg.Printf("job %s: restart command failed: %v", jf.JobID, err)
			}
		}
	}
	msg := "the machine restarted into the old Windows instead of SWIM WinPE (the one-time boot entry was not used) — nothing was changed; retry the install"
	if tries > 1 {
		msg = fmt.Sprintf("the machine restarted into the old Windows instead of SWIM WinPE %d times in a row (the one-time boot entry was not used) — nothing was changed; retry the install", tries)
	}
	err = c.PostJSON(events, tok, map[string]any{"status": "aborted", "phase": "handoff", "progress": 0,
		"message": msg, "level": "error", "log": diag.String()}, nil)
	if err != nil {
		lg.Printf("job %s: missed hand-off, could not report yet: %v", jf.JobID, err)
		return
	}
	lg.Printf("job %s: reported aborted — rebooted into the old Windows, not WinPE", jf.JobID)
	// leave no half-configured boot entries behind; the staged files stay for a retry
	_, _ = runCmd(ctx, time.Minute, "bcdedit", "/delete", peEntry, "/f")
	_, _ = runCmd(ctx, time.Minute, "bcdedit", "/delete", "{ramdiskoptions}", "/f")
	b, _ := json.MarshalIndent(map[string]any{"job_id": jf.JobID, "status": "aborted", "phase": "handoff", "message": msg, "reported": true}, "", "  ")
	_ = os.WriteFile(stPath, b, 0o600)
	_ = os.Remove(filepath.Join(root, "swim", "job", "unattend.xml"))
}

// stagedFilesPresent: everything WinPE needs for this job is still on the staging partition.
func stagedFilesPresent(root string, jf JobFile) bool {
	for _, f := range []string{jf.ImageFile, filepath.Join("swim", "winpe", "boot.wim"), filepath.Join("swim", "winpe", "boot.sdi"), filepath.Join("swim", "job", "unattend.xml")} {
		if f == "" {
			return false
		}
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			return false
		}
	}
	return true
}

// clearAbortedJob removes job files of an install the server no longer considers open
// (it was aborted before the machine reached WinPE), so the staging space can be released.
func clearAbortedJob() {
	if root := stagingByLabel(); root != "" {
		if _, err := os.Stat(filepath.Join(root, "swim", "job", "status.json")); err != nil {
			_ = os.RemoveAll(filepath.Join(root, "swim", "job"))
		}
	}
}

// rehandoff boots into SWIM WinPE again for a staged job whose restart came back into
// Windows. Everything WinPE needs is still on the staging partition; nothing is downloaded.
func rehandoff(ctx context.Context, ec *execCtx, jobID string) (string, error) {
	if reimageActive.Load() {
		return "", errors.New("a reimage is being prepared on this machine")
	}
	root := stagingByLabel()
	if root == "" {
		return "", errors.New("no SWIM staging partition on this machine")
	}
	jp := filepath.Join(root, "swim", "job", "job.json")
	b, err := os.ReadFile(jp)
	if err != nil {
		return "", errors.New("the staged files of this install are gone — start a new install")
	}
	var jf JobFile
	if json.Unmarshal(b, &jf) != nil || jf.JobID != jobID {
		return "", errors.New("the staged install on this machine belongs to another job")
	}
	if _, err := os.Stat(filepath.Join(root, "swim", "job", "status.json")); err == nil {
		return "", errors.New("SWIM WinPE already ran for this job")
	}
	for _, f := range []string{jf.ImageFile, filepath.Join("swim", "winpe", "boot.wim"), filepath.Join("swim", "winpe", "boot.sdi"), filepath.Join("swim", "job", "unattend.xml")} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			return "", fmt.Errorf("staged file missing (%s) — start a new install", f)
		}
	}
	j := &reimageJob{ec: ec, a: manifest.ReimageArgs{JobID: jobID}, root: root}
	j.event("", "prepare", pct(98), "retrying: configuring one-time boot into SWIM WinPE again", "info")
	if err := j.bootIntoPE(ctx, strings.TrimSuffix(root, `:\`)); err != nil {
		return "", err
	}
	jf.WinBootID = winBootID()
	if nb, err := json.MarshalIndent(jf, "", "  "); err == nil {
		_ = os.WriteFile(jp, nb, 0o600)
	}
	j.event("handoff", "rebooting", pct(100), "rebooting into SWIM WinPE in 15 s (retry) — the rest runs offline", "info")
	if _, err := runCmd(ctx, time.Minute, "shutdown.exe", "/r", "/f", "/t", "15", "/c", "SWIM: Windows is being reinstalled. Do not turn off the computer.", "/d", "p:4:1"); err != nil {
		return "", err
	}
	return "boot into SWIM WinPE configured again; restarting in 15 s", nil
}
