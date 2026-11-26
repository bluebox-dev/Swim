package builder

// Background housekeeping of the builder:
//   - inspect WIMs that were not captured by this builder version (imported files, older
//     captures): count CompactOS files left in System32/Boot, read the sysprep state and
//     the real expanded size straight from the WIM with wimlib, so the deploy check and the
//     image page have facts instead of "unknown";
//   - after a restart, clean up what an interrupted capture left behind.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// inspectOne inspects at most one image that still lacks wof_files. Returns true if it did work.
func (w *Worker) inspectOne(ctx context.Context) bool {
	var id, rel string
	skip := make([]string, 0, len(w.inspectFailed))
	for k := range w.inspectFailed {
		skip = append(skip, k)
	}
	err := w.Pool.QueryRow(ctx, `SELECT id, file_path FROM images WHERE wof_files IS NULL AND file_path IS NOT NULL
		AND status IN ('pending','published','superseded') AND NOT (id = ANY($1)) ORDER BY created_at LIMIT 1`, skip).Scan(&id, &rel)
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return false
	}
	// wof_files stays NULL (= not installable) when inspection fails; remember it so the
	// idle loop does not retry the same file until the builder restarts
	giveUp := func() {
		if w.inspectFailed == nil {
			w.inspectFailed = map[string]bool{}
		}
		w.inspectFailed[id] = true
	}
	path := filepath.Join(w.Storage, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err != nil {
		w.event(ctx, id, "inspect", "failed", "WIM file missing: "+rel)
		giveUp()
		return true
	}
	w.Log.Info("inspecting image", "id", id, "file", rel)

	wof, err := wimWOFCount(ctx, path, "/Windows/System32", "/Windows/Boot")
	if err != nil {
		w.event(ctx, id, "inspect", "failed", err.Error())
		giveUp()
		return true
	}
	state := wimImageState(ctx, path)
	expanded := wimExpandedBytes(ctx, path)

	generalized := state == "IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE"
	res := []checkResult{
		{ID: "S-01", Name: "Windows was generalized (sysprep /generalize)", Critical: true, Passed: generalized,
			Detail: "ImageState = " + state},
		{ID: "S-02", Name: "no CompactOS (WOF) files in Windows\\System32 and Windows\\Boot", Critical: true, Passed: wof == 0,
			Detail: fmt.Sprintf("%d compressed files — WinPE cannot run DISM/bcdboot from them; capture a new version", wof)},
	}
	if wof == 0 {
		res[1].Detail = ""
	}
	if _, err := w.Pool.Exec(ctx, `UPDATE images SET wof_files=$2,
		expanded_bytes=CASE WHEN $3::bigint > 0 THEN $3::bigint ELSE expanded_bytes END WHERE id=$1`, id, wof, expanded); err != nil {
		w.Log.Error("inspect: update image", "id", id, "err", err)
		giveUp()
		return true
	}
	_ = w.recordChecks(ctx, id, res, func(string, ...any) {})
	status := "ok"
	if !generalized || wof > 0 {
		status = "warning"
	}
	w.event(ctx, id, "inspect", status, fmt.Sprintf("%d CompactOS files in System32/Boot, ImageState %s, %.1f GB expanded",
		wof, state, float64(expanded)/1e9))
	return true
}

// wimWOFCount counts entries with the WOF reparse tag under the given paths of image 1.
func wimWOFCount(ctx context.Context, wim string, paths ...string) (int, error) {
	n := 0
	for _, p := range paths {
		c, cancel := context.WithTimeout(ctx, 20*time.Minute)
		out, err := exec.CommandContext(c, "wimlib-imagex", "dir", wim, "1", "--path="+p, "--detailed").Output()
		cancel()
		if err != nil {
			if strings.Contains(string(out), "not exist") {
				continue
			}
			return 0, fmt.Errorf("wimlib-imagex dir %s: %v", p, err)
		}
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if l := sc.Text(); strings.HasPrefix(l, "Reparse Tag") && strings.Contains(l, "0x80000017") {
				n++
			}
		}
	}
	return n, nil
}

// wimImageState reads Windows\Setup\State\State.ini from image 1 ("unknown" if absent).
func wimImageState(ctx context.Context, wim string) string {
	c, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(c, "wimlib-imagex", "extract", wim, "1", "/Windows/Setup/State/State.ini",
		"--to-stdout", "--no-acls").Output()
	if err != nil || len(out) == 0 {
		return "unknown"
	}
	out = bytes.ReplaceAll(bytes.TrimPrefix(out, []byte{0xFF, 0xFE}), []byte{0}, nil)
	return between(strings.ReplaceAll(string(out), "\r", ""), "ImageState=", "\n")
}

// cleanupAfterRestart removes what an interrupted capture left: overlays next to VM disks,
// throwaway reseal VMs, and images stuck in "building". Runs once at start-up, when no
// capture can be running.
func (w *Worker) cleanupAfterRestart(ctx context.Context) {
	if _, err := w.Pool.Exec(ctx, `UPDATE images SET status='failed' WHERE status='building'`); err == nil {
		_, _ = w.Pool.Exec(ctx, `INSERT INTO image_build_events(image_id, step, status, message)
			SELECT id, 'capture', 'failed', 'builder restarted during the capture' FROM images
			WHERE status='failed' AND NOT EXISTS (SELECT 1 FROM image_build_events e WHERE e.image_id=images.id
			AND e.message='builder restarted during the capture')
			AND created_at > now() - interval '1 day'`)
	}
	virsh := func(args ...string) (string, error) {
		c, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		b, err := exec.CommandContext(c, "virsh", append([]string{"-c", w.LV.URI}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}
	if names, err := virsh("list", "--all", "--name"); err == nil {
		for _, n := range strings.Fields(names) {
			if strings.HasPrefix(n, "swim-reseal-") {
				_, _ = virsh("destroy", n)
				_, _ = virsh("undefine", n, "--nvram") // never --remove-all-storage
				w.Log.Info("removed leftover reseal VM", "vm", n)
			}
		}
	}
	for _, dir := range []string{"/srv/swim/vms"} {
		matches, _ := filepath.Glob(filepath.Join(dir, "capture-img_*.qcow2"))
		for _, m := range matches {
			if err := os.Remove(m); err == nil {
				w.Log.Info("removed leftover capture overlay", "file", m)
			}
		}
	}
}
