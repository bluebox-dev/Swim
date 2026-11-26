//go:build windows

package agent

// Pre-staging ("เก็บ image ไว้ในเครื่องล่วงหน้า"): the admin chooses, per machine, which
// install image the machine keeps on its SWIMSTAGE partition. The agent downloads it in the
// background (SHA-256 verified) and keeps it after installs, so a reimage — e.g. a scheduled
// one — finds the file already there and nothing large crosses the network at that time.
//
// The choice is recorded on the staging partition itself (swim\prestage.json), because a
// reimage wipes ProgramData but not that partition.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"swim/pkg/manifest"
)

type prestageStatus struct {
	Version   string    `json:"version,omitempty"`
	ImageID   string    `json:"image_id,omitempty"`
	State     string    `json:"state"` // none | downloading | ready | failed
	Progress  int       `json:"progress,omitempty"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	prestageMu  sync.Mutex
	prestageCur = prestageStatus{State: "none"}
	prestaging  bool
)

type prestageMarker struct {
	ImageID string           `json:"image_id"`
	Image   manifest.FileRef `json:"image"`
}

func prestageMarkerPath(root string) string { return filepath.Join(root, "swim", "prestage.json") }

func readPrestageMarker(root string) (prestageMarker, bool) {
	var m prestageMarker
	b, err := os.ReadFile(prestageMarkerPath(root))
	return m, err == nil && json.Unmarshal(b, &m) == nil && m.Image.Version != ""
}

func setPrestage(s prestageStatus) {
	prestageMu.Lock()
	s.UpdatedAt = time.Now().UTC()
	prestageCur = s
	prestageMu.Unlock()
}

// prestageInfo is reported in inventory (the measured state).
func prestageInfo() prestageStatus {
	prestageMu.Lock()
	cur := prestageCur
	prestageMu.Unlock()
	if cur.State == "downloading" || cur.State == "failed" {
		return cur
	}
	root := stagingByLabel()
	if root == "" {
		return prestageStatus{State: "none", UpdatedAt: time.Now().UTC()}
	}
	m, ok := readPrestageMarker(root)
	if !ok {
		return prestageStatus{State: "none", UpdatedAt: time.Now().UTC()}
	}
	st := prestageStatus{Version: m.Image.Version, ImageID: m.ImageID, State: "downloading", UpdatedAt: time.Now().UTC()}
	p := filepath.Join(root, "swim", "images", m.Image.Version+".wim")
	if fi, err := os.Stat(p); err == nil {
		var rec imageRecord
		if b, err := os.ReadFile(p + ".json"); err == nil && json.Unmarshal(b, &rec) == nil &&
			rec.Size == fi.Size() && rec.MTime == fi.ModTime().Unix() && strings.EqualFold(rec.SHA256, m.Image.SHA256) {
			st.State = "ready"
		}
	}
	if st.State != "ready" && !prestaging {
		st.State = "failed"
		st.Error = "not downloaded yet"
	}
	return st
}

// prestageImage starts (or clears, with an empty image) the stored image in the background.
func prestageImage(ec *execCtx, a manifest.PrestageArgs) (string, error) {
	if reimageActive.Load() {
		return "", errors.New("a reimage is in progress on this machine")
	}
	if a.Image.Version == "" {
		root := stagingByLabel()
		if root != "" {
			_ = os.Remove(prestageMarkerPath(root))
		}
		setPrestage(prestageStatus{State: "none"})
		out, err := releaseStaging(context.Background())
		return "no image kept on this machine any more; " + out, err
	}
	if !versionRe.MatchString(a.Image.Version) || len(a.Image.SHA256) != 64 {
		return "", errors.New("invalid image reference")
	}
	prestageMu.Lock()
	if prestaging {
		prestageMu.Unlock()
		return "", errors.New("a download is already running on this machine")
	}
	prestaging = true
	prestageMu.Unlock()
	setPrestage(prestageStatus{Version: a.Image.Version, ImageID: a.ImageID, State: "downloading"})
	go func() {
		defer func() { prestageMu.Lock(); prestaging = false; prestageMu.Unlock(); requestInventory() }()
		if err := runPrestage(ec, a); err != nil {
			ec.lg.Printf("prestage %s: %v", a.Image.Version, err)
			setPrestage(prestageStatus{Version: a.Image.Version, ImageID: a.ImageID, State: "failed", Error: err.Error()})
			return
		}
		ec.lg.Printf("prestage %s: ready", a.Image.Version)
		setPrestage(prestageStatus{Version: a.Image.Version, ImageID: a.ImageID, State: "ready", Progress: 100})
	}()
	return "downloading image " + a.Image.Version + " to this machine in the background", nil
}

func runPrestage(ec *execCtx, a manifest.PrestageArgs) error {
	ctx := context.Background()
	j := &reimageJob{ec: ec, quiet: true, a: manifest.ReimageArgs{Image: a.Image}}
	// The console reads the progress from the inventory, which is otherwise sent only every few
	// minutes: the bar stood still and the download looked stuck. Send it as the download moves.
	lastPct, lastAt := -10, time.Time{}
	j.onProgress = func(p int) {
		setPrestage(prestageStatus{Version: a.Image.Version, ImageID: a.ImageID, State: "downloading", Progress: p})
		if p-lastPct >= 3 || time.Since(lastAt) > 45*time.Second {
			lastPct, lastAt = p, time.Now()
			requestInventory()
		}
	}
	vol, err := j.ensureStaging(ctx, a.Image.Size+3<<30)
	if err != nil {
		return err
	}
	j.root = vol.Letter + `:\`
	b, _ := json.Marshal(prestageMarker{ImageID: a.ImageID, Image: a.Image})
	if err := os.MkdirAll(filepath.Join(j.root, "swim"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(prestageMarkerPath(j.root), b, 0o600); err != nil {
		return err
	}
	imgRel := filepath.Join("swim", "images", a.Image.Version+".wim")
	// only the chosen image is kept
	for _, d := range imageDirs {
		matches, _ := filepath.Glob(filepath.Join(j.root, "swim", d, "*.wim"))
		for _, m := range matches {
			if !strings.EqualFold(m, filepath.Join(j.root, imgRel)) && pendingJobImage(j.root) != m {
				_ = os.Remove(m)
				_ = os.Remove(m + ".json")
			}
		}
	}
	if err := j.fetch(a.Image, imgRel, 0, 100, "image "+a.Image.Version); err != nil {
		return err
	}
	writeImageRecord(filepath.Join(j.root, imgRel), a.Image)
	return nil
}

// keepPrestagedAfterInstall: called instead of releasing the staging partition when the
// machine keeps a pre-staged image. Everything but that image (and the job's status/log)
// goes; if the chosen image is not the one just installed, the server sends it again.
func keepPrestagedAfterInstall(root string, lg interface{ Printf(string, ...any) }) bool {
	m, ok := readPrestageMarker(root)
	if !ok {
		return false
	}
	keep := filepath.Join(root, "swim", "images", m.Image.Version+".wim")
	var freed int64
	for _, d := range imageDirs {
		matches, _ := filepath.Glob(filepath.Join(root, "swim", d, "*.wim"))
		for _, f := range matches {
			if strings.EqualFold(f, keep) {
				continue
			}
			if fi, err := os.Stat(f); err == nil {
				freed += fi.Size()
			}
			_ = os.Remove(f)
			_ = os.Remove(f + ".json")
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
	lg.Printf("staging kept for pre-staged image %s, freed %.1f GB of other files", m.Image.Version, float64(freed)/(1<<30))
	return true
}
