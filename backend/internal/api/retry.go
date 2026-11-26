package api

// Retry of a reimage job that failed after the point of no return. The machine is
// then sitting in WinPE (its old Windows is gone) and polls GET /v1/pe/jobs/{id}/next.
// An admin picks an image (default: the published version of the machine's channel)
// with POST /v1/jobs/{id}/retry; WinPE downloads it plus a refreshed WinPE for this
// job (same drivers, current swim-pe) and starts over from partitioning.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/pkg/manifest"
)

type retryPlan struct {
	ImageID string           `json:"image_id"`
	Image   manifest.FileRef `json:"image"`
	WinPE   manifest.FileRef `json:"winpe"`
	// Agent is the agent published when the admin pressed retry: the new Windows gets the
	// current one, not the one staged when the job began (WinPE older than 0.5.40 ignores it)
	Agent       manifest.FileRef `json:"agent"`
	RequestedBy string           `json:"requested_by"`
	At          time.Time        `json:"at"`
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		ImageID string `json:"image_id"`
		Reason  string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	var hostID, status string
	var ponr *time.Time
	var channelID *string
	var disk, inv []byte
	if err := s.pool.QueryRow(r.Context(), `SELECT j.host_id, j.status, j.ponr_passed_at, h.channel_id, COALESCE(j.disk,'{}'::jsonb), COALESCE(h.inventory::text,'{}')
		FROM deployment_jobs j JOIN hosts h ON h.id=j.host_id WHERE j.id=$1`, id).Scan(&hostID, &status, &ponr, &channelID, &disk, &inv); err != nil {
		s.dbErr(w, err)
		return
	}
	if status != "failed" || ponr == nil {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "only a job that failed after the point of no return can be retried (the machine waits in WinPE)")
		return
	}
	if in.ImageID == "" {
		if channelID == nil || s.pool.QueryRow(r.Context(), `SELECT id FROM images WHERE channel_id=$1 AND status='published'`, *channelID).Scan(&in.ImageID) != nil {
			writeErr(w, http.StatusConflict, "SWIM-API-409", "this machine's channel has no published image — choose one")
			return
		}
	}
	var img manifest.FileRef
	var imgStatus string
	var expanded int64
	var wofFiles *int64
	if err := s.pool.QueryRow(r.Context(), `SELECT version, COALESCE(file_path,''), COALESCE(sha256,''), COALESCE(size_bytes,0), status,
		COALESCE(expanded_bytes,0), wof_files FROM images WHERE id=$1`, in.ImageID).Scan(&img.Version, &img.Path, &img.SHA256, &img.Size,
		&imgStatus, &expanded, &wofFiles); err != nil {
		s.dbErr(w, err)
		return
	}
	switch {
	case imgStatus != "published":
		writeErr(w, http.StatusConflict, "SWIM-API-409", "image is "+imgStatus+" — only published images can be installed")
		return
	case wofFiles == nil || *wofFiles > 0:
		writeErr(w, http.StatusConflict, "SWIM-API-409", "image "+img.Version+" still contains CompactOS (WOF) compressed system files — capture a new version")
		return
	}
	if msg := s.notGeneralized(r.Context(), in.ImageID, img.Version); msg != "" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", msg)
		return
	}
	var d manifest.DiskProfile
	_ = json.Unmarshal(disk, &d)
	if msg := diskTooSmall(d, expanded, inv); msg != "" {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", msg)
		return
	}
	pe, err := s.refreshJobWinPE(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "WinPE for this job: "+err.Error())
		return
	}
	var ag struct{ Version, File, SHA256 string }
	var agent manifest.FileRef
	if s.readJSONFile("agent/latest.json", &ag) == nil && ag.File != "" {
		agent = manifest.FileRef{Version: ag.Version, Path: "agent/" + ag.File, SHA256: ag.SHA256}
	}
	plan, _ := json.Marshal(retryPlan{ImageID: in.ImageID, Image: img, WinPE: pe, Agent: agent, RequestedBy: adminFrom(r).Name, At: time.Now().UTC()})
	msg := "waiting for the machine (WinPE) to pick up the retry — image " + img.Version
	err = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE deployment_jobs SET status='retry', phase='retry', progress=0, message=$2, retry=$3::jsonb,
			image_id=$4, finished_at=NULL, updated_at=now() WHERE id=$1 AND status='failed'`, id, msg, string(plan), in.ImageID)
		if err == nil && tag.RowsAffected() == 0 {
			err = errStatus{"job changed meanwhile — reload"}
		}
		if err != nil {
			return audit.Entry{}, err
		}
		data, _ := json.Marshal(map[string]any{"message": "retry requested by " + adminFrom(r).Name + " — image " + img.Version + ": " + in.Reason})
		if _, err := tx.Exec(r.Context(), `INSERT INTO job_events(job_id, seq, source, status, step, severity, data, at)
			VALUES ($1, (SELECT COALESCE(max(seq),0)+1 FROM job_events WHERE job_id=$1 AND source='admin'), 'admin', 'retry', 'retry', 'warn', $2::jsonb, now())`,
			id, string(data)); err != nil {
			return audit.Entry{}, err
		}
		e := s.entry(r, "job.retry", "host", hostID, map[string]any{"job_id": id, "image": img.Version})
		e.Reason = in.Reason
		return e, nil
	})
	var es errStatus
	var pgErr interface{ SQLState() string }
	switch {
	case errors.As(err, &es):
		writeErr(w, http.StatusConflict, "SWIM-API-409", es.msg)
		return
	case errors.As(err, &pgErr) && pgErr.SQLState() == "23505":
		writeErr(w, http.StatusConflict, "SWIM-API-409", "this machine already has another unfinished job")
		return
	case err != nil:
		s.dbErr(w, err)
		return
	}
	s.events.publish(Event{Type: "job.updated", HostID: hostID, Data: map[string]any{"id": id, "status": "retry"}})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "retry", "image": img.Version})
}

// refreshJobWinPE puts the current swim-pe.exe (from the latest WinPE build) into this
// job's boot.wim, which already carries the machine's storage/network drivers.
func (s *Server) refreshJobWinPE(r *http.Request, jobID string) (manifest.FileRef, error) {
	var latest struct {
		Version string           `json:"version"`
		BootWim manifest.FileRef `json:"boot_wim"`
	}
	if err := s.readJSONFile("winpe/latest.json", &latest); err != nil || latest.BootWim.Path == "" {
		return manifest.FileRef{}, errors.New("no WinPE build on the server")
	}
	rel := path.Join("winpe", "jobs", jobID, "boot.wim")
	wim := filepath.Join(s.cfg.StorageDir, filepath.FromSlash(rel))
	if _, err := os.Stat(wim); err != nil {
		// no drivers were uploaded for this job: the stock build is what it booted
		rel, wim = latest.BootWim.Path, filepath.Join(s.cfg.StorageDir, filepath.FromSlash(latest.BootWim.Path))
	} else {
		tmp, err := os.MkdirTemp("", "swim-pe-")
		if err != nil {
			return manifest.FileRef{}, err
		}
		defer os.RemoveAll(tmp)
		src := filepath.Join(s.cfg.StorageDir, filepath.FromSlash(latest.BootWim.Path))
		if out, err := exec.CommandContext(r.Context(), "wimlib-imagex", "extract", src, "1", "/Windows/System32/swim-pe.exe",
			"--dest-dir="+tmp, "--no-acls").CombinedOutput(); err != nil {
			return manifest.FileRef{}, fmt.Errorf("extract swim-pe: %v: %s", err, out)
		}
		cmd := exec.CommandContext(r.Context(), "wimlib-imagex", "update", wim, "1")
		cmd.Stdin = strings.NewReader("add " + filepath.Join(tmp, "swim-pe.exe") + " /Windows/System32/swim-pe.exe\n")
		if out, err := cmd.CombinedOutput(); err != nil {
			return manifest.FileRef{}, fmt.Errorf("update job WinPE: %v: %s", err, out)
		}
	}
	sum, n, err := sha256File(wim)
	if err != nil {
		return manifest.FileRef{}, err
	}
	return manifest.FileRef{Version: latest.Version, Path: rel, SHA256: sum, Size: n}, nil
}

// peJobAuth checks the job's WinPE token.
func (s *Server) peJobAuth(w http.ResponseWriter, r *http.Request) (jobID, hostID, status string, retry []byte, ok bool) {
	jobID = r.PathValue("id")
	h := sha256.Sum256([]byte(bearer(r)))
	if err := s.pool.QueryRow(r.Context(), `SELECT host_id, status, COALESCE(retry,'null'::jsonb) FROM deployment_jobs WHERE id=$1 AND pe_token_hash=$2`,
		jobID, h[:]).Scan(&hostID, &status, &retry); err != nil {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "invalid job token")
		return "", "", "", nil, false
	}
	return jobID, hostID, status, retry, true
}

// peNext: WinPE asks what to do after a failure. "wait" until an admin retries.
func (s *Server) peNext(w http.ResponseWriter, r *http.Request) {
	jobID, hostID, status, raw, ok := s.peJobAuth(w, r)
	if !ok {
		return
	}
	if status != "retry" {
		writeJSON(w, http.StatusOK, map[string]any{"action": "wait"})
		return
	}
	var plan retryPlan
	if err := json.Unmarshal(raw, &plan); err != nil || plan.Image.Path == "" {
		writeJSON(w, http.StatusOK, map[string]any{"action": "wait"})
		return
	}
	// a fresh one-time ticket for the new Windows' agent (the old one may have expired)
	ticket := randToken("swim_rt_")
	th := sha256.Sum256([]byte(ticket))
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		if _, err := tx.Exec(r.Context(), `INSERT INTO reimage_tickets(ticket_hash, host_id, job_id, expires_at) VALUES ($1,$2,$3,$4)`,
			th[:], hostID, jobID, time.Now().Add(72*time.Hour)); err != nil {
			return audit.Entry{}, err
		}
		_, err := tx.Exec(r.Context(), `UPDATE deployment_jobs SET status='pe', phase='retry_download', progress=1,
			message='WinPE picked up the retry — downloading '||$2::text, updated_at=now() WHERE id=$1 AND status='retry'`, jobID, plan.Image.Version)
		return audit.Entry{}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	s.events.publish(Event{Type: "job.updated", HostID: hostID, Data: map[string]any{"id": jobID, "status": "pe", "phase": "retry_download"}})
	// the agent published now, when WinPE picks the retry up (the plan's own is the fallback)
	agent := plan.Agent
	var ag struct{ Version, File, SHA256 string }
	if s.readJSONFile("agent/latest.json", &ag) == nil && ag.File != "" {
		agent = manifest.FileRef{Version: ag.Version, Path: "agent/" + ag.File, SHA256: ag.SHA256}
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": "retry", "image": plan.Image, "winpe": plan.WinPE, "agent": agent, "ticket": ticket})
}

// peAgentFile: the agent builds WinPE may fetch on a retry (the Windows build only).
var peAgentFile = regexp.MustCompile(`^agent/swim-agent-[0-9]+\.[0-9]+\.[0-9]+-windows-amd64\.exe$`)

// peFile: WinPE downloads the retry image, its refreshed WinPE and the current agent (nginx
// serves the bytes).
func (s *Server) peFile(w http.ResponseWriter, r *http.Request) {
	jobID, _, status, _, ok := s.peJobAuth(w, r)
	if !ok {
		return
	}
	if status == "completed" || status == "cancelled" { // the job is over: its token opens nothing any more
		writeErr(w, http.StatusForbidden, "SWIM-API-403", "job is "+status)
		return
	}
	rel := path.Clean("/" + r.PathValue("path"))[1:]
	if strings.Contains(rel, "..") || !(strings.HasPrefix(rel, "images/") || strings.HasPrefix(rel, "winpe/jobs/"+jobID+"/") ||
		(strings.HasPrefix(rel, "winpe/pe-") && strings.HasSuffix(rel, "/boot.wim")) || peAgentFile.MatchString(rel)) {
		writeErr(w, http.StatusForbidden, "SWIM-API-403", "not downloadable")
		return
	}
	if _, err := os.Stat(filepath.Join(s.cfg.StorageDir, filepath.FromSlash(rel))); err != nil {
		writeErr(w, http.StatusNotFound, "SWIM-API-404", "no such file")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Accel-Redirect", "/storage/"+rel)
	w.WriteHeader(http.StatusOK)
}
