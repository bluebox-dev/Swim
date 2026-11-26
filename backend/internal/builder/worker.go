// Package builder runs server_tasks that need host privileges: VM snapshots and
// WIM capture from a VM disk (qemu-nbd + wimlib NTFS-3G mode, spec 02).
// It runs as `swim-server builder` in its own privileged container.
package builder

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"swim/backend/internal/ids"
	"swim/backend/internal/vm"
	"swim/pkg/wof"
)

type Worker struct {
	Pool    *pgxpool.Pool
	Log     *slog.Logger
	LV      vm.Libvirt
	Storage string // /storage
	ISODir  string // where installer ISOs are placed (over ssh)
	ID      string

	inspectFailed map[string]bool // images whose WIM could not be inspected (see inspect.go)
	cur           *taskProgress   // progress of the task being run (see progress.go)
}

// LogPath is where a task's log lives (the API serves its tail).
func LogPath(storage, taskID string) string {
	return filepath.Join(storage, "logs", "tasks", taskID+".log")
}

func (w *Worker) Run(ctx context.Context) error {
	// A task left "running" by a previous builder process cannot be resumed safely.
	if _, err := w.Pool.Exec(ctx, `UPDATE server_tasks SET status='failed', finished_at=now(),
		result=jsonb_build_object('error','builder restarted while task was running')
		WHERE status='running' AND kind IN ('vm_snapshot','vm_revert','vm_snapshot_delete','capture_image','iso_inspect','iso_import')`); err != nil {
		return err
	}
	w.cleanupAfterRestart(ctx)
	w.Log.Info("builder ready", "id", w.ID)
	lastInspect := time.Time{}
	for {
		ran, err := w.once(ctx)
		if err != nil {
			w.Log.Error("task loop", "err", err)
		}
		if ran {
			continue
		}
		// idle: inspect images that lack facts (imported or captured by an older builder)
		if time.Since(lastInspect) > time.Minute {
			if !w.inspectOne(ctx) {
				lastInspect = time.Now()
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(3 * time.Second):
		}
	}
}

func (w *Worker) once(ctx context.Context) (bool, error) {
	var id, kind string
	var payload []byte
	err := w.Pool.QueryRow(ctx, `UPDATE server_tasks SET status='running', locked_by=$1, locked_until=now()+interval '6 hours'
		WHERE id = (SELECT id FROM server_tasks WHERE status='queued'
		            AND kind IN ('vm_snapshot','vm_revert','vm_snapshot_delete','capture_image','iso_inspect','iso_import')
		            ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, kind, payload`, w.ID).Scan(&id, &kind, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	_ = os.MkdirAll(filepath.Dir(LogPath(w.Storage, id)), 0o755)
	lf, err := os.OpenFile(LogPath(w.Storage, id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return true, w.finish(ctx, id, nil, err)
	}
	defer lf.Close()
	logf := func(format string, a ...any) {
		fmt.Fprintf(lf, "%s  %s\n", time.Now().UTC().Format("15:04:05"), fmt.Sprintf(format, a...))
	}
	logf("task %s (%s) started on %s", id, kind, w.ID)
	w.Log.Info("task start", "id", id, "kind", kind)

	var p map[string]string
	_ = json.Unmarshal(payload, &p)
	var result map[string]any
	w.cur = &taskProgress{id: id, kind: kind, started: time.Now()}
	defer func() { w.cur = nil }()
	switch kind {
	case "vm_snapshot":
		logf("snapshot %s → %s", p["vm"], p["snapshot"])
		err = w.LV.CreateSnapshot(ctx, p["vm"], p["snapshot"])
	case "vm_revert":
		logf("revert %s → %s", p["vm"], p["snapshot"])
		err = w.LV.RevertSnapshot(ctx, p["vm"], p["snapshot"])
	case "vm_snapshot_delete":
		logf("delete snapshot %s of %s", p["snapshot"], p["vm"])
		err = w.LV.DeleteSnapshot(ctx, p["vm"], p["snapshot"])
	case "capture_image":
		result, err = w.capture(ctx, p, logf, lf)
	case "iso_inspect":
		result, err = w.isoInspect(ctx, p, logf)
	case "iso_import":
		result, err = w.isoImport(ctx, p, logf, lf)
	default:
		err = fmt.Errorf("unknown task kind %q", kind)
	}
	if err != nil {
		logf("FAILED: %v", err)
	} else {
		logf("done")
	}
	return true, w.finish(ctx, id, result, err)
}

func (w *Worker) finish(ctx context.Context, id string, result map[string]any, err error) error {
	status := "done"
	if result == nil {
		result = map[string]any{}
	}
	if err != nil {
		status = "failed"
		result["error"] = err.Error()
	}
	b, _ := json.Marshal(result)
	_, dbErr := w.Pool.Exec(ctx, `UPDATE server_tasks SET status=$2, result=$3::jsonb, finished_at=now(), locked_until=NULL
		WHERE id=$1`, id, status, string(b))
	w.Log.Info("task end", "id", id, "status", status)
	return dbErr
}

// ───────── capture ─────────

// Default NTFS capture exclusions (same set wimlib/DISM use for a Windows volume),
// plus the SWIM agent: its state, because an enrolled agent in the Golden Master must never
// become the identity of every machine that receives the image (spec 03 §2.1), and its
// program, because WinPE puts the agent published at install time on every machine. An
// image therefore never has to be captured again just because a new agent came out.
const captureConfig = `[ExclusionList]
\$ntfs.log
\hiberfil.sys
\pagefile.sys
\swapfile.sys
\System Volume Information
\RECYCLER
\$RECYCLE.BIN
\Windows\CSC
\ProgramData\SWIM
\Program Files\SWIM
`

func (w *Worker) event(ctx context.Context, imageID, step, status, msg string) {
	_, _ = w.Pool.Exec(ctx, `INSERT INTO image_build_events(image_id, step, status, message) VALUES ($1,$2,$3,$4)`,
		imageID, step, status, msg)
}

func (w *Worker) capture(ctx context.Context, p map[string]string, logf func(string, ...any), lf io.Writer) (map[string]any, error) {
	vmName, channelID, version, revertTo, actor := p["vm"], p["channel_id"], p["version"], p["revert_to"], p["actor"]
	if st, err := w.LV.State(ctx, vmName); err != nil {
		return nil, err
	} else if st != "shut off" {
		return nil, fmt.Errorf("VM %s is %q — run sysprep /generalize /oobe /shutdown first", vmName, st)
	}
	disk, err := w.LV.MainDisk(ctx, vmName)
	if err != nil {
		return nil, err
	}
	var channelName string
	if err := w.Pool.QueryRow(ctx, `SELECT name FROM channels WHERE id=$1`, channelID).Scan(&channelName); err != nil {
		return nil, fmt.Errorf("channel %s: %w", channelID, err)
	}

	imageID := ids.New("img_")
	if _, err := w.Pool.Exec(ctx, `INSERT INTO images(id, channel_id, version, status, built_by) VALUES ($1,$2,$3,'building',$4)`,
		imageID, channelID, version, actor); err != nil {
		return nil, fmt.Errorf("register image: %w", err)
	}
	logf("image %s (%s %s) status=building", imageID, channelName, version)
	rel := filepath.Join("images", channelName, version+".wim")
	out := filepath.Join(w.Storage, rel)

	fail := func(step string, err error) (map[string]any, error) {
		w.event(ctx, imageID, step, "failed", err.Error())
		_, _ = w.Pool.Exec(context.Background(), `UPDATE images SET status='failed' WHERE id=$1`, imageID)
		_ = os.Remove(out)
		return map[string]any{"image_id": imageID}, err
	}

	if _, err := os.Stat(out); err == nil {
		return fail("prepare", fmt.Errorf("%s already exists", rel))
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fail("prepare", err)
	}

	// preflight-capacity (spec 02 §7): the WIM goes to storage, the overlay grows next to
	// the Golden Master disk while CompactOS files are rewritten. Refuse before writing.
	if msg := w.capacityProblem(ctx, channelID, out, disk); msg != "" {
		return fail("preflight", errors.New(msg))
	}
	logf("preflight: enough free space")
	w.event(ctx, imageID, "preflight", "ok", "enough free space for the overlay and the WIM")
	w.progress(ctx, "preflight", 1, "enough free space")

	// Work on a throwaway qcow2 overlay: system files are rewritten (WOF → plain) before
	// capture, and the Golden Master disk itself must never be written.
	overlay := filepath.Join(filepath.Dir(disk), "capture-"+imageID+".qcow2")
	if b, err := exec.CommandContext(ctx, "qemu-img", "create", "-q", "-f", "qcow2", "-b", disk, "-F", "qcow2", overlay).CombinedOutput(); err != nil {
		return fail("nbd", fmt.Errorf("qemu-img overlay: %v: %s", err, b))
	}
	defer func() {
		_ = os.Remove(overlay)
		logf("overlay %s removed", overlay)
	}()
	// sysprep: the image must be generalized, or every client becomes a copy of the master
	if p["reseal"] != "no" {
		w.event(ctx, imageID, "sysprep", "running", "checking that Windows is generalized")
		w.progress(ctx, "sysprep", 2, "checking that Windows is generalized")
		if err := w.reseal(ctx, imageID, vmName, disk, overlay, logf); err != nil {
			return fail("sysprep", err)
		}
		w.event(ctx, imageID, "sysprep", "ok", "Windows is generalized (sysprep /generalize /oobe)")
	}

	dev, err := freeNBD()
	if err != nil {
		return fail("nbd", err)
	}
	logf("qemu-nbd %s (overlay of %s) → %s", overlay, disk, dev)
	w.event(ctx, imageID, "nbd", "running", disk)
	if b, err := exec.CommandContext(ctx, "qemu-nbd", "--format=qcow2", "--connect="+dev, overlay).CombinedOutput(); err != nil {
		return fail("nbd", fmt.Errorf("qemu-nbd: %v: %s", err, b))
	}
	disconnected := false
	disconnect := func() {
		if !disconnected {
			_ = exec.Command("qemu-nbd", "--disconnect", dev).Run()
			disconnected = true
			logf("nbd disconnected")
		}
	}
	defer disconnect()

	part, size, err := windowsPartition(dev)
	if err != nil {
		return fail("find_partition", err)
	}
	logf("Windows partition %s (%.1f GB NTFS)", part, float64(size)/1e9)

	// CompactOS (WOF) files: Windows reads them through wof.sys, WinPE cannot (it does not
	// attach WOF to the new volume), so DISM/bcdboot from the new image would not run there.
	w.event(ctx, imageID, "decompress", "running", "CompactOS files → plain files")
	logf("decompressing CompactOS (WOF) files on %s — a few minutes", part)
	w.progress(ctx, "decompress", stageStart["decompress"], "listing CompactOS files")
	wst, err := wof.Uncompress(part, "/usr/local/bin/ntfs-unwof", os.TempDir(), false, logf, func(done, total int) {
		w.progress(ctx, "decompress", stagePct("decompress", float64(done)/float64(max(total, 1))),
			fmt.Sprintf("%d of %d CompactOS files", done, total))
	})
	if err != nil {
		return fail("decompress", err)
	}
	if n := wst.SystemLeft(); n > 0 {
		return fail("decompress", fmt.Errorf("%d CompactOS files in \\Windows\\System32 or \\Windows\\Boot could not be decompressed (see log)", n))
	}
	w.event(ctx, imageID, "decompress", "ok", fmt.Sprintf("%d files, %.2f GB → %.2f GB, %d PE checksums verified; %d other files left compressed (checksum not verifiable or LZX — Windows reads them)",
		wst.Files, float64(wst.InBytes)/1e9, float64(wst.OutBytes)/1e9, wst.PEChecked, wst.Failed))

	// offline image checks (generalized, boot files, no baked-in identity or passwords …)
	w.event(ctx, imageID, "test", "running", "offline image checks S-01…S-06")
	w.progress(ctx, "test", stageStart["test"], "offline image checks S-01…S-06")
	checks, err := imageChecks(part)
	if err != nil {
		return fail("test", err)
	}
	if err := w.recordChecks(ctx, imageID, checks, logf); err != nil {
		return fail("test", err)
	}

	cfgPath := filepath.Join(os.TempDir(), "swim-capture-"+imageID+".ini")
	if err := os.WriteFile(cfgPath, []byte(captureConfig), 0o644); err != nil {
		return fail("capture", err)
	}
	defer os.Remove(cfgPath)

	w.event(ctx, imageID, "capture", "running", part)
	logf("wimlib-imagex capture %s %s (LZX) — this takes 10–30 min", part, rel)
	start := time.Now()
	cmd := exec.CommandContext(ctx, "wimlib-imagex", "capture", part, out, channelName+" "+version,
		"Captured by SWIM from "+vmName, "--compress=LZX", "--config="+cfgPath)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	w.progress(ctx, "capture", stageStart["capture"], "scanning files")
	go copyProgress(pr, lf, func(pct int) {
		w.progress(ctx, "capture", stagePct("capture", float64(pct)/100), fmt.Sprintf("writing the WIM: %d%%", pct))
	})
	err = cmd.Run()
	pw.Close()
	if err != nil {
		return fail("capture", fmt.Errorf("wimlib-imagex: %v", err))
	}
	disconnect()
	logf("capture finished in %s", time.Since(start).Round(time.Second))

	w.progress(ctx, "hash", stageStart["hash"], "SHA-256 of the WIM")
	sum, n, err := hashFile(out)
	if err != nil {
		return fail("hash", err)
	}
	expanded := wimExpandedBytes(ctx, out)
	if _, err := w.Pool.Exec(ctx, `UPDATE images SET status='pending', file_path=$2, sha256=$3, size_bytes=$4, expanded_bytes=$5,
		wof_files=0 WHERE id=$1`, imageID, filepath.ToSlash(rel), sum, n, expanded); err != nil {
		return fail("register", err)
	}
	w.event(ctx, imageID, "capture", "ok", fmt.Sprintf("%d bytes, sha256 %s", n, sum))
	w.event(ctx, imageID, "test", "ok", "offline checks passed; VM tests T-01…T-09 need a test-runner VM (not in this lab)")
	logf("image %s → pending (size %.2f GB, expanded %.2f GB, sha256 %s)", imageID, float64(n)/1e9, float64(expanded)/1e9, sum)

	res := map[string]any{"image_id": imageID, "file": rel, "sha256": sum, "size_bytes": n, "expanded_bytes": expanded}
	if revertTo != "" {
		logf("reverting %s to snapshot %s", vmName, revertTo)
		if err := w.LV.RevertSnapshot(ctx, vmName, revertTo); err != nil {
			// The image is fine; only the Golden Master stays generalized.
			res["revert_error"] = err.Error()
			logf("revert failed: %v (image is still valid)", err)
		} else {
			res["reverted_to"] = revertTo
		}
	}
	return res, nil
}

// copyProgress writes wimlib output to the log, keeping only every ~5% of the
// \r-separated progress lines.
func copyProgress(r io.Reader, lf io.Writer, onPct ...func(int)) {
	sc := bufio.NewScanner(r)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, b := range data {
			if b == '\r' || b == '\n' {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	lastPct, lastScan := -5, ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if i := strings.Index(line, "%"); i > 0 && strings.Contains(line, "Archiving") {
			j := strings.LastIndex(line[:i], "(")
			if pct, err := strconv.Atoi(strings.TrimSpace(line[j+1 : i])); err == nil {
				if pct < lastPct+5 && pct != 100 {
					continue
				}
				lastPct = pct
				for _, f := range onPct {
					f(pct)
				}
			}
		}
		// "N GiB scanned (files, directories)" comes hundreds of times a second: keep one per GiB
		if i := strings.Index(line, " GiB scanned"); i > 0 {
			if line[:i] == lastScan {
				continue
			}
			lastScan = line[:i]
		}
		fmt.Fprintf(lf, "%s  | %s\n", time.Now().UTC().Format("15:04:05"), line)
	}
}

func freeNBD() (string, error) {
	for i := 0; i < 16; i++ {
		b, err := os.ReadFile(fmt.Sprintf("/sys/block/nbd%d/size", i))
		if err != nil {
			return "", errors.New("nbd module not loaded on host (modprobe nbd max_part=16)")
		}
		if strings.TrimSpace(string(b)) == "0" {
			return fmt.Sprintf("/dev/nbd%d", i), nil
		}
	}
	return "", errors.New("no free /dev/nbd device")
}

// windowsPartition picks the largest NTFS partition of dev (the C: volume;
// recovery partitions are small).
func windowsPartition(dev string) (string, int64, error) {
	base := filepath.Base(dev)
	var parts []string
	for i := 0; i < 50; i++ { // wait for the kernel to publish partitions
		parts, _ = filepath.Glob("/sys/class/block/" + base + "p*")
		if len(parts) > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(parts) == 0 {
		return "", 0, errors.New("no partitions found on VM disk — is Windows installed?")
	}
	sort.Strings(parts)
	best, bestSize := "", int64(0)
	locked, lockedSize := "", int64(0)
	for _, p := range parts {
		name := filepath.Base(p)
		devPath := "/dev/" + name
		typ, _ := exec.Command("blkid", "-o", "value", "-s", "TYPE", devPath).Output()
		b, _ := os.ReadFile(filepath.Join(p, "size"))
		sectors, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		switch strings.TrimSpace(string(typ)) {
		case "ntfs":
			if sectors*512 > bestSize {
				best, bestSize = devPath, sectors*512
			}
		case "BitLocker":
			if sectors*512 > lockedSize {
				locked, lockedSize = devPath, sectors*512
			}
		}
	}
	// a BitLocker volume bigger than every NTFS one is the Windows partition: do not
	// silently pick the recovery partition instead
	if lockedSize > bestSize {
		return "", 0, fmt.Errorf("the Windows partition %s (%.0f GB) is BitLocker-encrypted — decrypt it on the "+
			"Golden Master (manage-bde -off C:) and set PreventDeviceEncryption before capturing", locked, float64(lockedSize)/1e9)
	}
	if best == "" {
		return "", 0, errors.New("no NTFS partition found on VM disk")
	}
	return best, bestSize, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func wimExpandedBytes(ctx context.Context, path string) int64 {
	out, err := exec.CommandContext(ctx, "wimlib-imagex", "info", path, "1").Output()
	if err != nil {
		return 0
	}
	// "Total Bytes" counts every hard link again (Windows hard-links most of System32
	// into WinSxS); the space needed on disk is the total minus "Hard Link Bytes".
	var total, links int64
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			switch strings.TrimSpace(k) {
			case "Total Bytes":
				total = n
			case "Hard Link Bytes":
				links = n
			}
		}
	}
	if links > 0 && links < total {
		return total - links
	}
	return total
}

const (
	gib = int64(1) << 30
	// rewriting CompactOS files grows the overlay by about their uncompressed size
	// (gm-win11: 9 GB of WOF streams → 17.3 GB of plain files) plus sysprep and qcow2 metadata
	overlayReserve = 20 * gib
	minFreeAfter   = 5 * gib
)

// capacityProblem estimates the space a capture needs and returns why it does not fit ("" = fits).
func (w *Worker) capacityProblem(ctx context.Context, channelID, wimPath, disk string) string {
	// WIM estimate: the last capture of this channel + 10 %, else 20 GB
	var last int64
	_ = w.Pool.QueryRow(ctx, `SELECT COALESCE(size_bytes,0) FROM images WHERE channel_id=$1 AND size_bytes IS NOT NULL
		ORDER BY created_at DESC LIMIT 1`, channelID).Scan(&last)
	wimEst := 20 * gib
	if last > 0 {
		wimEst = last + last/10
	}
	sFree, sDev, err1 := statFree(filepath.Dir(wimPath))
	vFree, vDev, err2 := statFree(filepath.Dir(disk))
	if err1 != nil || err2 != nil {
		return "" // cannot tell; the capture itself will fail cleanly if space runs out
	}
	need := func(have, want int64, where string) string {
		if have-want < minFreeAfter {
			return fmt.Sprintf("not enough free space in %s: %.1f GB free, capture needs about %.1f GB (+%.0f GB spare) — delete old images or snapshots first",
				where, float64(have)/1e9, float64(want)/1e9, float64(minFreeAfter)/1e9)
		}
		return ""
	}
	if sDev == vDev { // same filesystem: both land on it
		return need(sFree, wimEst+overlayReserve, filepath.Dir(wimPath))
	}
	if m := need(sFree, wimEst, filepath.Dir(wimPath)); m != "" {
		return m
	}
	return need(vFree, overlayReserve, filepath.Dir(disk))
}

// statFree returns the bytes available to unprivileged writers and a filesystem id.
func statFree(dir string) (int64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	var fi syscall.Stat_t
	if err := syscall.Stat(dir, &fi); err != nil {
		return 0, 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), uint64(fi.Dev), nil
}
