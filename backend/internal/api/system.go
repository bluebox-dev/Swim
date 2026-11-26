package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"swim/backend/internal/audit"
)

type diskUsage struct {
	Path       string  `json:"path"`
	TotalBytes uint64  `json:"total_bytes"`
	FreeBytes  uint64  `json:"free_bytes"`
	UsedPct    float64 `json:"used_pct"`
	Level      string  `json:"level"` // ok | warn (≥80%) | block (≥90%) — spec 02 capacity
}

func statDisk(path string) (diskUsage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return diskUsage{}, err
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	d := diskUsage{Path: path, TotalBytes: total, FreeBytes: free}
	if total > 0 {
		d.UsedPct = float64(total-free) / float64(total) * 100
	}
	switch {
	case d.UsedPct >= 90:
		d.Level = "block"
	case d.UsedPct >= 80:
		d.Level = "warn"
	default:
		d.Level = "ok"
	}
	return d, nil
}

func (s *Server) virsh(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "virsh", append([]string{"-c", s.cfg.Libvirt}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	// Without a sign-in the answer is only up/down: versions, disk figures and the
	// hypervisor build are useful to an attacker and nothing public needs them.
	if s.adminByToken(bearer(r)) == nil && s.adminBySession(r) == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.pool.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "down"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	checks := map[string]any{}
	status := "ok"

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		checks["database"], status = "down: "+err.Error(), "down"
	} else {
		var v string
		_ = s.pool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v)
		checks["database"] = "ok (schema " + v + ")"
	}
	if d, err := statDisk(s.cfg.StorageDir); err != nil {
		checks["storage"], status = "error: "+err.Error(), "degraded"
	} else {
		checks["storage"] = d
		if d.Level != "ok" && status == "ok" {
			status = "degraded"
		}
	}
	if out, err := s.virsh(r.Context(), "version", "--daemon"); err != nil {
		checks["libvirt"] = "unreachable: " + out
		if status == "ok" {
			status = "degraded"
		}
	} else {
		checks["libvirt"] = strings.ReplaceAll(out, "\n", "; ")
	}
	code := http.StatusOK
	if status == "down" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"status": status, "version": s.cfg.Version,
		"uptime_s": int(time.Since(s.started).Seconds()), "checks": checks})
}

// metrics exposes a few Prometheus gauges; the full set comes with phase 4 monitoring.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var hosts, activeKS, published int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM hosts`).Scan(&hosts)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM kill_switches WHERE active`).Scan(&activeKS)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM images WHERE status='published'`).Scan(&published)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "swim_hosts_total %d\nswim_kill_switches_active %d\nswim_images_published %d\n", hosts, activeKS, published)
	if d, err := statDisk(s.cfg.StorageDir); err == nil {
		fmt.Fprintf(w, "swim_storage_used_ratio %.4f\n", d.UsedPct/100)
	}
}

func (s *Server) keyset(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kid": s.key.Kid, "alg": "Ed25519", "pub": base64.RawURLEncoding.EncodeToString(s.key.Pub),
	}}})
}

func (s *Server) whoami(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r)
	out := map[string]any{"name": a.Name, "username": a.Name, "user_id": a.UserID, "two_person": s.cfg.TwoPerson, "auth": a.Auth}
	if a.Auth == "session" {
		var dn string
		var rec int
		_ = s.pool.QueryRow(r.Context(), `SELECT display_name, jsonb_array_length(recovery_hashes) FROM admin_users WHERE id=$1`, a.AccountID).Scan(&dn, &rec)
		out["display_name"], out["recovery_left"] = dn, rec
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) capacity(w http.ResponseWriter, r *http.Request) {
	d, err := statDisk(s.cfg.StorageDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", err.Error())
		return
	}
	var imgBytes int64
	_ = s.pool.QueryRow(r.Context(), `SELECT COALESCE(sum(size_bytes),0) FROM images
		WHERE status NOT IN ('deleted')`).Scan(&imgBytes)
	writeJSON(w, http.StatusOK, map[string]any{"storage": d, "image_bytes": imgBytes})
}

func diskAllocated(st os.FileInfo) int64 {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return sys.Blocks * 512
	}
	return st.Size()
}

func itoa(n int) string { return strconv.Itoa(n) }

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT id, at, actor, action, COALESCE(object_type,''), COALESCE(object_id,''),
		COALESCE(reason,''), COALESCE(result,''), encode(hash,'hex') FROM audit_log WHERE id >= `+auditFloorSQL+` ORDER BY id DESC LIMIT 200`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	defer rows.Close()
	type item struct {
		ID         int64     `json:"id"`
		At         time.Time `json:"at"`
		Actor      string    `json:"actor"`
		Action     string    `json:"action"`
		ObjectType string    `json:"object_type"`
		ObjectID   string    `json:"object_id"`
		Reason     string    `json:"reason,omitempty"`
		Result     string    `json:"result"`
		Hash       string    `json:"hash"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.At, &it.Actor, &it.Action, &it.ObjectType, &it.ObjectID, &it.Reason, &it.Result, &it.Hash); err != nil {
			s.dbErr(w, err)
			return
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	res, err := audit.Verify(r.Context(), s.pool)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	// rows = the whole chain (archived rows stay in the database and are verified too);
	// visible_rows = what the activity page shows since the last archive
	var visible int64
	_ = s.pool.QueryRow(r.Context(), `SELECT count(*) FROM audit_log WHERE id >= `+auditFloorSQL).Scan(&visible)
	writeJSON(w, http.StatusOK, map[string]any{"rows": res.Rows, "ok": res.OK, "broken_at": res.BrokenAt, "problem": res.Problem,
		"visible_rows": visible, "pruned": res.Pruned, "keep_days": auditKeepDays()})
}
