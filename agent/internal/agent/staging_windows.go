//go:build windows

package agent

// Images kept on the SWIMSTAGE partition. Policy ("leave nothing behind"): once a
// reimage is confirmed in the new Windows the whole staging partition is deleted and
// its space is given back to C: (releaseStaging), so the disk holds only the installed
// system; the next install downloads its image again. Nothing is touched after a
// failure or an abort, because a retry or a rollback still needs it — an admin can
// release that space with the release_staging command. Admins can delete a stored image on demand
// (delete_staged_image). Each verified image has a sidecar <file>.json recording the
// SHA-256 it was checked against, with the size and mtime it had then, so inventory
// can say "verified and unchanged" without re-hashing gigabytes every 15 minutes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"

	"swim/pkg/manifest"
)

// reimageActive is set while this agent prepares a reimage (staging, downloads).
var reimageActive atomic.Bool

var versionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// imageDirs: "images" is where the agent and WinPE ≥ 0.5.9 store images; WinPE 0.5.7–0.5.8
// retries used "image".
var imageDirs = []string{"images", "image"}

type imageRecord struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	MTime   int64  `json:"mtime_unix"`
}

// writeImageRecord notes that path was verified against ref (call right after the check).
func writeImageRecord(path string, ref manifest.FileRef) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	b, _ := json.Marshal(imageRecord{Version: ref.Version, SHA256: ref.SHA256, Size: fi.Size(), MTime: fi.ModTime().Unix()})
	_ = os.WriteFile(path+".json", b, 0o600)
}

// stagingByLabel finds the SWIMSTAGE volume (the job folder may already be cleaned up).
func stagingByLabel() string {
	for c := 'C'; c <= 'Z'; c++ {
		root := string(c) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		name := make([]uint16, 64)
		if windows.GetVolumeInformation(p, &name[0], uint32(len(name)), nil, nil, nil, nil, 0) == nil &&
			strings.EqualFold(windows.UTF16ToString(name), stagingLabel) {
			return root
		}
	}
	return stagingRoot()
}

// StagingInfo is the "staging" object in the inventory.
type StagingInfo struct {
	Letter    string        `json:"letter"`
	SizeBytes int64         `json:"size_bytes"`
	FreeBytes int64         `json:"free_bytes"`
	Images    []StagedImage `json:"images"`
}

type StagedImage struct {
	Version   string `json:"version"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256OK  *bool  `json:"sha256_ok"` // null: never verified by this agent/WinPE (older builds)
	InUse     bool   `json:"in_use"`    // referenced by a job that has not reported yet
}

func stagingInfo() *StagingInfo {
	root := stagingByLabel()
	if root == "" {
		return nil
	}
	si := &StagingInfo{Letter: strings.TrimSuffix(root, `\`), Images: []StagedImage{}}
	var free, total, totalFree uint64
	if p, err := windows.UTF16PtrFromString(root); err == nil && windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree) == nil {
		si.SizeBytes, si.FreeBytes = int64(total), int64(totalFree)
	}
	inUse := pendingJobImage(root)
	for _, d := range imageDirs {
		matches, _ := filepath.Glob(filepath.Join(root, "swim", d, "*.wim"))
		for _, m := range matches {
			fi, err := os.Stat(m)
			if err != nil {
				continue
			}
			img := StagedImage{Version: strings.TrimSuffix(filepath.Base(m), ".wim"), SizeBytes: fi.Size(),
				InUse: inUse != "" && strings.EqualFold(inUse, m)}
			var rec imageRecord
			if b, err := os.ReadFile(m + ".json"); err == nil && json.Unmarshal(b, &rec) == nil {
				ok := rec.Size == fi.Size() && rec.MTime == fi.ModTime().Unix() && len(rec.SHA256) == 64
				img.SHA256OK = &ok
			}
			si.Images = append(si.Images, img)
		}
	}
	sort.Slice(si.Images, func(i, j int) bool { return si.Images[i].Version > si.Images[j].Version })
	return si
}

// pendingJobImage returns the full path of the image a not-yet-reported job uses, or "".
func pendingJobImage(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "swim", "job", "job.json"))
	if err != nil {
		return ""
	}
	var jf struct {
		ImageFile string `json:"image_file"`
	}
	if json.Unmarshal(b, &jf) != nil || jf.ImageFile == "" {
		return ""
	}
	var st struct {
		Status   string `json:"status"`
		Reported bool   `json:"reported"`
	}
	if sb, err := os.ReadFile(filepath.Join(root, "swim", "job", "status.json")); err == nil && json.Unmarshal(sb, &st) == nil &&
		st.Reported && (st.Status == "first_boot" || st.Status == "completed" || st.Status == "aborted") {
		return "" // that job is over
	}
	return filepath.Join(root, jf.ImageFile)
}

// cleanupAfterInstall runs once the new Windows has reported the job as completed:
// keep only the installed image (with its verification record) and the job's
// status/log; drop other images, drivers, WinPE, the staged agent and job secrets.
func cleanupAfterInstall(root string, lg interface{ Printf(string, ...any) }) {
	var jf struct {
		ImageFile string           `json:"image_file"`
		Image     manifest.FileRef `json:"image"`
	}
	b, err := os.ReadFile(filepath.Join(root, "swim", "job", "job.json"))
	if err != nil || json.Unmarshal(b, &jf) != nil || jf.ImageFile == "" {
		lg.Printf("staging cleanup skipped: job.json unreadable")
		return
	}
	keep := filepath.Join(root, jf.ImageFile)
	if _, err := os.Stat(keep + ".json"); err != nil && len(jf.Image.SHA256) == 64 {
		// WinPE checked this file against the job's hash before installing it
		writeImageRecord(keep, jf.Image)
	}
	var freed int64
	for _, d := range imageDirs {
		matches, _ := filepath.Glob(filepath.Join(root, "swim", d, "*.wim"))
		for _, m := range matches {
			if strings.EqualFold(m, keep) {
				continue
			}
			if fi, err := os.Stat(m); err == nil {
				freed += fi.Size()
			}
			_ = os.Remove(m)
			_ = os.Remove(m + ".json")
		}
	}
	for _, p := range []string{filepath.Join("swim", "drivers"), filepath.Join("swim", "winpe"), filepath.Join("swim", "agent"),
		filepath.Join("swim", "job", "job.json"), filepath.Join("swim", "job", "retry.json"), filepath.Join("swim", "job", "unattend.xml")} {
		full := filepath.Join(root, p)
		if fi, err := os.Stat(full); err == nil {
			if fi.IsDir() {
				freed += dirSize(full)
			} else {
				freed += fi.Size()
			}
			_ = os.RemoveAll(full)
		}
	}
	lg.Printf("staging cleanup: kept %s, freed %.1f GB", filepath.Base(keep), float64(freed)/(1<<30))
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

// deleteStagedImage removes one stored image (command delete_staged_image).
func deleteStagedImage(version string) (string, error) {
	if !versionRe.MatchString(version) {
		return "", errors.New("invalid image version")
	}
	if reimageActive.Load() {
		return "", errors.New("a reimage is being prepared on this machine — try again after it finishes")
	}
	root := stagingByLabel()
	if root == "" {
		return "", errors.New("this machine has no SWIM staging partition")
	}
	inUse := pendingJobImage(root)
	var freed int64
	found := false
	for _, d := range imageDirs {
		p := filepath.Join(root, "swim", d, version+".wim")
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if inUse != "" && strings.EqualFold(inUse, p) {
			return "", fmt.Errorf("image %s belongs to a reimage job that has not finished", version)
		}
		if err := os.Remove(p); err != nil {
			return "", fmt.Errorf("delete %s: %w", version, err)
		}
		_ = os.Remove(p + ".json")
		freed += fi.Size()
		found = true
	}
	if !found {
		return "", fmt.Errorf("image %s is not stored on this machine", version)
	}
	return fmt.Sprintf("deleted image %s from %s (%.1f GB freed)", version, strings.TrimSuffix(root, `\`), float64(freed)/(1<<30)), nil
}

// releaseStaging deletes the SWIMSTAGE partition and extends C: over the freed space
// (when the two are adjacent, which is how ensureStaging creates it). Refused while a
// reimage is being prepared or a job has not reported its result yet.
func releaseStaging(ctx context.Context) (string, error) {
	if reimageActive.Load() {
		return "", errors.New("a reimage is being prepared on this machine — try again after it finishes")
	}
	root := stagingByLabel()
	if root == "" {
		return "no SWIM staging partition on this machine — nothing to release", nil
	}
	if p := pendingJobImage(root); p != "" {
		return "", errors.New("a reimage job on this machine has not finished — its staged files are still needed")
	}
	script := fmt.Sprintf(`
$st = Get-Volume | Where-Object { $_.FileSystemLabel -eq '%s' } | Select-Object -First 1
if (-not $st) { '{"freed":0,"grew":0}'; return }
$sp = Get-Partition -Volume $st
$os = Get-Partition -DriveLetter C
if ($sp.IsBoot -or $sp.IsSystem -or $sp.Guid -eq $os.Guid) { throw "refusing to delete: the staging label is on a system partition" }
$freed = $sp.Size
Remove-Partition -DiskNumber $sp.DiskNumber -PartitionNumber $sp.PartitionNumber -Confirm:$false
$grew = 0
if ($sp.DiskNumber -eq $os.DiskNumber) {
  $max = (Get-PartitionSupportedSize -DriveLetter C).SizeMax
  if ($max -gt ($os.Size + 16MB)) { Resize-Partition -DriveLetter C -Size $max; $grew = $max - $os.Size }
}
[pscustomobject]@{ freed=$freed; grew=$grew; c=(Get-Partition -DriveLetter C).Size } | ConvertTo-Json -Compress`, stagingLabel)
	out, err := ps(ctx, 10*time.Minute, script)
	if err != nil {
		return "", fmt.Errorf("release staging: %w", err)
	}
	var v struct{ Freed, Grew, C int64 }
	if i := strings.Index(out, "{"); i < 0 || json.Unmarshal([]byte(strings.TrimSpace(out[i:])), &v) != nil {
		return "", fmt.Errorf("release staging: unexpected output: %s", out)
	}
	gb := func(n int64) float64 { return float64(n) / (1 << 30) }
	if v.Freed == 0 {
		return "no SWIM staging partition on this machine — nothing to release", nil
	}
	if v.Grew == 0 {
		return fmt.Sprintf("staging partition deleted (%.1f GB); C: could not be extended over it (not adjacent) — the space is unallocated", gb(v.Freed)), nil
	}
	return fmt.Sprintf("staging partition deleted and C: extended by %.1f GB (C: is now %.1f GB)", gb(v.Grew), gb(v.C)), nil
}
