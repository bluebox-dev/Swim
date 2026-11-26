package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
)

// getImage returns one image with what an approver needs to decide: its build stages
// (image_build_events, in order) and the offline image checks S-01…S-06 (image_tests).
//
//	GET /v1/images/{id}
func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var img struct {
		ID            string     `json:"id"`
		ChannelID     string     `json:"channel_id"`
		Version       string     `json:"version"`
		Status        string     `json:"status"`
		FilePath      *string    `json:"file_path"`
		SHA256        *string    `json:"sha256"`
		SizeBytes     *int64     `json:"size_bytes"`
		ExpandedBytes *int64     `json:"expanded_bytes"`
		MinSystemGB   int        `json:"min_system_gb,omitempty"` // smallest C: a deploy accepts
		WOFFiles      *int64     `json:"wof_files"`               // CompactOS files left in System32/Boot; must be 0 to install
		Installable   bool       `json:"installable"`             // WinPE can finish this image (wof_files = 0)
		BuiltBy       *string    `json:"built_by"`
		ApprovedBy    *string    `json:"approved_by"`
		CreatedAt     time.Time  `json:"created_at"`
		PublishedAt   *time.Time `json:"published_at"`
	}
	if err := s.pool.QueryRow(r.Context(), `SELECT id, channel_id, version, status, file_path, sha256, size_bytes, expanded_bytes,
		wof_files, built_by, approved_by, created_at, published_at FROM images WHERE id=$1`, id).Scan(&img.ID, &img.ChannelID,
		&img.Version, &img.Status, &img.FilePath, &img.SHA256, &img.SizeBytes, &img.ExpandedBytes, &img.WOFFiles,
		&img.BuiltBy, &img.ApprovedBy, &img.CreatedAt, &img.PublishedAt); err != nil {
		s.dbErr(w, err)
		return
	}
	img.Installable = img.WOFFiles != nil && *img.WOFFiles == 0
	if img.ExpandedBytes != nil && *img.ExpandedBytes > 0 {
		img.MinSystemGB = minSystemGB(*img.ExpandedBytes)
	}

	type event struct {
		Step    string    `json:"step"`
		Status  string    `json:"status"` // running | ok | warning | failed | skipped | rejected
		Message string    `json:"message"`
		At      time.Time `json:"at"`
	}
	rows, err := s.pool.Query(r.Context(), `SELECT COALESCE(step,''), COALESCE(status,''), COALESCE(message,''), at
		FROM image_build_events WHERE image_id=$1 ORDER BY at, id`, id)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (event, error) {
		var e event
		return e, row.Scan(&e.Step, &e.Status, &e.Message, &e.At)
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}

	type check struct {
		ID     string    `json:"id"`
		Passed bool      `json:"passed"`
		At     time.Time `json:"at"`
	}
	rows, err = s.pool.Query(r.Context(), `SELECT test_id, COALESCE(passed,false), COALESCE(at, now()) FROM image_tests
		WHERE image_id=$1 ORDER BY test_id, at DESC`, id)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	checks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (check, error) {
		var c check
		return c, row.Scan(&c.ID, &c.Passed, &c.At)
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"image": img, "events": events, "checks": checks})
}

// deleteImage removes an image version and its WIM file.
//
//	DELETE /v1/images/{id}   body (optional): {"reason": "..."}
//
// Refused while a capture of it is running or while an unfinished install job (or a
// retry plan) uses it. Images that old jobs or rollouts still reference keep their row
// as history (status "deleted", no file); others are removed entirely.
func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	var rel string
	var hard bool
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var status, version, channelID string
		var file *string
		if err := tx.QueryRow(r.Context(), `SELECT status, version, channel_id, file_path FROM images WHERE id=$1 FOR UPDATE`, id).
			Scan(&status, &version, &channelID, &file); err != nil {
			return audit.Entry{}, err
		}
		if status == "building" {
			return audit.Entry{}, errStatus{"this version is still being captured — wait until the capture finishes or fails"}
		}
		var busy string
		_ = tx.QueryRow(r.Context(), `SELECT id FROM deployment_jobs WHERE (image_id=$1 OR fallback_image_id=$1 OR retry::text LIKE '%'||$1||'%')
			AND status NOT IN ('completed','failed','aborted','cancelled') LIMIT 1`, id).Scan(&busy)
		if busy != "" {
			return audit.Entry{}, errStatus{"install job " + busy + " is still using this version — wait until it finishes or cancel it"}
		}
		var refs int
		if err := tx.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM deployment_jobs WHERE image_id=$1 OR fallback_image_id=$1)
			+ (SELECT count(*) FROM rollouts WHERE image_id=$1) + (SELECT count(*) FROM images WHERE delta_base_id=$1)`, id).Scan(&refs); err != nil {
			return audit.Entry{}, err
		}
		if file != nil {
			rel = *file
		}
		hard = refs == 0
		if hard {
			for _, q := range []string{`DELETE FROM image_tests WHERE image_id=$1`, `DELETE FROM image_build_events WHERE image_id=$1`,
				`DELETE FROM images WHERE id=$1`} {
				if _, err := tx.Exec(r.Context(), q, id); err != nil {
					return audit.Entry{}, err
				}
			}
		} else {
			if _, err := tx.Exec(r.Context(), `UPDATE images SET status='deleted', file_path=NULL WHERE id=$1`, id); err != nil {
				return audit.Entry{}, err
			}
			msg := "deleted by an admin"
			if in.Reason != "" {
				msg += ": " + in.Reason
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO image_build_events(image_id, step, status, message) VALUES ($1,'retention','deleted',$2)`,
				id, msg); err != nil {
				return audit.Entry{}, err
			}
		}
		return s.entry(r, "image.delete", "image", id, map[string]any{"version": version, "channel_id": channelID,
			"was": status, "reason": in.Reason, "row_kept_as_history": !hard}), nil
	})
	var es errStatus
	if errors.As(err, &es) {
		writeErr(w, http.StatusConflict, "SWIM-API-409", es.msg)
		return
	}
	if err != nil {
		s.dbErr(w, err)
		return
	}
	fileRemoved := false
	if rel != "" {
		// only files inside <storage>/images may be removed
		root := filepath.Join(s.cfg.StorageDir, "images") + string(filepath.Separator)
		p := filepath.Join(s.cfg.StorageDir, filepath.FromSlash(rel))
		if strings.HasPrefix(p, root) {
			if err := os.Remove(p); err == nil || errors.Is(err, os.ErrNotExist) {
				fileRemoved = true
			} else {
				s.log.Error("image delete: remove file", "file", p, "err", err)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "row_kept_as_history": !hard, "file_removed": fileRemoved})
}
