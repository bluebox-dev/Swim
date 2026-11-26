package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
)

// "Archive and clear" for the activity log (ประวัติการใช้งาน).
//
// Nothing is deleted from audit_log: the database role of this service cannot delete
// audit rows, and that guarantee stays. Archiving writes every row up to now, exactly as
// stored (hashes included), to <audit dir>/audit-<UTC>.jsonl, then appends one
// "audit.archived" row naming the file, its SHA-256, the row range and who did it. The
// activity views show only rows from the latest audit.archived row on, so the page
// starts empty; /v1/audit/verify still checks the whole unbroken chain.
//
//	POST /v1/audit/archive               → {file, rows, sha256}
//	GET  /v1/audit/archives              → [{file, rows, sha256, size, created_at, actor}]
//	GET  /v1/audit/archives/{file}       → the .jsonl (attachment)

var archiveNameRe = regexp.MustCompile(`^audit-\d{8}-\d{6}\.jsonl$`)

// auditFloor is the id of the latest audit.archived row (0 when never archived).
// Activity views show rows with id >= auditFloor.
const auditFloorSQL = `(SELECT COALESCE(max(id),0) FROM audit_log WHERE action='audit.archived')`

func (s *Server) auditDir() string {
	if d := os.Getenv("SWIM_AUDIT_ARCHIVE_DIR"); d != "" {
		return d
	}
	return "/audit-archive"
}

func (s *Server) archiveAudit(w http.ResponseWriter, r *http.Request) {
	dir := s.auditDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "archive folder: "+err.Error())
		return
	}
	name := "audit-" + time.Now().UTC().Format("20060102-150405") + ".jsonl"
	path := filepath.Join(dir, name)
	var res map[string]any
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		// same lock as audit.Write: no row can be appended between the export and the marker
		if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(727002)`); err != nil {
			return audit.Entry{}, err
		}
		var floor int64
		if err := tx.QueryRow(r.Context(), auditFloorSQL).Scan(&floor); err != nil {
			return audit.Entry{}, err
		}
		rows, err := tx.Query(r.Context(), `SELECT id, at, actor, action, COALESCE(object_type,''), COALESCE(object_id,''),
			COALESCE(reason,''), COALESCE(ip::text,''), COALESCE(result,''), COALESCE(data::text,'null'),
			COALESCE(encode(prev_hash,'hex'),''), encode(hash,'hex') FROM audit_log WHERE id >= $1 ORDER BY id`, floor)
		if err != nil {
			return audit.Entry{}, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err != nil {
			rows.Close()
			return audit.Entry{}, err
		}
		h := sha256.New()
		bw := bufio.NewWriter(f)
		var n, firstID, lastID int64
		var firstAt, lastAt time.Time
		for rows.Next() {
			var id int64
			var at time.Time
			var actor, action, otype, oid, reason, ip, result, data, prev, hash string
			if err := rows.Scan(&id, &at, &actor, &action, &otype, &oid, &reason, &ip, &result, &data, &prev, &hash); err != nil {
				rows.Close()
				f.Close()
				os.Remove(path)
				return audit.Entry{}, err
			}
			line, _ := json.Marshal(map[string]any{"id": id, "at": at.UTC().Format(time.RFC3339Nano), "actor": actor, "action": action,
				"object_type": otype, "object_id": oid, "reason": reason, "ip": ip, "result": result,
				"data": json.RawMessage(data), "prev_hash": prev, "hash": hash})
			line = append(line, '\n')
			bw.Write(line)
			h.Write(line)
			if n == 0 {
				firstID, firstAt = id, at
			}
			lastID, lastAt = id, at
			n++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			f.Close()
			os.Remove(path)
			return audit.Entry{}, err
		}
		if err := bw.Flush(); err != nil {
			f.Close()
			os.Remove(path)
			return audit.Entry{}, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			os.Remove(path)
			return audit.Entry{}, err
		}
		f.Close()
		sum := hex.EncodeToString(h.Sum(nil))
		res = map[string]any{"file": name, "rows": n, "sha256": sum}
		return s.entry(r, "audit.archived", "audit", name, map[string]any{"file": name, "rows": n, "sha256": sum,
			"first_id": firstID, "last_id": lastID, "first_at": firstAt, "last_at": lastAt}), nil
	})
	if err != nil {
		_ = os.Remove(path) // the marker row was not written; do not leave an orphan file
		s.dbErr(w, err)
		return
	}
	s.log.Info("audit archived", "file", name, "rows", res["rows"])
	s.events.publish(Event{Type: "audit.archived"})
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) listAuditArchives(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT at, actor, COALESCE(data->>'file',''), COALESCE((data->>'rows')::bigint,0),
		COALESCE(data->>'sha256','') FROM audit_log WHERE action='audit.archived' ORDER BY id DESC`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	type item struct {
		File      string    `json:"file"`
		Rows      int64     `json:"rows"`
		SHA256    string    `json:"sha256"`
		Size      int64     `json:"size"`
		CreatedAt time.Time `json:"created_at"`
		Actor     string    `json:"actor"`
		Missing   bool      `json:"missing,omitempty"` // the file is no longer on the server
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
		var it item
		err := row.Scan(&it.CreatedAt, &it.Actor, &it.File, &it.Rows, &it.SHA256)
		if fi, e := os.Stat(filepath.Join(s.auditDir(), it.File)); e == nil && archiveNameRe.MatchString(it.File) {
			it.Size = fi.Size()
		} else {
			it.Missing = true
		}
		return it, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) downloadAuditArchive(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if !archiveNameRe.MatchString(name) {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "invalid archive name")
		return
	}
	f, err := os.Open(filepath.Join(s.auditDir(), name))
	if err != nil {
		writeErr(w, http.StatusNotFound, "SWIM-API-404", "no such archive")
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	_ = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		return s.entry(r, "audit.archive_download", "audit", name, nil), nil
	})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// ───────── retention: the activity log keeps 3 months ─────────

// auditKeepDays: how long activity rows and archive files are kept (SWIM_AUDIT_KEEP_DAYS,
// default 90, never below 30).
func auditKeepDays() int {
	if n, err := strconv.Atoi(os.Getenv("SWIM_AUDIT_KEEP_DAYS")); err == nil && n >= 30 {
		return n
	}
	return 90
}

// pruneAudit deletes activity rows and archive files older than the retention period and
// records how many went ("audit.pruned"). Runs at start-up and then every hour.
func (s *Server) pruneAudit(ctx context.Context) {
	days := auditKeepDays()
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT audit_prune(make_interval(days => $1))`, days).Scan(&n); err != nil {
		s.log.Error("audit retention", "err", err)
		return
	}
	files := []string{}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	if ents, err := os.ReadDir(s.auditDir()); err == nil {
		for _, e := range ents {
			fi, err := e.Info()
			if err != nil || e.IsDir() || !archiveNameRe.MatchString(e.Name()) || fi.ModTime().After(cutoff) {
				continue
			}
			if os.Remove(filepath.Join(s.auditDir(), e.Name())) == nil {
				files = append(files, e.Name())
			}
		}
	}
	if n == 0 && len(files) == 0 {
		return
	}
	_ = s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
		return audit.Entry{Actor: "system:retention", Action: "audit.pruned", ObjectType: "audit", ObjectID: "retention", Result: "ok",
			Data: map[string]any{"rows": n, "archive_files": files, "keep_days": days}}, nil
	})
	s.log.Info("audit retention", "rows_deleted", n, "archives_deleted", len(files), "keep_days", days)
}
