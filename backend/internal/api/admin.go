package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/backend/internal/ids"
)

// ───────── sites ─────────

func (s *Server) listSites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT s.id, s.name, COALESCE(s.description,''), s.max_concurrent_deploy, s.max_concurrent_download,
		(SELECT count(*) FROM hosts h WHERE h.site_id=s.id AND h.retired_at IS NULL AND h.review_at IS NULL)
		FROM sites s ORDER BY s.name`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var id, name, desc string
		var dep, dl, hosts int
		err := row.Scan(&id, &name, &desc, &dep, &dl, &hosts)
		return map[string]any{"id": id, "name": name, "description": desc, "max_concurrent_deploy": dep,
			"max_concurrent_download": dl, "hosts": hosts}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "name required")
		return
	}
	id := ids.New("site_")
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO sites(id, name) VALUES ($1,$2)`, id, in.Name)
		return s.entry(r, "site.create", "site", id, in), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// ───────── channels ─────────

type channel struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	OS        string  `json:"os"`
	Release   string  `json:"release"`
	Arch      string  `json:"arch"`
	Edition   string  `json:"edition"`
	MinDiskGB int     `json:"min_disk_gb"`
	EOLDate   *string `json:"eol_date,omitempty"`
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT id, name, os, release, arch, edition, min_disk_gb, eol_date::text FROM channels ORDER BY name`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (channel, error) {
		var c channel
		err := row.Scan(&c.ID, &c.Name, &c.OS, &c.Release, &c.Arch, &c.Edition, &c.MinDiskGB, &c.EOLDate)
		return c, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	var in channel
	if !decode(w, r, &in) {
		return
	}
	if in.Name == "" || in.OS == "" || in.Release == "" || in.Arch == "" || in.Edition == "" {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "name, os, release, arch, edition required")
		return
	}
	if in.MinDiskGB == 0 {
		in.MinDiskGB = 128
	}
	in.ID = ids.New("ch_")
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO channels(id, name, os, release, arch, edition, min_disk_gb, eol_date)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::date)`, in.ID, in.Name, in.OS, in.Release, in.Arch, in.Edition, in.MinDiskGB, in.EOLDate)
		return s.entry(r, "channel.create", "channel", in.ID, in), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, in)
}

// ───────── images ─────────

type image struct {
	ID          string     `json:"id"`
	ChannelID   string     `json:"channel_id"`
	Version     string     `json:"version"`
	Status      string     `json:"status"`
	FilePath    *string    `json:"file_path"`
	SHA256      *string    `json:"sha256"`
	SizeBytes   *int64     `json:"size_bytes"`
	BuiltBy     *string    `json:"built_by"`
	ApprovedBy  *string    `json:"approved_by"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at"`
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT id, channel_id, version, status, file_path, sha256, size_bytes,
		built_by, approved_by, created_at, published_at FROM images
		WHERE ($1 = '' OR channel_id = $1) ORDER BY created_at DESC`, r.URL.Query().Get("channel"))
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (image, error) {
		var i image
		err := row.Scan(&i.ID, &i.ChannelID, &i.Version, &i.Status, &i.FilePath, &i.SHA256, &i.SizeBytes,
			&i.BuiltBy, &i.ApprovedBy, &i.CreatedAt, &i.PublishedAt)
		return i, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// importImage registers a WIM already copied into <storage>/images/ (phase 3:
// "upload WIM เองได้"). The image lands in `pending`; a different admin approves.
func (s *Server) importImage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ChannelID string `json:"channel_id"`
		Version   string `json:"version"`
		File      string `json:"file"` // relative to <storage>/images
	}
	if !decode(w, r, &in) {
		return
	}
	rel := path.Clean("/" + in.File)[1:]
	if in.ChannelID == "" || in.Version == "" || rel == "" || !strings.HasSuffix(strings.ToLower(rel), ".wim") {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "channel_id, version and a .wim file under images/ required")
		return
	}
	full := filepath.Join(s.cfg.StorageDir, "images", filepath.FromSlash(rel))
	f, err := os.Open(full)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "SWIM-API-422", "file not found in storage/images")
		return
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "read: "+err.Error())
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	id := ids.New("img_")
	stored := "images/" + rel
	err = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO images(id, channel_id, version, status, file_path, sha256, size_bytes, built_by)
			VALUES ($1,$2,$3,'pending',$4,$5,$6,$7)`, id, in.ChannelID, in.Version, stored, sum, n, adminFrom(r).UserID)
		return s.entry(r, "image.import", "image", id, map[string]any{"version": in.Version, "sha256": sum, "size": n}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "pending", "sha256": sum, "size_bytes": n})
}

// approveImage publishes a pending image and supersedes the current one in the
// same transaction. With Config.TwoPerson the builder cannot approve their own image.
func (s *Server) approveImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	me := adminFrom(r)
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var channelID, status string
		var builtBy *string
		if err := tx.QueryRow(r.Context(), `SELECT channel_id, status, built_by FROM images WHERE id=$1 FOR UPDATE`, id).
			Scan(&channelID, &status, &builtBy); err != nil {
			return audit.Entry{}, err
		}
		if status != "pending" {
			return audit.Entry{}, errStatus{"image is " + status + ", only pending can be approved"}
		}
		if s.cfg.TwoPerson && builtBy != nil && *builtBy == me.UserID {
			return audit.Entry{}, errStatus{"builder cannot approve their own image (2-person rule)"}
		}
		if _, err := tx.Exec(r.Context(), `UPDATE images SET status='superseded' WHERE channel_id=$1 AND status='published'`, channelID); err != nil {
			return audit.Entry{}, err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE images SET status='published', approved_by=$2, published_at=now() WHERE id=$1`, id, me.UserID); err != nil {
			return audit.Entry{}, err
		}
		return s.entry(r, "image.approve", "image", id, map[string]string{"channel_id": channelID}), nil
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
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "published"})
}

type errStatus struct{ msg string }

func (e errStatus) Error() string { return e.msg }

// ───────── hosts ─────────

func (s *Server) listHosts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT to_jsonb(h) - 'dev_token_hash' - 'x25519_pub' - 'os_disk' - 'bitlocker' - 'removed_token_hash'
		|| jsonb_build_object('site_name', s.name, 'channel_name', c.name,
		   'open_commands', (SELECT count(*) FROM agent_commands ac WHERE ac.host_id=h.id AND ac.status IN ('queued','delivered','running')),
		   -- latest reimage job, so lists and the map can show machines that are reinstalling or stuck in WinPE
		   'job', (SELECT jsonb_build_object('id', j.id, 'status', j.status, 'phase', j.phase, 'progress', j.progress,
		           'ponr', j.ponr_passed_at IS NOT NULL, 'updated_at', j.updated_at,
		           'image_version', (SELECT version FROM images WHERE id=j.image_id))
		           FROM deployment_jobs j WHERE j.host_id=h.id AND j.created_at >= COALESCE(h.history_since, '-infinity') ORDER BY j.created_at DESC LIMIT 1),
		   -- what SWIM last installed on it (null = never reimaged by SWIM)
		   'installed_image', (SELECT jsonb_build_object('image_id', i.id, 'version', i.version, 'channel_id', i.channel_id,
		           'channel_name', ch.name, 'iso', ch.name LIKE 'iso-%', 'edition', ch.edition, 'installed_at', j.finished_at)
		           FROM deployment_jobs j JOIN images i ON i.id=j.image_id JOIN channels ch ON ch.id=i.channel_id
		           WHERE j.host_id=h.id AND j.status='completed' ORDER BY j.finished_at DESC NULLS LAST LIMIT 1))
		FROM hosts h LEFT JOIN sites s ON s.id=h.site_id LEFT JOIN channels c ON c.id=h.channel_id
		WHERE h.deleted_at IS NULL
		ORDER BY h.retired_at IS NOT NULL, h.hostname LIMIT 2000`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var b []byte
		if err := row.Scan(&b); err != nil {
			return nil, err
		}
		var m map[string]any
		err := json.Unmarshal(b, &m)
		id, _ := m["id"].(string)
		m["online"] = s.pres.isOnline(id)
		return m, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ───────── enrollment tokens ─────────

func (s *Server) createEnrollToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SiteID   *string `json:"site_id"`
		MaxUses  *int    `json:"max_uses"`
		TTLHours int     `json:"ttl_hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	id, token, expires, err := s.newEnrollToken(r, in.SiteID, in.MaxUses, in.TTLHours, "command")
	if err != nil {
		s.dbErr(w, err)
		return
	}
	// The token is shown once; only its hash is stored.
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "token": token, "expires_at": expires})
}

// newEnrollToken creates a short-lived token and audits it. via = command | msi.
func (s *Server) newEnrollToken(r *http.Request, siteID *string, maxUses *int, ttlHours int, via string) (string, string, time.Time, error) {
	if ttlHours <= 0 || ttlHours > 24*30 {
		ttlHours = 72
	}
	if siteID != nil && *siteID == "" {
		siteID = nil
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", "", time.Time{}, err
	}
	token := "swim_et_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	id := ids.New("et_")
	expires := time.Now().Add(time.Duration(ttlHours) * time.Hour)
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO enrollment_tokens(id, token_hash, site_id, max_uses, expires_at, created_by)
			VALUES ($1,$2,$3,$4,$5,$6)`, id, hash[:], siteID, maxUses, expires, adminFrom(r).UserID)
		return s.entry(r, "enrollment_token.create", "enrollment_token", id,
			map[string]any{"site_id": siteID, "max_uses": maxUses, "expires_at": expires, "via": via}), err
	})
	return id, token, expires, err
}

// ───────── kill switches (spec 07 §4) ─────────

var killScopes = map[string]bool{"global": true, "channel": true, "site": true, "rollout": true}

func (s *Server) listKillSwitches(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT scope, scope_id, active, COALESCE(reason,''), COALESCE(set_by,''),
		set_at, COALESCE(cleared_by,''), cleared_at FROM kill_switches ORDER BY active DESC, scope, scope_id`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var scope, sid, reason, setBy, clearedBy string
		var active bool
		var setAt, clearedAt *time.Time
		err := row.Scan(&scope, &sid, &active, &reason, &setBy, &setAt, &clearedBy, &clearedAt)
		return map[string]any{"scope": scope, "scope_id": sid, "active": active, "reason": reason,
			"set_by": setBy, "set_at": setAt, "cleared_by": clearedBy, "cleared_at": clearedAt}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func scopeArgs(r *http.Request) (string, string, bool) {
	scope, id := r.PathValue("scope"), r.PathValue("id")
	if scope == "global" {
		id = "*"
	}
	return scope, id, killScopes[scope] && id != ""
}

// setKillSwitch turns a switch on. One person is enough: stopping is always safe.
func (s *Server) setKillSwitch(w http.ResponseWriter, r *http.Request) {
	scope, sid, ok := scopeArgs(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "scope must be global|channel|site|rollout")
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	me := adminFrom(r)
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO kill_switches(scope, scope_id, active, reason, set_by, set_at)
			VALUES ($1,$2,true,$3,$4,now())
			ON CONFLICT (scope, scope_id) DO UPDATE SET active=true, reason=$3, set_by=$4, set_at=now(),
				cleared_by=NULL, cleared_at=NULL`, scope, sid, in.Reason, "user:"+me.Name)
		e := s.entry(r, "kill_switch.set", "kill_switch", scope+"/"+sid, nil)
		e.Reason = in.Reason
		return e, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scope": scope, "scope_id": sid, "active": true})
}

// clearKillSwitch turns a switch off. With Config.TwoPerson, clearing the global
// switch needs a second admin: their token in X-Approver-Token (spec 08).
func (s *Server) clearKillSwitch(w http.ResponseWriter, r *http.Request) {
	scope, sid, ok := scopeArgs(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "scope must be global|channel|site|rollout")
		return
	}
	me := adminFrom(r)
	clearedBy := "user:" + me.Name
	if scope == "global" && s.cfg.TwoPerson {
		second := s.adminByToken(r.Header.Get("X-Approver-Token"))
		if second == nil || second.UserID == me.UserID {
			writeErr(w, http.StatusForbidden, "SWIM-API-403",
				"clearing the global kill switch needs a second admin (X-Approver-Token)")
			return
		}
		clearedBy += "+user:" + second.Name
	}
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE kill_switches SET active=false, cleared_by=$3, cleared_at=now()
			WHERE scope=$1 AND scope_id=$2 AND active`, scope, sid, clearedBy)
		if err != nil {
			return audit.Entry{}, err
		}
		if tag.RowsAffected() == 0 {
			return audit.Entry{}, pgx.ErrNoRows
		}
		e := s.entry(r, "kill_switch.clear", "kill_switch", scope+"/"+sid, map[string]string{"cleared_by": clearedBy})
		return e, nil
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scope": scope, "scope_id": sid, "active": false, "cleared_by": clearedBy})
}
