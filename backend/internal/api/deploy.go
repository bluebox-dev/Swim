package api

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/nacl/box"

	"swim/backend/internal/audit"
	"swim/backend/internal/ids"
	"swim/pkg/manifest"
)

const minReimageAgent = "0.5.0"

var terminalJob = map[string]bool{"completed": true, "failed": true, "aborted": true, "cancelled": true}

func randToken(prefix string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Server) readJSONFile(rel string, v any) error {
	b, err := os.ReadFile(filepath.Join(s.cfg.StorageDir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ───────── admin: start a reimage ─────────

// deployRequest is the body of POST /v1/hosts/{id}/deploy (and a scheduled deploy).
type deployRequest struct {
	ImageID       string               `json:"image_id"`
	Disk          manifest.DiskProfile `json:"disk"`
	Hostname      string               `json:"hostname"`
	AdminUser     string               `json:"admin_user"`
	AdminPassword string               `json:"admin_password"`
	Reason        string               `json:"reason"`
}

func (s *Server) createDeploy(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("id")
	var in deployRequest
	if !decode(w, r, &in) {
		return
	}
	res, ce := s.startDeploy(r.Context(), "user:"+adminFrom(r).Name, clientIP(r), hostID, in)
	if ce != nil {
		writeErr(w, ce.status, fmt.Sprintf("SWIM-API-%d", ce.status), ce.msg)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// startDeploy validates and queues a reimage of one machine. actor/ip go to the audit log.
func (s *Server) startDeploy(ctx context.Context, actor, ip, hostID string, in deployRequest) (map[string]any, *cmdErr) {
	switch {
	case in.Disk.Mode != "refresh" && in.Disk.Mode != "repartition":
		return nil, &cmdErr{http.StatusBadRequest, "disk.mode must be refresh or repartition"}
	case in.Disk.SystemGB != 0 && in.Disk.SystemGB < 40:
		return nil, &cmdErr{http.StatusBadRequest, "system partition must be at least 40 GB (or 0 = all space)"}
	case strings.TrimSpace(in.AdminUser) == "" || len(in.AdminPassword) < 8:
		return nil, &cmdErr{http.StatusBadRequest, "local administrator name and a password of at least 8 characters are required"}
	}
	if in.Disk.Mode == "refresh" {
		in.Disk.SystemGB, in.Disk.DataLabel = 0, ""
	}

	var hostname, agentVer string
	var xpub []byte
	var retired *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(hostname,''), COALESCE(agent_version,''), x25519_pub, retired_at
		FROM hosts WHERE id=$1`, hostID).Scan(&hostname, &agentVer, &xpub, &retired); err != nil {
		return nil, dbCmdErr(err)
	}
	if retired != nil {
		return nil, &cmdErr{http.StatusConflict, "this machine was removed from SWIM"}
	}
	if versionLess(agentVer, minReimageAgent) {
		return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " cannot reimage — update the agent to " + minReimageAgent + " or later first"}
	}
	if len(xpub) != 32 {
		return nil, &cmdErr{http.StatusConflict, "machine has no encryption key — re-enroll it"}
	}
	if in.Hostname = strings.ToUpper(strings.TrimSpace(in.Hostname)); in.Hostname == "" {
		in.Hostname = strings.ToUpper(hostname)
	}
	if len(in.Hostname) > 15 || strings.ContainsAny(in.Hostname, ` \/:*?"<>|,.~!@#$%^&'(){}_`) {
		return nil, &cmdErr{http.StatusBadRequest, "computer name: up to 15 letters, digits or '-'"}
	}

	var img manifest.FileRef
	var status string
	var expanded int64
	var wofFiles *int64
	var inv []byte
	if err := s.pool.QueryRow(ctx, `SELECT version, COALESCE(file_path,''), COALESCE(sha256,''), COALESCE(size_bytes,0), status,
		COALESCE(expanded_bytes,0), wof_files FROM images WHERE id=$1`, in.ImageID).Scan(&img.Version, &img.Path, &img.SHA256, &img.Size,
		&status, &expanded, &wofFiles); err != nil {
		return nil, dbCmdErr(err)
	}
	if status != "published" {
		return nil, &cmdErr{http.StatusConflict, "image is " + status + " — only published images can be installed"}
	}
	// WinPE cannot run programs from a volume whose system files are WOF-compressed
	// (CompactOS), so the new Windows could not be finished after the point of no return.
	if wofFiles == nil || *wofFiles > 0 {
		return nil, &cmdErr{http.StatusConflict, "image " + img.Version + " still contains CompactOS (WOF) compressed system files " +
			"and cannot be installed by WinPE — capture a new version (the builder now stores those files uncompressed)"}
	}
	if msg := s.notGeneralized(ctx, in.ImageID, img.Version); msg != "" {
		return nil, &cmdErr{http.StatusConflict, msg}
	}
	_ = s.pool.QueryRow(ctx, `SELECT COALESCE(inventory::text,'{}') FROM hosts WHERE id=$1`, hostID).Scan(&inv)
	if msg := diskTooSmall(in.Disk, expanded, inv); msg != "" {
		return nil, &cmdErr{http.StatusBadRequest, msg}
	}
	var pe struct {
		Version string           `json:"version"`
		BootWim manifest.FileRef `json:"boot_wim"`
		BootSDI manifest.FileRef `json:"boot_sdi"`
		SwimPE  manifest.FileRef `json:"swim_pe"`
	}
	if err := s.readJSONFile("winpe/latest.json", &pe); err != nil || pe.BootWim.Path == "" {
		return nil, &cmdErr{http.StatusConflict, "no WinPE build on the server — run scripts/build-winpe.sh"}
	}
	var ag struct{ Version, File, SHA256 string }
	if err := s.readJSONFile("agent/latest.json", &ag); err != nil || ag.File == "" {
		return nil, &cmdErr{http.StatusConflict, "no agent build on the server"}
	}

	jobID := ids.New("job_")
	ticket, peToken := randToken("swim_rt_"), randToken("swim_pe_")
	secrets, _ := json.Marshal(manifest.ReimageSecrets{AdminPassword: in.AdminPassword, Ticket: ticket, PEToken: peToken})
	var pub [32]byte
	copy(pub[:], xpub)
	sealed, err := box.SealAnonymous(nil, secrets, &pub, rand.Reader)
	if err != nil {
		return nil, &cmdErr{http.StatusInternalServerError, "seal: " + err.Error()}
	}
	pe.BootWim.Version = pe.Version
	if pe.SwimPE.Path != "" {
		pe.SwimPE.Version = pe.Version
	}
	args := manifest.ReimageArgs{JobID: jobID, Image: img, WinPE: pe.BootWim, BootSDI: pe.BootSDI, PEExe: pe.SwimPE,
		Agent: manifest.FileRef{Version: ag.Version, Path: "agent/" + ag.File, SHA256: ag.SHA256},
		Disk:  in.Disk, Hostname: in.Hostname, Admin: strings.TrimSpace(in.AdminUser), TimeZone: "SE Asia Standard Time",
		Sealed: base64.StdEncoding.EncodeToString(sealed)}
	argsJSON, _ := json.Marshal(args)
	p, ce := s.planCommand(ctx, hostID, "reimage", argsJSON, in.Reason, actor)
	if ce != nil {
		return nil, ce
	}
	th, ph := sha256.Sum256([]byte(ticket)), sha256.Sum256([]byte(peToken))
	diskJSON, _ := json.Marshal(in.Disk)
	err = s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
		if _, err := tx.Exec(ctx, `INSERT INTO deployment_jobs(id, host_id, kind, engine, image_id, disk_mode, disk,
			target_hostname, status, phase, progress, actor, pe_token_hash, manifest_not_after)
			VALUES ($1,$2,'deploy','winpe',$3,$4,$5::jsonb,$6,'queued','queued',0,$7,$8,$9)`,
			jobID, hostID, in.ImageID, in.Disk.Mode, string(diskJSON), in.Hostname, actor, ph[:], p.Expires); err != nil {
			return audit.Entry{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO reimage_tickets(ticket_hash, host_id, job_id, expires_at) VALUES ($1,$2,$3,$4)`,
			th[:], hostID, jobID, time.Now().Add(72*time.Hour)); err != nil {
			return audit.Entry{}, err
		}
		if err := insertCommand(ctx, tx, p, in.Reason, actor, nil); err != nil {
			return audit.Entry{}, err
		}
		e := audit.Entry{Actor: actor, Action: "job.deploy", ObjectType: "host", ObjectID: hostID, IP: ip, Result: "ok", Data: map[string]any{"job_id": jobID, "image": img.Version, "disk": in.Disk,
			"hostname": in.Hostname, "admin_user": args.Admin}}
		e.Reason = in.Reason
		return e, nil
	})
	if err != nil {
		var pe2 interface{ SQLState() string }
		if errors.As(err, &pe2) && pe2.SQLState() == "23505" {
			return nil, &cmdErr{http.StatusConflict, "this machine already has an unfinished job"}
		}
		return nil, dbCmdErr(err)
	}
	s.pres.nudge(hostID)
	s.events.publish(Event{Type: "job.updated", HostID: hostID, Data: map[string]any{"id": jobID, "status": "queued"}})
	return map[string]any{"job_id": jobID, "command_id": p.ID}, nil
}

type jobView struct {
	ID         string          `json:"id"`
	HostID     string          `json:"host_id"`
	Hostname   string          `json:"hostname"`
	Image      string          `json:"image_version"`
	Status     string          `json:"status"`
	Phase      *string         `json:"phase"`
	Progress   *int            `json:"progress"`
	Message    *string         `json:"message"`
	Disk       json.RawMessage `json:"disk"`
	Actor      string          `json:"actor"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	FinishedAt *time.Time      `json:"finished_at"`
	Result     json.RawMessage `json:"result"`
	PONR       bool            `json:"ponr"` // passed the point of no return (old Windows gone)
	// the staged install can be booted into WinPE again (POST /v1/jobs/{id}/rehandoff): aborted
	// at the hand-off by the server watchdog (the agent, when it notices first, deletes the
	// answer file) and no retry of it has failed since
	CanRehandoff bool `json:"can_rehandoff"`
}

const jobCols = `j.id, j.host_id, COALESCE(j.target_hostname,''), COALESCE(i.version,''), j.status, j.phase, j.progress, j.message,
	COALESCE(j.disk,'null'::jsonb), j.actor, j.created_at, j.updated_at, j.finished_at, COALESCE(j.result,'null'::jsonb),
	j.ponr_passed_at IS NOT NULL,
	(j.status='aborted' AND j.phase='handoff' AND j.ponr_passed_at IS NULL
	 AND (SELECT source FROM job_events e WHERE e.job_id=j.id AND e.status='aborted' ORDER BY e.at DESC LIMIT 1)='server'
	 AND NOT EXISTS(SELECT 1 FROM agent_commands c WHERE c.type='rehandoff' AND c.args->>'job_id'=j.id AND c.status IN ('failed','expired')))`

func scanJob(row pgx.Row) (jobView, error) {
	var j jobView
	var disk, res []byte
	err := row.Scan(&j.ID, &j.HostID, &j.Hostname, &j.Image, &j.Status, &j.Phase, &j.Progress, &j.Message, &disk, &j.Actor,
		&j.CreatedAt, &j.UpdatedAt, &j.FinishedAt, &res, &j.PONR, &j.CanRehandoff)
	j.Disk, j.Result = disk, res
	return j, err
}

func (s *Server) listHostJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT `+jobCols+` FROM deployment_jobs j LEFT JOIN images i ON i.id=j.image_id
		WHERE j.host_id=$1 AND j.created_at >= COALESCE((SELECT history_since FROM hosts WHERE id=$1), '-infinity')
		ORDER BY j.created_at DESC LIMIT 20`, r.PathValue("id"))
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (jobView, error) { return scanJob(row) })
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := scanJob(s.pool.QueryRow(r.Context(), `SELECT `+jobCols+` FROM deployment_jobs j LEFT JOIN images i ON i.id=j.image_id WHERE j.id=$1`, id))
	if err != nil {
		s.dbErr(w, err)
		return
	}
	rows, err := s.pool.Query(r.Context(), `SELECT source, COALESCE(status,''), COALESCE(step,''), COALESCE(severity,''), COALESCE(data,'{}'::jsonb), at
		FROM job_events WHERE job_id=$1 ORDER BY at, seq`, id)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var src, st, step, sev string
		var data []byte
		var at time.Time
		err := row.Scan(&src, &st, &step, &sev, &data, &at)
		return map[string]any{"source": src, "status": st, "phase": step, "level": sev, "data": json.RawMessage(data), "at": at}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	var logText *string
	_ = s.pool.QueryRow(r.Context(), `SELECT log FROM deployment_jobs WHERE id=$1`, id).Scan(&logText)
	writeJSON(w, http.StatusOK, map[string]any{"job": j, "events": events, "log": logText})
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var hostID string
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		err := tx.QueryRow(r.Context(), `UPDATE deployment_jobs SET status='cancelled', phase='cancelled', finished_at=now(), updated_at=now()
			WHERE id=$1 AND status IN ('queued','staging') RETURNING host_id`, id).Scan(&hostID)
		if errors.Is(err, pgx.ErrNoRows) {
			return audit.Entry{}, errStatus{"only a job that has not reached the reboot (handoff) can be cancelled"}
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE agent_commands SET status='cancelled', finished_at=now()
				WHERE host_id=$1 AND type='reimage' AND status='queued'`, hostID)
		}
		return s.entry(r, "job.cancel", "host", hostID, map[string]string{"job_id": id}), err
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
	s.events.publish(Event{Type: "job.updated", HostID: hostID, Data: map[string]any{"id": id, "status": "cancelled"}})
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "cancelled"})
}

// ───────── agent / WinPE: progress, files, drivers, final report ─────────

type jobEventIn struct {
	Status   string `json:"status"` // staging | handoff | pe | completed | failed | aborted (optional)
	Phase    string `json:"phase"`
	Progress *int   `json:"progress"`
	Message  string `json:"message"`
	Level    string `json:"level"`
	Log      string `json:"log"`
}

// recordJobEvent appends an event and moves the job's phase/status forward.
// It returns whether the job was cancelled by an admin (the agent then stops staging).
func (s *Server) recordJobEvent(r *http.Request, jobID, hostID, source string, in jobEventIn) (bool, error) {
	cancelled := false
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var status string
		if err := tx.QueryRow(r.Context(), `SELECT status FROM deployment_jobs WHERE id=$1 AND host_id=$2 FOR UPDATE`, jobID, hostID).Scan(&status); err != nil {
			return audit.Entry{}, err
		}
		if status == "cancelled" {
			cancelled = true
		}
		data, _ := json.Marshal(map[string]any{"message": in.Message, "progress": in.Progress})
		if _, err := tx.Exec(r.Context(), `INSERT INTO job_events(job_id, seq, source, status, step, severity, data, at)
			VALUES ($1, (SELECT COALESCE(max(seq),0)+1 FROM job_events WHERE job_id=$1 AND source=$2), $2, NULLIF($3,''), $4, NULLIF($5,''), $6::jsonb, now())`,
			jobID, source, in.Status, in.Phase, in.Level, string(data)); err != nil {
			return audit.Entry{}, err
		}
		if terminalJob[status] && !(status == "aborted" || status == "failed") { // never move a finished job (except late detail)
			return audit.Entry{}, nil
		}
		newStatus := status
		if in.Status != "" && !cancelled {
			newStatus = in.Status
		}
		_, err := tx.Exec(r.Context(), `UPDATE deployment_jobs SET status=$2, phase=COALESCE(NULLIF($3,''),phase), progress=COALESCE($4,progress),
			message=COALESCE(NULLIF($5,''),message), log=CASE WHEN $6='' THEN log ELSE $6 END, updated_at=now(),
			finished_at=CASE WHEN $2 IN ('completed','failed','aborted','cancelled') THEN COALESCE(finished_at, now()) ELSE NULL END,
			ponr_passed_at=CASE WHEN $3='ponr' THEN COALESCE(ponr_passed_at, now()) ELSE ponr_passed_at END
			WHERE id=$1`, jobID, newStatus, in.Phase, in.Progress, in.Message, in.Log)
		if err != nil || !terminalJob[newStatus] {
			return audit.Entry{}, err
		}
		return audit.Entry{Actor: source + ":" + hostID, Action: "job." + newStatus, ObjectType: "job", ObjectID: jobID,
			IP: clientIP(r), Result: newStatus, Data: map[string]string{"message": in.Message}}, nil
	})
	if err == nil {
		s.events.publish(Event{Type: "job.updated", HostID: hostID, Data: map[string]any{"id": jobID, "phase": in.Phase, "progress": in.Progress, "status": in.Status}})
	}
	return cancelled, err
}

func (s *Server) agentJobEvent(w http.ResponseWriter, r *http.Request) {
	hostID, _, ok := s.hostAuth(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var in jobEventIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "invalid JSON")
		return
	}
	s.pres.touch(hostID)
	cancelled, err := s.recordJobEvent(r, r.PathValue("id"), hostID, "agent", in)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancel": cancelled})
}

// peJobEvent: WinPE has no host token, only the job's one-time PE token.
func (s *Server) peJobEvent(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	h := sha256.Sum256([]byte(bearer(r)))
	var hostID string
	if err := s.pool.QueryRow(r.Context(), `SELECT host_id FROM deployment_jobs WHERE id=$1 AND pe_token_hash=$2`, jobID, h[:]).Scan(&hostID); err != nil {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "invalid job token")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var in jobEventIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "invalid JSON")
		return
	}
	if _, err := s.recordJobEvent(r, jobID, hostID, "pe", in); err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// agentFile lets an enrolled agent download images, WinPE and agent builds.
// nginx serves the bytes (Range/resume) after this auth check.
func (s *Server) agentFile(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.hostAuth(w, r); !ok {
		return
	}
	rel := path.Clean("/" + r.PathValue("path"))[1:]
	top := strings.SplitN(rel, "/", 2)[0]
	if (top != "images" && top != "winpe" && top != "agent") || strings.Contains(rel, "..") {
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

// agentDrivers receives this machine's storage/network drivers (zip) and builds a
// per-job WinPE that can see the disk: base boot.wim + /swim/drivers (loaded by
// swim-pe with drvload) — also injected into the new Windows before first boot.
func (s *Server) agentDrivers(w http.ResponseWriter, r *http.Request) {
	hostID, _, ok := s.hostAuth(w, r)
	if !ok {
		return
	}
	jobID := r.PathValue("id")
	var status string
	if err := s.pool.QueryRow(r.Context(), `SELECT status FROM deployment_jobs WHERE id=$1 AND host_id=$2`, jobID, hostID).Scan(&status); err != nil {
		s.dbErr(w, err)
		return
	}
	if terminalJob[status] {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "job is "+status)
		return
	}
	var pe struct {
		Version string           `json:"version"`
		BootWim manifest.FileRef `json:"boot_wim"`
	}
	if err := s.readJSONFile("winpe/latest.json", &pe); err != nil {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "no WinPE build")
		return
	}
	dir := filepath.Join(s.cfg.StorageDir, "winpe", "jobs", jobID)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, "drivers"), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", err.Error())
		return
	}
	zipPath := filepath.Join(dir, "drivers.zip")
	f, err := os.Create(zipPath)
	if err == nil {
		_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, 512<<20))
		f.Close()
	}
	if err == nil {
		err = unzipTo(zipPath, filepath.Join(dir, "drivers"))
	}
	wim := filepath.Join(dir, "boot.wim")
	if err == nil {
		err = copyFileSimple(filepath.Join(s.cfg.StorageDir, filepath.FromSlash(pe.BootWim.Path)), wim)
	}
	if err == nil {
		out, e := exec.CommandContext(r.Context(), "wimlib-imagex", "update", wim, "1",
			"--command=add "+filepath.Join(dir, "drivers")+" /swim/drivers").CombinedOutput()
		if e != nil {
			err = fmt.Errorf("wimlib update: %v: %s", e, out)
		}
	}
	var sum string
	var size int64
	if err == nil {
		sum, size, err = sha256File(wim)
	}
	_ = os.RemoveAll(filepath.Join(dir, "drivers"))
	if err != nil {
		s.log.Error("per-job winpe", "job", jobID, "err", err)
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "WinPE build failed: "+err.Error())
		return
	}
	rel := "winpe/jobs/" + jobID + "/boot.wim"
	_, _ = s.pool.Exec(r.Context(), `UPDATE deployment_jobs SET boot_wim=$2, updated_at=now() WHERE id=$1`, jobID, rel)
	writeJSON(w, http.StatusOK, manifest.FileRef{Version: pe.Version, Path: rel, SHA256: sum, Size: size})
}

func unzipTo(zipPath, dst string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	n := 0
	var total int64
	if len(zr.File) > 20000 {
		return errors.New("driver zip has too many files")
	}
	for _, f := range zr.File {
		// a small zip can unpack to far more than it weighs: stop at 2 GB of drivers
		if total += int64(f.UncompressedSize64); total > 2<<30 || f.UncompressedSize64 > 256<<20 {
			return errors.New("driver zip is too large when unpacked")
		}
		name := filepath.FromSlash(path.Clean("/" + strings.ReplaceAll(f.Name, `\`, "/"))[1:])
		if name == "" || strings.Contains(name, "..") {
			continue
		}
		target := filepath.Join(dst, name)
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err == nil {
			_, err = io.Copy(out, io.LimitReader(rc, 256<<20))
			out.Close()
		}
		rc.Close()
		if err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return errors.New("driver zip is empty")
	}
	return nil
}

func copyFileSimple(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func sha256File(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// notGeneralized: an image whose latest S-01 check failed was not sysprepped — it would
// deploy as a clone of the master and ignore the computer name and admin account.
// Images without an S-01 result are not judged here.
func (s *Server) notGeneralized(ctx context.Context, imageID, version string) string {
	var passed *bool
	if err := s.pool.QueryRow(ctx, `SELECT passed FROM image_tests WHERE image_id=$1 AND test_id='S-01' ORDER BY at DESC LIMIT 1`,
		imageID).Scan(&passed); err == nil && passed != nil && !*passed {
		return "image " + version + " was not generalized with sysprep (check S-01 failed) — it would install as a copy of the master " +
			"and ignore the computer name and admin account; capture a new version"
	}
	return ""
}

// minSystemGB is the smallest Windows partition an image fits in: its expanded size
// plus room for drivers, the page file, updates and the first user profile.
func minSystemGB(expanded int64) int {
	const marginGB = 15
	return int((expanded+(1<<30)-1)>>30) + marginGB
}

// diskTooSmall checks the requested layout against the image size and, when the
// agent reported it, the machine's disks. Returns "" when it fits.
func diskTooSmall(d manifest.DiskProfile, expanded int64, inventory []byte) string {
	if expanded <= 0 {
		return ""
	}
	need := minSystemGB(expanded)
	var inv struct {
		Disks []struct {
			Drive string `json:"drive"`
			Size  int64  `json:"size"`
		} `json:"disks"`
		Physical []struct {
			Size int64 `json:"size"`
		} `json:"physical_disks"`
	}
	_ = json.Unmarshal(inventory, &inv)
	var biggest int64
	for _, p := range inv.Physical {
		biggest = max(biggest, p.Size)
	}
	const stagingGB = 20
	switch {
	case d.Mode == "repartition" && d.SystemGB > 0 && d.SystemGB < need:
		return fmt.Sprintf("C: %d GB is too small for this image — it needs at least %d GB (%.1f GB expanded + 15 GB free space)",
			d.SystemGB, need, float64(expanded)/(1<<30))
	case d.Mode == "repartition" && biggest > 0 && int(biggest>>30) < need+stagingGB:
		return fmt.Sprintf("disk is %d GB — this image needs at least %d GB for Windows plus %d GB for the staging partition",
			biggest>>30, need, stagingGB)
	case d.Mode == "refresh":
		for _, v := range inv.Disks {
			if strings.EqualFold(v.Drive, "C:") && v.Size > 0 && int(v.Size>>30)+1 < need {
				return fmt.Sprintf("the current C: partition is %d GB — this image needs at least %d GB; choose \"repartition\" with a bigger C:",
					v.Size>>30, need)
			}
		}
	}
	return ""
}

// dbCmdErr maps a database error to an API error for helpers that do not write responses.
func dbCmdErr(err error) *cmdErr {
	if errors.Is(err, pgx.ErrNoRows) {
		return &cmdErr{http.StatusNotFound, "not found"}
	}
	var pe interface{ SQLState() string }
	if errors.As(err, &pe) && pe.SQLState() == "23505" {
		return &cmdErr{http.StatusConflict, "this machine already has an unfinished job"}
	}
	return &cmdErr{http.StatusInternalServerError, "database error"}
}
