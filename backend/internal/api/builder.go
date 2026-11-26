package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/backend/internal/builder"
	"swim/backend/internal/ids"
	"swim/backend/internal/vm"
)

type vmView struct {
	Name      string        `json:"name"`
	State     string        `json:"state"`
	Disks     []vm.Disk     `json:"disks"`
	Snapshots []vm.Snapshot `json:"snapshots"`
	DiskBytes int64         `json:"disk_bytes"` // allocated on host
}

func (s *Server) listVMs(w http.ResponseWriter, r *http.Request) {
	names, err := s.lv.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "SWIM-API-502", "libvirt: "+err.Error())
		return
	}
	out := []vmView{}
	for _, n := range names {
		v := vmView{Name: n, Snapshots: []vm.Snapshot{}}
		v.State, _ = s.lv.State(r.Context(), n)
		v.Disks, _ = s.lv.Disks(r.Context(), n)
		if d, err := s.lv.MainDisk(r.Context(), n); err == nil {
			if snaps, err := vm.Snapshots(r.Context(), d); err == nil {
				v.Snapshots = snaps
			}
			if st, err := os.Stat(d); err == nil {
				v.DiskBytes = diskAllocated(st)
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// vmFrom validates the {name} path value against libvirt.
func (s *Server) vmFrom(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if !s.lv.Exists(r.Context(), name) {
		writeErr(w, http.StatusNotFound, "SWIM-API-404", "no such VM")
		return "", false
	}
	return name, true
}

func (s *Server) vmAction(w http.ResponseWriter, r *http.Request) {
	name, ok := s.vmFrom(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	if msg := s.captureInProgress(r.Context(), name); msg != "" && action != "shutdown" && action != "destroy" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", msg)
		return
	}
	if err := s.lv.Action(r.Context(), name, action); err != nil {
		writeErr(w, http.StatusConflict, "SWIM-API-409", err.Error())
		return
	}
	_ = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		return s.entry(r, "vm."+action, "vm", name, nil), nil
	})
	st, _ := s.lv.State(r.Context(), name)
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "state": st})
}

// vmMedia inserts an ISO from <data>/vms/iso or ejects it ({"iso": ""}).
func (s *Server) vmMedia(w http.ResponseWriter, r *http.Request) {
	name, ok := s.vmFrom(w, r)
	if !ok {
		return
	}
	var in struct {
		ISO string `json:"iso"`
	}
	if !decode(w, r, &in) {
		return
	}
	full := ""
	if in.ISO != "" {
		base := filepath.Base(in.ISO)
		full = filepath.Join(s.cfg.ISODir, base)
		if _, err := os.Stat(full); err != nil || !strings.HasSuffix(strings.ToLower(base), ".iso") {
			writeErr(w, http.StatusUnprocessableEntity, "SWIM-API-422", "ISO not found in "+s.cfg.ISODir)
			return
		}
	}
	if err := s.lv.SetMedia(r.Context(), name, full); err != nil {
		writeErr(w, http.StatusConflict, "SWIM-API-409", err.Error())
		return
	}
	_ = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		return s.entry(r, "vm.media", "vm", name, map[string]string{"iso": in.ISO}), nil
	})
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "iso": in.ISO})
}

func (s *Server) listISOs(w http.ResponseWriter, r *http.Request) {
	m, _ := filepath.Glob(filepath.Join(s.cfg.ISODir, "*.iso"))
	out := []map[string]any{}
	for _, p := range m {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		it := map[string]any{"name": filepath.Base(p), "size_bytes": st.Size(), "modified": st.ModTime()}
		// editions found by the last inspection of this file (iso_inspect), if it is newer than the file
		var res []byte
		if err := s.pool.QueryRow(r.Context(), `SELECT result FROM server_tasks WHERE kind='iso_inspect' AND status='done'
			AND payload->>'iso'=$1 AND finished_at > $2 ORDER BY finished_at DESC LIMIT 1`, filepath.Base(p), st.ModTime()).Scan(&res); err == nil {
			var v struct {
				File     string          `json:"file"`
				Editions json.RawMessage `json:"editions"`
			}
			if json.Unmarshal(res, &v) == nil && len(v.Editions) > 0 {
				it["install_file"], it["editions"] = v.File, v.Editions
			}
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, out)
}

// isoFrom validates the {name} of an ISO in the ISO folder.
func (s *Server) isoFrom(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if name != filepath.Base(name) || !strings.HasSuffix(strings.ToLower(name), ".iso") {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "invalid ISO name")
		return "", false
	}
	if st, err := os.Stat(filepath.Join(s.cfg.ISODir, name)); err != nil || st.IsDir() {
		writeErr(w, http.StatusNotFound, "SWIM-API-404", "no such ISO in the ISO folder")
		return "", false
	}
	return name, true
}

// isoInspect reads which Windows editions an ISO holds (builder task; the editions
// then appear in GET /v1/builder/isos and in the task's result).
//
//	POST /v1/builder/isos/{name}/inspect → {task_id}
func (s *Server) isoInspect(w http.ResponseWriter, r *http.Request) {
	name, ok := s.isoFrom(w, r)
	if !ok {
		return
	}
	id, err := s.queueTask(r, "iso_inspect", map[string]string{"iso": name}, name)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

// isoImport turns one edition of an ISO into an install image (status "pending" when
// the task finishes; approve it to make it installable).
//
//	POST /v1/builder/isos/{name}/import  {"index": 6, "version": "optional name"} → {task_id}
func (s *Server) isoImport(w http.ResponseWriter, r *http.Request) {
	name, ok := s.isoFrom(w, r)
	if !ok {
		return
	}
	var in struct {
		Index   int    `json:"index"`
		Version string `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Index < 1 {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "index: which edition of the ISO to import (see inspect)")
		return
	}
	if in.Version != "" && !vm.ValidName(in.Version) {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "version: letters, digits, . _ -")
		return
	}
	if d, err := statDisk(s.cfg.StorageDir); err == nil && d.Level == "block" {
		writeErr(w, http.StatusInsufficientStorage, "SWIM-API-507", "storage ≥ 90% — free space before importing")
		return
	}
	id, err := s.queueTask(r, "iso_import", map[string]string{"iso": name, "index": itoa(in.Index), "version": in.Version}, name)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

// ───────── console (Guacamole JSON auth) ─────────

// guacSign produces the `data` parameter of guacamole-auth-json:
// base64( AES-128-CBC_{key, iv=0}( HMAC-SHA256_key(json) || json ) ).
func guacSign(secretHex string, payload []byte) (string, error) {
	key, err := hex.DecodeString(secretHex)
	if err != nil || len(key) != 16 {
		return "", errors.New("GUAC_JSON_SECRET must be 32 hex chars")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	plain := append(mac.Sum(nil), payload...)
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	for i := 0; i < pad; i++ {
		plain = append(plain, byte(pad))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (s *Server) vmConsole(w http.ResponseWriter, r *http.Request) {
	name, ok := s.vmFrom(w, r)
	if !ok {
		return
	}
	port, err := s.lv.VNCPort(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "VM is not running")
		return
	}
	me := adminFrom(r)
	payload, _ := json.Marshal(map[string]any{
		"username": me.Name,
		"expires":  time.Now().Add(5 * time.Minute).UnixMilli(), // link must be opened within 5 min
		"connections": map[string]any{name: map[string]any{
			"protocol": "vnc",
			"parameters": map[string]string{
				"hostname": "127.0.0.1", "port": itoa(port),
				"color-depth": "24", "cursor": "remote", "clipboard-encoding": "UTF-8",
			},
		}},
	})
	data, err := guacSign(s.cfg.GuacSecret, payload)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", err.Error())
		return
	}
	clientID := base64.StdEncoding.EncodeToString([]byte(name + "\x00c\x00json"))
	_ = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		return s.entry(r, "vm.console", "vm", name, nil), nil
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"url": "/guacamole/#/client/" + clientID + "?data=" + url.QueryEscape(data),
	})
}

// ───────── tasks: snapshot / revert / capture ─────────

func (s *Server) queueTask(r *http.Request, kind string, payload map[string]string, objID string) (string, error) {
	id := ids.New("task_")
	payload["actor"] = adminFrom(r).UserID
	b, _ := json.Marshal(payload)
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO server_tasks(id, kind, payload) VALUES ($1,$2,$3::jsonb)`, id, kind, string(b))
		return s.entry(r, "task."+kind, "vm", objID, payload), err
	})
	return id, err
}

func (s *Server) vmSnapshot(w http.ResponseWriter, r *http.Request) {
	name, ok := s.vmFrom(w, r)
	if !ok {
		return
	}
	var in struct {
		Snapshot string `json:"snapshot"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !vm.ValidName(in.Snapshot) {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "snapshot name: letters, digits, . _ - (max 63)")
		return
	}
	if msg := s.captureInProgress(r.Context(), name); msg != "" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", msg)
		return
	}
	kind := map[string]string{"snapshot": "vm_snapshot", "revert": "vm_revert", "delete-snapshot": "vm_snapshot_delete"}[r.PathValue("op")]
	if kind == "" {
		writeErr(w, http.StatusNotFound, "SWIM-API-404", "unknown operation")
		return
	}
	if st, _ := s.lv.State(r.Context(), name); st != "shut off" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "shut the VM down first (it is "+st+")")
		return
	}
	id, err := s.queueTask(r, kind, map[string]string{"vm": name, "snapshot": in.Snapshot}, name)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

func (s *Server) vmCapture(w http.ResponseWriter, r *http.Request) {
	name, ok := s.vmFrom(w, r)
	if !ok {
		return
	}
	var in struct {
		ChannelID string `json:"channel_id"`
		Version   string `json:"version"`
		RevertTo  string `json:"revert_to"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !vm.ValidName(in.Version) {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "version: letters, digits, . _ - (e.g. v2026.11.1)")
		return
	}
	if in.RevertTo != "" && !vm.ValidName(in.RevertTo) {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "invalid revert_to snapshot")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM images WHERE channel_id=$1 AND version=$2)`,
		in.ChannelID, in.Version).Scan(&exists); err != nil {
		s.dbErr(w, err)
		return
	}
	if exists {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "this channel already has version "+in.Version)
		return
	}
	if st, _ := s.lv.State(r.Context(), name); st != "shut off" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "VM must be shut off after sysprep (it is "+st+")")
		return
	}
	if d, err := statDisk(s.cfg.StorageDir); err == nil && d.Level == "block" {
		writeErr(w, http.StatusInsufficientStorage, "SWIM-API-507", "storage ≥ 90% — free space before capturing")
		return
	}
	id, err := s.queueTask(r, "capture_image", map[string]string{"vm": name, "channel_id": in.ChannelID,
		"version": in.Version, "revert_to": in.RevertTo}, name)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT id, kind, payload, status, COALESCE(result,'{}'::jsonb), created_at, finished_at
		FROM server_tasks ORDER BY created_at DESC LIMIT 30`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var id, kind, status string
		var payload, result map[string]any
		var created time.Time
		var finished *time.Time
		err := row.Scan(&id, &kind, &payload, &status, &result, &created, &finished)
		return map[string]any{"id": id, "kind": kind, "payload": payload, "status": status, "result": result,
			"created_at": created, "finished_at": finished}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) taskLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !vm.ValidName(id) {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "bad id")
		return
	}
	b, err := os.ReadFile(builder.LogPath(s.cfg.StorageDir, id))
	if err != nil {
		writeErr(w, http.StatusNotFound, "SWIM-API-404", "no log yet")
		return
	}
	if len(b) > 16384 {
		b = b[len(b)-16384:]
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b)
}

// captureInProgress returns why the VM's disk must not change now: a capture reads it
// through a qcow2 overlay, and booting or reverting the master would corrupt that overlay.
func (s *Server) captureInProgress(ctx context.Context, vmName string) string {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT id FROM server_tasks WHERE kind='capture_image' AND status IN ('queued','running')
		AND payload->>'vm' = $1 LIMIT 1`, vmName).Scan(&id)
	if err != nil {
		return ""
	}
	return "an image is being captured from " + vmName + " (" + id + ") — wait until it finishes; starting or reverting the VM now would corrupt the capture"
}
