package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
)

// Where the server's disk space goes, and removing what nothing needs any more.
//
//	GET  /v1/capacity/server/detail    breakdown by category, each with its items
//	POST /v1/capacity/server/cleanup   delete everything marked "removable"
//
// Removable: agent builds older than the published and the previous one, WinPE builds
// other than the published one, per-job WinPE and rescue media of installs that
// completed, were cancelled, or are no longer in the history, and task logs older than
// 30 days. Images are deleted on the Images page; the golden master and ISOs by hand.

type storageItem struct {
	Name      string    `json:"name"`
	Bytes     int64     `json:"bytes"`
	Modified  time.Time `json:"modified"`
	Note      string    `json:"note,omitempty"`
	Removable bool      `json:"removable"`
	path      string
}

type storageCategory struct {
	Key            string        `json:"key"`
	Bytes          int64         `json:"bytes"`
	RemovableBytes int64         `json:"removable_bytes"`
	Items          []storageItem `json:"items"`
}

// treeSize is the space a file or folder really occupies (sparse qcow2 files count
// their allocated blocks).
func treeSize(p string) int64 {
	var n int64
	_ = filepath.Walk(p, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += diskAllocated(fi)
		}
		return nil
	})
	return n
}

func children(dir string) []storageItem {
	es, _ := os.ReadDir(dir)
	out := []storageItem{}
	for _, e := range es {
		p := filepath.Join(dir, e.Name())
		it := storageItem{Name: e.Name(), Bytes: treeSize(p), path: p}
		if fi, err := e.Info(); err == nil {
			it.Modified = fi.ModTime()
		}
		out = append(out, it)
	}
	return out
}

func (s *Server) storageBreakdown(r *http.Request) ([]storageCategory, error) {
	// install jobs still worth keeping files for: unfinished, or failed/aborted (retry, rescue)
	keepJob := map[string]bool{}
	rows, err := s.pool.Query(r.Context(), `SELECT id FROM deployment_jobs WHERE finished_at IS NULL OR status IN ('failed','aborted')`)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		keepJob[id] = true
	}
	jobOf := func(name string) string {
		if i := strings.Index(name, "job_"); i >= 0 {
			id := name[i:]
			if j := strings.IndexAny(id, "./"); j > 0 {
				id = id[:j]
			}
			return id
		}
		return ""
	}
	var latestAgent struct{ File string }
	_ = s.readJSONFile("agent/latest.json", &latestAgent)
	var latestPE struct{ Version string }
	_ = s.readJSONFile("winpe/latest.json", &latestPE)

	st := s.cfg.StorageDir
	var cats []storageCategory

	// images: name them by version and status from the database
	imgs := storageCategory{Key: "images", Items: []storageItem{}}
	irows, err := s.pool.Query(r.Context(), `SELECT version, status, COALESCE(file_path,'') FROM images WHERE file_path IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	known := map[string]string{}
	var v, status, fp string
	if _, err := pgx.ForEachRow(irows, []any{&v, &status, &fp}, func() error {
		known[filepath.Join(st, filepath.FromSlash(fp))] = v + " · " + status
		return nil
	}); err != nil {
		return nil, err
	}
	_ = filepath.Walk(filepath.Join(st, "images"), func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		it := storageItem{Name: filepath.Base(p), Bytes: diskAllocated(fi), Modified: fi.ModTime(), Note: known[p], path: p}
		if it.Note == "" {
			it.Note = "not in the image list"
		}
		imgs.Items = append(imgs.Items, it)
		return nil
	})
	cats = append(cats, imgs)

	vmDir := filepath.Dir(s.cfg.ISODir)
	vms := storageCategory{Key: "vms", Items: []storageItem{}}
	for _, it := range children(vmDir) {
		if it.path == s.cfg.ISODir {
			continue
		}
		vms.Items = append(vms.Items, it)
	}
	cats = append(cats, vms, storageCategory{Key: "iso", Items: children(s.cfg.ISODir)})

	// agent builds: keep the published one and the newest other one
	agent := storageCategory{Key: "agent", Items: children(filepath.Join(st, "agent"))}
	sort.Slice(agent.Items, func(i, j int) bool { return agent.Items[i].Modified.After(agent.Items[j].Modified) })
	cur := strings.TrimSuffix(latestAgent.File, "-windows-amd64.exe") // swim-agent-<ver>
	prev := ""
	for i := range agent.Items {
		it := &agent.Items[i]
		if !strings.HasPrefix(it.Name, "swim-agent-") {
			continue // install.ps1, latest.json, certificate
		}
		build := strings.TrimSuffix(strings.TrimSuffix(it.Name, "-windows-amd64.exe"), "-linux-amd64")
		switch {
		case cur != "" && build == cur:
			it.Note = "published"
		case prev == "" || build == prev:
			prev, it.Note = build, "previous build"
		default:
			it.Removable = cur != ""
		}
	}
	cats = append(cats, agent)

	pe := storageCategory{Key: "winpe", Items: []storageItem{}}
	for _, it := range children(filepath.Join(st, "winpe")) {
		switch {
		case it.Name == "jobs":
			for _, j := range children(it.path) {
				j.Name = "jobs/" + j.Name
				if j.Removable = !keepJob[jobOf(j.Name)]; !j.Removable {
					j.Note = "install not finished or may be retried"
				}
				pe.Items = append(pe.Items, j)
			}
		case strings.HasPrefix(it.Name, "pe-"):
			if it.Name == latestPE.Version {
				it.Note = "published"
			} else {
				it.Removable = latestPE.Version != ""
			}
			pe.Items = append(pe.Items, it)
		default:
			pe.Items = append(pe.Items, it)
		}
	}
	cats = append(cats, pe)

	rescue := storageCategory{Key: "rescue", Items: children(filepath.Join(st, "rescue"))}
	for i := range rescue.Items {
		it := &rescue.Items[i]
		if id := jobOf(it.Name); id != "" {
			if it.Removable = !keepJob[id]; !it.Removable {
				it.Note = "rescue media of a failed install"
			}
		}
	}
	cats = append(cats, rescue, storageCategory{Key: "drivers", Items: children(filepath.Join(st, "drivers"))})

	logs := storageCategory{Key: "logs", Items: []storageItem{}}
	_ = filepath.Walk(filepath.Join(st, "logs"), func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(filepath.Join(st, "logs"), p)
		logs.Items = append(logs.Items, storageItem{Name: filepath.ToSlash(rel), Bytes: diskAllocated(fi), Modified: fi.ModTime(),
			Removable: time.Since(fi.ModTime()) > 30*24*time.Hour, path: p})
		return nil
	})
	cats = append(cats, logs)

	for i := range cats {
		c := &cats[i]
		sort.SliceStable(c.Items, func(a, b int) bool { return c.Items[a].Bytes > c.Items[b].Bytes })
		for _, it := range c.Items {
			c.Bytes += it.Bytes
			if it.Removable {
				c.RemovableBytes += it.Bytes
			}
		}
	}
	return cats, nil
}

func (s *Server) capacityDetail(w http.ResponseWriter, r *http.Request) {
	d, err := statDisk(s.cfg.StorageDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", err.Error())
		return
	}
	cats, err := s.storageBreakdown(r)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	var known, removable int64
	for _, c := range cats {
		known += c.Bytes
		removable += c.RemovableBytes
	}
	// the rest of the disk: operating system, Docker images and build cache, database
	other := int64(d.TotalBytes-d.FreeBytes) - known
	if other < 0 {
		other = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{"storage": d, "categories": cats, "other_bytes": other, "removable_bytes": removable})
}

func (s *Server) capacityCleanup(w http.ResponseWriter, r *http.Request) {
	cats, err := s.storageBreakdown(r)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	var freed int64
	removed, failed := []string{}, []string{}
	for _, c := range cats {
		for _, it := range c.Items {
			// only inside the storage folder, never the folder itself
			if !it.Removable || !strings.HasPrefix(it.path, filepath.Clean(s.cfg.StorageDir)+string(filepath.Separator)) {
				continue
			}
			if err := os.RemoveAll(it.path); err != nil {
				failed = append(failed, c.Key+"/"+it.Name)
				s.log.Error("storage cleanup", "path", it.path, "err", err)
				continue
			}
			freed += it.Bytes
			removed = append(removed, c.Key+"/"+it.Name)
		}
	}
	data, _ := json.Marshal(removed)
	_ = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		return s.entry(r, "storage.cleanup", "server", "storage", map[string]any{"removed": json.RawMessage(data), "failed": failed, "freed_bytes": freed}), nil
	})
	s.log.Info("storage cleanup", "freed_bytes", freed, "removed", len(removed), "failed", len(failed))
	writeJSON(w, http.StatusOK, map[string]any{"freed_bytes": freed, "removed": removed, "failed": failed})
}
