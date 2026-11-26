package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/backend/internal/ids"
	"swim/pkg/manifest"
)

// ───────── command planning (shared by single-host and group commands) ─────────

type cmdErr struct {
	status int
	msg    string
}

type cmdPlan struct {
	ID, HostID, Type  string
	ArgsJSON, EnvJSON []byte
	AuditArgs         map[string]any
	Expires           time.Time
}

// delete_staged_image: agents before this version do not know the command.
const minStagedImageAgent = "0.5.9"

// wake (Wake-on-LAN relay) ships with the first agent built after 0.5.11.
const minWakeAgent = "0.5.12"

var stagedVersionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// planCommand validates and signs one command for one host. It does not write.
func (s *Server) planCommand(ctx context.Context, hostID, typ string, rawArgs json.RawMessage, reason, actor string) (*cmdPlan, *cmdErr) {
	risk, ok := manifest.CommandTypes[typ]
	if !ok {
		return nil, &cmdErr{http.StatusBadRequest, "unknown command type (allowlist only)"}
	}
	var uuid, agentVer string
	var siteID, channelID *string
	var retired, review *time.Time
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(smbios_uuid::text,''), COALESCE(agent_version,''), site_id, channel_id, retired_at, review_at
		FROM hosts WHERE id=$1`, hostID).Scan(&uuid, &agentVer, &siteID, &channelID, &retired, &review)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &cmdErr{http.StatusNotFound, "no such machine"}
	} else if err != nil {
		return nil, &cmdErr{http.StatusInternalServerError, "database error"}
	}
	if retired != nil {
		return nil, &cmdErr{http.StatusConflict, "this machine was removed from SWIM"}
	}
	if review != nil && !reviewAllowed[typ] {
		return nil, &cmdErr{http.StatusConflict, "this machine came back after it was removed — accept it (รับกลับเข้าระบบ) or uninstall its agent first"}
	}
	_ = risk // every command type is allowed; signing + full logging are the controls

	var args any = map[string]any{}
	auditArgs := map[string]any{}
	switch typ {
	case "reboot":
		var a manifest.RebootArgs
		_ = json.Unmarshal(rawArgs, &a)
		if a.DelayS == 0 {
			a.DelayS = 300
		}
		a.DelayS = max(60, min(a.DelayS, 86400))
		if a.Message = cleanMessage(a.Message); a.Message == "" {
			a.Message = "IT will restart this computer (SWIM). Please save your work."
		}
		args, auditArgs = a, map[string]any{"delay_s": a.DelayS, "message": a.Message}
	case "run_script":
		// a library script (any that is not retired) or PowerShell typed ad hoc; either way the
		// full text travels in the signed command and is kept in agent_commands.args
		var a struct {
			ScriptID string `json:"script_id"`
			Name     string `json:"name"`
			Body     string `json:"body"`
			TimeoutS int    `json:"timeout_s"`
		}
		_ = json.Unmarshal(rawArgs, &a)
		var sa manifest.ScriptArgs
		if a.ScriptID != "" {
			var status string
			err := s.pool.QueryRow(ctx, `SELECT id, name, sha256, body, timeout_s, status FROM scripts WHERE id=$1`, a.ScriptID).
				Scan(&sa.ScriptID, &sa.Name, &sa.SHA256, &sa.Body, &sa.TimeoutS, &status)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, &cmdErr{http.StatusUnprocessableEntity, "unknown script"}
			} else if err != nil {
				return nil, &cmdErr{http.StatusInternalServerError, "database error"}
			}
			if status == "retired" {
				return nil, &cmdErr{http.StatusConflict, "script was retired"}
			}
		} else {
			if strings.TrimSpace(a.Body) == "" || len(a.Body) > 65536 {
				return nil, &cmdErr{http.StatusBadRequest, "PowerShell body required (≤ 64 KB)"}
			}
			sum := sha256.Sum256([]byte(a.Body))
			sa = manifest.ScriptArgs{Name: strings.TrimSpace(a.Name), Body: a.Body, SHA256: hex.EncodeToString(sum[:]), TimeoutS: a.TimeoutS}
			if sa.Name == "" {
				sa.Name = "PowerShell (พิมพ์เอง)"
			}
			if sa.TimeoutS == 0 {
				sa.TimeoutS = 600
			}
			sa.TimeoutS = max(10, min(sa.TimeoutS, 3600))
		}
		args, auditArgs = sa, map[string]any{"script_id": sa.ScriptID, "name": sa.Name, "sha256": sa.SHA256, "ad_hoc": sa.ScriptID == "",
			"body": sa.Body, "timeout_s": sa.TimeoutS}
	case "reimage":
		var a manifest.ReimageArgs
		if err := json.Unmarshal(rawArgs, &a); err != nil || a.JobID == "" {
			return nil, &cmdErr{http.StatusBadRequest, "reimage is started from the deploy endpoint"}
		}
		if versionLess(agentVer, minReimageAgent) {
			return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " cannot reimage"}
		}
		args, auditArgs = a, map[string]any{"job_id": a.JobID, "image": a.Image.Version, "disk": a.Disk}
	case "wake":
		var a manifest.WakeArgs
		_ = json.Unmarshal(rawArgs, &a)
		if len(a.MACs) == 0 || len(a.MACs) > 256 {
			return nil, &cmdErr{http.StatusBadRequest, "1–256 MAC addresses required"}
		}
		for i, m := range a.MACs {
			hw, err := net.ParseMAC(strings.TrimSpace(m))
			if err != nil || len(hw) != 6 {
				return nil, &cmdErr{http.StatusBadRequest, "invalid MAC address " + m}
			}
			a.MACs[i] = hw.String()
		}
		if versionLess(agentVer, minWakeAgent) {
			return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " cannot relay Wake-on-LAN — update the agent to " + minWakeAgent + " first"}
		}
		args, auditArgs = a, map[string]any{"macs": a.MACs}
	case "delete_staged_image":
		var a manifest.DeleteStagedImageArgs
		_ = json.Unmarshal(rawArgs, &a)
		if !stagedVersionRe.MatchString(a.Version) {
			return nil, &cmdErr{http.StatusBadRequest, "image version required"}
		}
		if versionLess(agentVer, minStagedImageAgent) {
			return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " cannot delete stored images — update the agent to " + minStagedImageAgent + " first"}
		}
		var busy bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_jobs WHERE host_id=$1 AND finished_at IS NULL)`, hostID).Scan(&busy); err != nil {
			return nil, &cmdErr{http.StatusInternalServerError, "database error"}
		}
		if busy {
			return nil, &cmdErr{http.StatusConflict, "this machine has a reimage job in progress — stored images stay until it finishes"}
		}
		args, auditArgs = a, map[string]any{"version": a.Version}
	case "release_staging":
		if versionLess(agentVer, "0.5.13") {
			return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " cannot release the staging partition — update the agent to 0.5.13 first"}
		}
		var busy bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_jobs WHERE host_id=$1 AND finished_at IS NULL)`, hostID).Scan(&busy); err != nil {
			return nil, &cmdErr{http.StatusInternalServerError, "database error"}
		}
		if busy {
			return nil, &cmdErr{http.StatusConflict, "this machine has a reimage job in progress — its staged files stay until it finishes"}
		}
		args = manifest.ReleaseStagingArgs{Force: true} // no open job: leftovers of aborted installs may go
	case "remote_session":
		// built by the remote endpoint only (hostRemote): the password is sealed to the host key
		var a manifest.RemoteSessionArgs
		if err := json.Unmarshal(rawArgs, &a); err != nil || a.Sealed == "" {
			return nil, &cmdErr{http.StatusBadRequest, "remote sessions are started from the remote endpoint"}
		}
		if versionLess(agentVer, minRemoteSessionAgent) {
			return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " cannot open a remote session without a password — it updates itself to " + minRemoteSessionAgent + " shortly"}
		}
		args, auditArgs = a, map[string]any{"account": remoteAccount, "allow_from": a.AllowFrom, "ttl_s": a.TTLS, "tunnel": a.Tunnel != ""}
	case "prestage_image":
		var a manifest.PrestageArgs
		if err := json.Unmarshal(rawArgs, &a); err != nil {
			return nil, &cmdErr{http.StatusBadRequest, "prestage_image: invalid arguments"}
		}
		if versionLess(agentVer, minPrestageAgent) {
			return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " cannot keep images ahead of time — it updates itself to " + minPrestageAgent + " shortly"}
		}
		var busy bool
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_jobs WHERE host_id=$1 AND finished_at IS NULL)`, hostID).Scan(&busy)
		if busy {
			return nil, &cmdErr{http.StatusConflict, "this machine has a reimage job in progress — the stored image is set after it finishes"}
		}
		args, auditArgs = a, map[string]any{"image_id": a.ImageID, "version": a.Image.Version}
	case "rehandoff":
		var a manifest.RehandoffArgs
		if err := json.Unmarshal(rawArgs, &a); err != nil || a.JobID == "" {
			return nil, &cmdErr{http.StatusBadRequest, "rehandoff is started from POST /v1/jobs/{id}/rehandoff"}
		}
		if versionLess(agentVer, "0.5.21") {
			return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " cannot retry the restart into WinPE — it updates itself to 0.5.21 shortly"}
		}
		args, auditArgs = a, map[string]any{"job_id": a.JobID}
	case "set_policy":
		var a manifest.SetPolicyArgs
		if err := json.Unmarshal(rawArgs, &a); err != nil {
			return nil, &cmdErr{http.StatusBadRequest, "set_policy: invalid arguments"}
		}
		if versionLess(agentVer, minPolicyAgent) {
			return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " cannot apply machine policy — it updates itself to " + minPolicyAgent + " shortly"}
		}
		args, auditArgs = a, map[string]any{"install_lock": a.InstallLock, "keep": a.Keep}
	case "update_agent":
		var latest manifest.UpdateArgs
		b, err := os.ReadFile(filepath.Join(s.cfg.StorageDir, "agent", "latest.json"))
		if err != nil || json.Unmarshal(b, &latest) != nil || latest.File == "" || len(latest.SHA256) != 64 {
			return nil, &cmdErr{http.StatusConflict, "no agent build published on the server"}
		}
		if agentVer == latest.Version {
			return nil, &cmdErr{http.StatusConflict, "already on agent " + agentVer}
		}
		if versionLess(agentVer, "0.4.0") {
			return nil, &cmdErr{http.StatusConflict, "agent " + agentVer + " predates update_agent — run the approved \"อัปเดต SWIM Agent\" script instead"}
		}
		args, auditArgs = latest, map[string]any{"from": agentVer, "to": latest.Version, "sha256": latest.SHA256}
	}
	argsJSON, _ := json.Marshal(args)

	now := time.Now().UTC().Truncate(time.Second)
	cmd := manifest.Command{Schema: 1, Kind: "command", CommandID: ids.New("cmd_"), Type: typ, Args: argsJSON,
		Actor: actor, IssuedAt: now, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(commandTTL)}
	cmd.Host.HostID, cmd.Host.SmbiosUUID = hostID, uuid
	payload, _ := json.Marshal(cmd)
	envJSON, _ := json.Marshal(manifest.Sign(s.key.Priv, s.key.Kid, payload))
	return &cmdPlan{ID: cmd.CommandID, HostID: hostID, Type: typ, ArgsJSON: argsJSON, EnvJSON: envJSON,
		AuditArgs: auditArgs, Expires: cmd.NotAfter}, nil
}

func insertCommand(ctx context.Context, tx pgx.Tx, p *cmdPlan, reason, actor string, batchID *string) error {
	_, err := tx.Exec(ctx, `INSERT INTO agent_commands(id, host_id, type, args, envelope, reason, created_by, expires_at, batch_id)
		VALUES ($1,$2,$3,$4::jsonb,$5::jsonb,NULLIF($6,''),$7,$8,$9)`, p.ID, p.HostID, p.Type, string(p.ArgsJSON),
		string(p.EnvJSON), reason, actor, p.Expires, batchID)
	return err
}

func (s *Server) createCommand(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("id")
	var in struct {
		Type   string          `json:"type"`
		Args   json.RawMessage `json:"args"`
		Reason string          `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	actor := "user:" + adminFrom(r).Name
	var ts *toolScript
	if in.Type == "run_script" { // from outside: a tool or a saved script, never script text
		var ce *cmdErr
		if in.Args, ts, ce = scriptRequest(in.Args); ce != nil {
			writeErr(w, ce.status, fmt.Sprintf("SWIM-API-%d", ce.status), ce.msg)
			return
		}
	}
	p, ce := s.planCommand(r.Context(), hostID, in.Type, in.Args, in.Reason, actor)
	if ce != nil {
		writeErr(w, ce.status, fmt.Sprintf("SWIM-API-%d", ce.status), ce.msg)
		return
	}
	p.applyTool(ts)
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		err := insertCommand(r.Context(), tx, p, in.Reason, actor, nil)
		e := s.entry(r, "command."+in.Type, "host", hostID, map[string]any{"command_id": p.ID, "args": p.AuditArgs})
		e.Reason = in.Reason
		return e, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	s.pres.nudge(hostID)
	s.events.publish(Event{Type: "command.updated", HostID: hostID, Data: map[string]any{"id": p.ID, "status": "queued"}})
	writeJSON(w, http.StatusCreated, map[string]any{"id": p.ID, "status": "queued", "expires_at": p.Expires})
}

// ───────── group commands ─────────

// createBatch sends one command to every machine of a site, or to a selection.
// Each machine gets its own signed command; machines that cannot take it
// (removed, kill switch, already up to date…) are listed as skipped.
func (s *Server) createBatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type    string          `json:"type"`
		Args    json.RawMessage `json:"args"`
		Reason  string          `json:"reason"`
		SiteID  *string         `json:"site_id"` // "" = machines without a site
		HostIDs []string        `json:"host_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if _, ok := manifest.CommandTypes[in.Type]; !ok {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "unknown command type (allowlist only)")
		return
	}
	var target map[string]any
	var hostIDs []string
	switch {
	case in.SiteID != nil:
		rows, err := s.pool.Query(r.Context(), `SELECT id FROM hosts WHERE retired_at IS NULL AND
			(CASE WHEN $1 = '' THEN site_id IS NULL ELSE site_id = $1 END) ORDER BY hostname`, *in.SiteID)
		if err == nil {
			hostIDs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		}
		if err != nil {
			s.dbErr(w, err)
			return
		}
		target = map[string]any{"site_id": *in.SiteID}
	case len(in.HostIDs) > 0:
		hostIDs, target = in.HostIDs, map[string]any{"host_ids": in.HostIDs}
	default:
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "site_id or host_ids required")
		return
	}
	if len(hostIDs) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "SWIM-API-422", "no machines in this group")
		return
	}
	if len(hostIDs) > 2000 {
		writeErr(w, http.StatusUnprocessableEntity, "SWIM-API-422", "at most 2000 machines per group command")
		return
	}
	actor := "user:" + adminFrom(r).Name
	var ts *toolScript
	if in.Type == "run_script" { // from outside: a tool or a saved script, never script text
		var ce *cmdErr
		if in.Args, ts, ce = scriptRequest(in.Args); ce != nil {
			writeErr(w, ce.status, fmt.Sprintf("SWIM-API-%d", ce.status), ce.msg)
			return
		}
	}
	var plans []*cmdPlan
	skipped := []map[string]string{}
	for _, id := range hostIDs {
		p, ce := s.planCommand(r.Context(), id, in.Type, in.Args, in.Reason, actor)
		if p != nil {
			p.applyTool(ts)
		}
		if ce != nil {
			if ce.status == http.StatusBadRequest { // same for every host: report once
				writeErr(w, ce.status, "SWIM-API-400", ce.msg)
				return
			}
			skipped = append(skipped, map[string]string{"host_id": id, "reason": ce.msg})
			continue
		}
		plans = append(plans, p)
	}
	batchID := ids.New("bat_")
	argsForRecord := json.RawMessage("{}")
	if len(plans) > 0 {
		var a map[string]any
		_ = json.Unmarshal(plans[0].ArgsJSON, &a)
		delete(a, "body") // never copy script bodies into the batch record
		argsForRecord, _ = json.Marshal(a)
	}
	targetJSON, _ := json.Marshal(target)
	skippedJSON, _ := json.Marshal(skipped)
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		if _, err := tx.Exec(r.Context(), `INSERT INTO command_batches(id, type, args, reason, target, total, skipped, created_by)
			VALUES ($1,$2,$3::jsonb,NULLIF($4,''),$5::jsonb,$6,$7::jsonb,$8)`, batchID, in.Type, string(argsForRecord), in.Reason,
			string(targetJSON), len(plans), string(skippedJSON), actor); err != nil {
			return audit.Entry{}, err
		}
		for _, p := range plans {
			if err := insertCommand(r.Context(), tx, p, in.Reason, actor, &batchID); err != nil {
				return audit.Entry{}, err
			}
		}
		e := s.entry(r, "batch."+in.Type, "batch", batchID, map[string]any{"target": target, "queued": len(plans), "skipped": len(skipped)})
		e.Reason = in.Reason
		return e, nil
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	for _, p := range plans {
		s.pres.nudge(p.HostID)
	}
	s.events.publish(Event{Type: "batch.updated", Data: map[string]any{"id": batchID}})
	writeJSON(w, http.StatusCreated, map[string]any{"id": batchID, "queued": len(plans), "skipped": skipped})
}

func (s *Server) listBatches(w http.ResponseWriter, r *http.Request) {
	s.expireCommands(r)
	rows, err := s.pool.Query(r.Context(), `SELECT b.id, b.type, b.args, b.reason, b.target, b.total, jsonb_array_length(b.skipped),
		b.created_by, b.created_at, s.name,
		count(c.*) FILTER (WHERE c.status='done'), count(c.*) FILTER (WHERE c.status='failed'),
		count(c.*) FILTER (WHERE c.status IN ('expired','cancelled')), count(c.*) FILTER (WHERE c.status IN ('queued','delivered','running'))
		FROM command_batches b LEFT JOIN agent_commands c ON c.batch_id=b.id LEFT JOIN sites s ON s.id = b.target->>'site_id'
		GROUP BY b.id, s.name ORDER BY b.created_at DESC LIMIT 20`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var id, typ, by string
		var args, target []byte
		var reason, siteName *string
		var total, skipped, done, failed, gone, open int
		var at time.Time
		err := row.Scan(&id, &typ, &args, &reason, &target, &total, &skipped, &by, &at, &siteName, &done, &failed, &gone, &open)
		return map[string]any{"id": id, "type": typ, "args": json.RawMessage(args), "reason": reason, "target": json.RawMessage(target),
			"site_name": siteName, "total": total, "skipped": skipped, "created_by": by, "created_at": at,
			"done": done, "failed": failed, "expired": gone, "open": open}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getBatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var skipped []byte
	var typ string
	if err := s.pool.QueryRow(r.Context(), `SELECT type, skipped FROM command_batches WHERE id=$1`, id).Scan(&typ, &skipped); err != nil {
		s.dbErr(w, err)
		return
	}
	rows, err := s.pool.Query(r.Context(), `SELECT c.id, c.host_id, COALESCE(h.hostname,''), c.status, c.finished_at,
		COALESCE(c.result->>'error',''), left(COALESCE(c.result->>'output',''), 300)
		FROM agent_commands c JOIN hosts h ON h.id=c.host_id WHERE c.batch_id=$1 ORDER BY h.hostname`, id)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var cid, hid, hn, st, errText, out string
		var fin *time.Time
		err := row.Scan(&cid, &hid, &hn, &st, &fin, &errText, &out)
		return map[string]any{"command_id": cid, "host_id": hid, "hostname": hn, "status": st, "finished_at": fin,
			"error": errText, "output": out, "online": s.pres.isOnline(hid)}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "type": typ, "items": items, "skipped": json.RawMessage(skipped)})
}

// ───────── sites & assignment ─────────

func (s *Server) updateSite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "name required")
		return
	}
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE sites SET name=$2, description=NULLIF($3,'') WHERE id=$1`, id, strings.TrimSpace(in.Name), in.Description)
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
		return s.entry(r, "site.update", "site", id, in), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) deleteSite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var n int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM hosts WHERE site_id=$1 AND retired_at IS NULL`, id).Scan(&n); err != nil {
			return audit.Entry{}, err
		}
		if n > 0 {
			return audit.Entry{}, errStatus{fmt.Sprintf("move the %d machine(s) out of this site first", n)}
		}
		for _, q := range []string{`UPDATE hosts SET site_id=NULL WHERE site_id=$1`, `UPDATE enrollment_tokens SET site_id=NULL WHERE site_id=$1`,
			`DELETE FROM kill_switches WHERE scope='site' AND scope_id=$1 AND NOT active`, `DELETE FROM sites WHERE id=$1`} {
			if _, err := tx.Exec(r.Context(), q, id); err != nil {
				return audit.Entry{}, err
			}
		}
		return s.entry(r, "site.delete", "site", id, nil), nil
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
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "deleted"})
}

// assignSite moves machines into a site ("" / null = no site). Also sets the
// channel when channel_id is given, so a group can be pointed at an image line.
func (s *Server) assignSite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostIDs   []string `json:"host_ids"`
		SiteID    *string  `json:"site_id"`
		ChannelID *string  `json:"channel_id"`
		Field     string   `json:"field"` // site | channel
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.HostIDs) == 0 || (in.Field != "site" && in.Field != "channel") {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "host_ids and field (site|channel) required")
		return
	}
	val := in.SiteID
	col := "site_id"
	if in.Field == "channel" {
		val, col = in.ChannelID, "channel_id"
	}
	if val != nil && *val == "" {
		val = nil
	}
	var n int64
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE hosts SET `+col+`=$2 WHERE id = ANY($1) AND retired_at IS NULL`, in.HostIDs, val)
		n = tag.RowsAffected()
		return s.entry(r, "host.assign_"+in.Field, "host", strings.Join(in.HostIDs, ","), map[string]any{col: val, "count": n}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	for _, id := range in.HostIDs {
		s.events.publish(Event{Type: "host.updated", HostID: id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

// versionLess compares dotted numeric versions (missing parts count as 0).
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscanf(pa[i], "%d", &x)
		}
		if i < len(pb) {
			fmt.Sscanf(pb[i], "%d", &y)
		}
		if x != y {
			return x < y
		}
	}
	return false
}
