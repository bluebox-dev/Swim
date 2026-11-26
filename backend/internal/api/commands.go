package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/backend/internal/ids"
)

const (
	commandTTL      = time.Hour
	maxOutputBytes  = 512 << 10
	longPollMaxWait = 20 * time.Second // keep ≤ offlineAfter − 10 s
)

// hostAuth resolves the calling agent from its host token. Retired hosts have
// no token any more, so they are rejected here as well.
func (s *Server) hostAuth(w http.ResponseWriter, r *http.Request) (id, uuid string, ok bool) {
	tok := bearer(r)
	if tok == "" {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "host token required")
		return "", "", false
	}
	h := sha256.Sum256([]byte(tok))
	err := s.pool.QueryRow(r.Context(), `SELECT id, COALESCE(smbios_uuid::text,'') FROM hosts
		WHERE dev_token_hash=$1 AND retired_at IS NULL`, h[:]).Scan(&id, &uuid)
	if errors.Is(err, pgx.ErrNoRows) {
		// the agent of a removed machine: it may only fetch its uninstall command and report it
		if strings.HasPrefix(r.URL.Path, "/v1/agent/commands") {
			if tid, tuuid, ok := s.tombstoned(r, h[:]); ok {
				return tid, tuuid, true
			}
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "unknown host token")
		return "", "", false
	}
	if err != nil {
		s.dbErr(w, err)
		return "", "", false
	}
	return id, uuid, true
}

func (s *Server) expireCommands(r *http.Request) {
	_, _ = s.pool.Exec(r.Context(), `UPDATE agent_commands SET status='expired', finished_at=now()
		WHERE status IN ('queued','delivered') AND expires_at < now()`)
	// a reimage the machine never picked up (it stayed off for the command's lifetime) must
	// not block the machine forever: cancel its job, nothing on the disk was touched
	_, _ = s.pool.Exec(r.Context(), `UPDATE deployment_jobs j SET status='cancelled', phase='cancelled',
		message='the machine did not pick up the install within 1 hour (it was off or offline) — nothing was changed',
		finished_at=now(), updated_at=now()
		WHERE j.status='queued' AND j.finished_at IS NULL AND EXISTS(SELECT 1 FROM agent_commands c
			WHERE c.type='reimage' AND c.status='expired' AND c.args->>'job_id'=j.id)`)
}

// ───────── admin: send / list / cancel ─────────

func cleanMessage(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '"' || r == '\r' || r == '\n' || r == '%' || r == '^' || r == '&' || r == '|' || r == '<' || r == '>' {
			return -1
		}
		return r
	}, s)
	if rs := []rune(strings.TrimSpace(s)); len(rs) > 200 {
		s = string(rs[:200])
	}
	return strings.TrimSpace(s)
}

type commandView struct {
	ID         string          `json:"id"`
	HostID     string          `json:"host_id"`
	Type       string          `json:"type"`
	Args       json.RawMessage `json:"args"`
	Status     string          `json:"status"`
	Reason     *string         `json:"reason"`
	CreatedBy  string          `json:"created_by"`
	CreatedAt  time.Time       `json:"created_at"`
	ExpiresAt  time.Time       `json:"expires_at"`
	Delivered  *time.Time      `json:"delivered_at"`
	FinishedAt *time.Time      `json:"finished_at"`
	Result     json.RawMessage `json:"result"`
}

// args without script bodies; result output trimmed unless full=true.
const commandCols = `id, host_id, type, CASE WHEN $2 THEN args ELSE (args - 'body') END, status, reason, created_by, created_at, expires_at, delivered_at, finished_at,
	CASE WHEN $2 THEN result ELSE result || jsonb_build_object('output', left(result->>'output', 4000)) END`

func scanCommands(rows pgx.Rows) ([]commandView, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (commandView, error) {
		var c commandView
		var args, res []byte
		err := row.Scan(&c.ID, &c.HostID, &c.Type, &args, &c.Status, &c.Reason, &c.CreatedBy, &c.CreatedAt, &c.ExpiresAt,
			&c.Delivered, &c.FinishedAt, &res)
		c.Args, c.Result = args, res
		if len(c.Result) == 0 {
			c.Result = json.RawMessage("null")
		}
		return c, err
	})
}

func (s *Server) listHostCommands(w http.ResponseWriter, r *http.Request) {
	s.expireCommands(r)
	rows, err := s.pool.Query(r.Context(), `SELECT `+commandCols+` FROM agent_commands WHERE host_id=$1
		AND cleared_at IS NULL AND created_at >= COALESCE((SELECT history_since FROM hosts WHERE id=$1), '-infinity') ORDER BY created_at DESC LIMIT 50`,
		r.PathValue("id"), false)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := scanCommands(rows)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getCommand(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT `+commandCols+` FROM agent_commands WHERE id=$1`, r.PathValue("id"), true)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := scanCommands(rows)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	if len(out) == 0 {
		writeErr(w, http.StatusNotFound, "SWIM-API-404", "not found")
		return
	}
	writeJSON(w, http.StatusOK, out[0])
}

func (s *Server) cancelCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var hostID string
		err := tx.QueryRow(r.Context(), `UPDATE agent_commands SET status='cancelled', finished_at=now()
			WHERE id=$1 AND status='queued' RETURNING host_id`, id).Scan(&hostID)
		if errors.Is(err, pgx.ErrNoRows) {
			return audit.Entry{}, errStatus{"only commands still waiting in the queue can be cancelled"}
		}
		return s.entry(r, "command.cancel", "host", hostID, map[string]string{"command_id": id}), err
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
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "cancelled"})
}

// deleteHost permanently removes a host that was already retired, with its
// command history. The audit log keeps the full trail (it is append-only).
func (s *Server) deleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var hostname string
		var retired *time.Time
		if err := tx.QueryRow(r.Context(), `SELECT COALESCE(hostname,''), retired_at FROM hosts WHERE id=$1 FOR UPDATE`, id).Scan(&hostname, &retired); err != nil {
			return audit.Entry{}, err
		}
		if retired == nil {
			return audit.Entry{}, errStatus{"remove the machine from SWIM first (uninstall or retire), then delete it"}
		}
		// its agent may still be installed: hide the machine but keep the tombstone, so it
		// shows up in "pending review" if that agent ever comes back
		var tomb bool
		if err := tx.QueryRow(r.Context(), `SELECT removed_token_hash IS NOT NULL FROM hosts WHERE id=$1`, id).Scan(&tomb); err != nil {
			return audit.Entry{}, err
		}
		if tomb {
			_, err := tx.Exec(r.Context(), `UPDATE hosts SET deleted_at=now() WHERE id=$1`, id)
			return s.entry(r, "host.delete", "host", id, map[string]string{"hostname": hostname, "mode": "hidden, tombstone kept"}), err
		}
		var jobs int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM deployment_jobs WHERE host_id=$1`, id).Scan(&jobs); err != nil {
			return audit.Entry{}, err
		}
		if jobs > 0 {
			return audit.Entry{}, errStatus{"this machine has deployment history — it is kept for reporting"}
		}
		for _, q := range []string{`DELETE FROM agent_commands WHERE host_id=$1`, `DELETE FROM reimage_tickets WHERE host_id=$1`,
			`DELETE FROM agent_update_events WHERE host_id=$1`, `DELETE FROM log_bundles WHERE host_id=$1`, `DELETE FROM hosts WHERE id=$1`} {
			if _, err := tx.Exec(r.Context(), q, id); err != nil {
				return audit.Entry{}, err
			}
		}
		return s.entry(r, "host.delete", "host", id, map[string]string{"hostname": hostname}), nil
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
	s.events.publish(Event{Type: "host.updated", HostID: id})
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "deleted"})
}

func (s *Server) getHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var b []byte
	err := s.pool.QueryRow(r.Context(), `SELECT to_jsonb(h) - 'dev_token_hash' - 'x25519_pub' - 'removed_token_hash'
		|| jsonb_build_object('site_name', s.name, 'channel_name', c.name, 'online', $2::boolean,
		   -- accounts the install lock keeps as administrators (the agent also keeps RID 500 and groups)
		   'policy_keep', jsonb_build_array('Administrator', 'swim-remote') || COALESCE((SELECT jsonb_build_array(ac.args->>'admin_user')
		       FROM agent_commands ac WHERE ac.host_id=h.id AND ac.type='reimage' AND ac.status='done' AND ac.args->>'admin_user' <> ''
		       ORDER BY ac.created_at DESC LIMIT 1), '[]'::jsonb),
		   'installed_image', (SELECT jsonb_build_object('image_id', i.id, 'version', i.version, 'channel_id', i.channel_id,
		       'channel_name', ch.name, 'iso', ch.name LIKE 'iso-%', 'edition', ch.edition, 'installed_at', j.finished_at)
		       FROM deployment_jobs j JOIN images i ON i.id=j.image_id JOIN channels ch ON ch.id=i.channel_id
		       WHERE j.host_id=h.id AND j.status='completed' ORDER BY j.finished_at DESC NULLS LAST LIMIT 1))
		FROM hosts h LEFT JOIN sites s ON s.id = h.site_id LEFT JOIN channels c ON c.id = h.channel_id WHERE h.id=$1`,
		id, s.pres.isOnline(id)).Scan(&b)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// ───────── agent: long-poll + results ─────────

func (s *Server) agentCommands(w http.ResponseWriter, r *http.Request) {
	hostID, _, ok := s.hostAuth(w, r)
	if !ok {
		return
	}
	// An open long-poll is how the server knows the machine is online right now.
	cameOnline, wake := s.pres.begin(hostID)
	defer s.pres.end(hostID)
	s.markOnline(hostID, cameOnline)
	_, _ = s.pool.Exec(r.Context(), `UPDATE hosts SET last_seen=now() WHERE id=$1`, hostID)

	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	deadline := time.Now().Add(min(time.Duration(max(wait, 0))*time.Second, longPollMaxWait))
	type item struct {
		ID       string          `json:"id"`
		Envelope json.RawMessage `json:"envelope"`
	}
	for {
		rows, err := s.pool.Query(r.Context(), `UPDATE agent_commands SET status='delivered', delivered_at=now()
			WHERE id IN (SELECT id FROM agent_commands WHERE host_id=$1 AND status='queued' AND expires_at > now()
			             ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 10)
			RETURNING id, envelope`, hostID)
		if err != nil {
			s.dbErr(w, err)
			return
		}
		out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
			var it item
			var env []byte
			err := row.Scan(&it.ID, &env)
			it.Envelope = env
			return it, err
		})
		if err != nil {
			s.dbErr(w, err)
			return
		}
		for _, it := range out {
			s.events.publish(Event{Type: "command.updated", HostID: hostID, Data: map[string]any{"id": it.ID, "status": "delivered"}})
		}
		if len(out) > 0 || time.Now().After(deadline) {
			writeJSON(w, http.StatusOK, out)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-wake: // a command was just queued for this host
		case <-time.After(min(5*time.Second, time.Until(deadline))): // safety net
		}
	}
}

func (s *Server) agentCommandResult(w http.ResponseWriter, r *http.Request) {
	hostID, _, ok := s.hostAuth(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var in struct {
		Status   string          `json:"status"`
		Output   string          `json:"output"`
		ExitCode *int            `json:"exit_code"`
		Error    string          `json:"error"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "invalid JSON")
		return
	}
	if in.Status != "running" && in.Status != "done" && in.Status != "failed" {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "status must be running|done|failed")
		return
	}
	// PostgreSQL jsonb rejects NUL (\u0000), which some Windows tools print (UTF-16 output)
	in.Output, in.Error = strings.ReplaceAll(in.Output, "\x00", ""), strings.ReplaceAll(in.Error, "\x00", "")
	truncated := false
	if len(in.Output) > maxOutputBytes {
		in.Output, truncated = in.Output[len(in.Output)-maxOutputBytes:], true
	}
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var typ, status string
		err := tx.QueryRow(r.Context(), `SELECT type, status FROM agent_commands WHERE id=$1 AND host_id=$2 FOR UPDATE`, id, hostID).Scan(&typ, &status)
		if err != nil {
			return audit.Entry{}, err
		}
		switch status {
		case "done", "failed", "cancelled":
			return audit.Entry{}, errStatus{"command already " + status}
		}
		if in.Status == "running" {
			_, err = tx.Exec(r.Context(), `UPDATE agent_commands SET status='running' WHERE id=$1`, id)
			return audit.Entry{}, err
		}
		res := map[string]any{"output": in.Output, "output_truncated": truncated, "exit_code": in.ExitCode, "error": in.Error}
		if len(in.Data) > 0 && string(in.Data) != "null" {
			res["data"] = in.Data
		}
		resJSON, _ := json.Marshal(res)
		if _, err := tx.Exec(r.Context(), `UPDATE agent_commands SET status=$2, result=$3::jsonb, finished_at=now() WHERE id=$1`,
			id, in.Status, string(resJSON)); err != nil {
			return audit.Entry{}, err
		}
		if in.Status == "failed" && typ == "rehandoff" {
			// the retry could not start on the machine: the job is aborted again, with the reason
			if _, err := tx.Exec(r.Context(), `UPDATE deployment_jobs SET status='aborted', phase='handoff', finished_at=now(), updated_at=now(),
				message='retry of the restart into WinPE did not start: ' || $2
				WHERE id=(SELECT args->>'job_id' FROM agent_commands WHERE id=$1) AND status='handoff'`, id, in.Error); err != nil {
				return audit.Entry{}, err
			}
		}
		if in.Status == "done" {
			switch typ {
			case "refresh_inventory":
				var inv inventory
				if json.Unmarshal(in.Data, &inv) == nil && inv.Hostname != "" {
					_, err = tx.Exec(r.Context(), `UPDATE hosts SET hostname=$2, vendor=$3, model=$4, firmware=$5, secure_boot=$6,
						tpm_version=$7, inventory=CASE WHEN $8::jsonb ? 'os' THEN $8::jsonb ELSE inventory END,
						inventory_at=CASE WHEN $8::jsonb ? 'os' THEN now() ELSE inventory_at END WHERE id=$1`,
						hostID, inv.Hostname, inv.Vendor, inv.Model, inv.Firmware, inv.SecureBoot, inv.TPMVersion, string(in.Data))
				}
			case "uninstall_agent":
				// The agent confirmed it is removing itself: revoke its credentials now.
				// The agent confirmed it is removing itself: revoke its credentials now (no tombstone: it is gone).
				_, err = tx.Exec(r.Context(), `UPDATE hosts SET retired_at=now(), retired_reason='agent uninstalled (command '||$2||')',
					dev_token_hash=NULL, removed_token_hash=NULL, removing_at=NULL, review_at=NULL WHERE id=$1`, hostID, id)
				if err == nil {
					_, err = tx.Exec(r.Context(), `UPDATE agent_commands SET status='cancelled', finished_at=now()
						WHERE host_id=$1 AND id<>$2 AND status IN ('queued','delivered')`, hostID, id)
				}
			}
			if err != nil {
				return audit.Entry{}, err
			}
		}
		return audit.Entry{Actor: "host:" + hostID, Action: "command.result", ObjectType: "command", ObjectID: id,
			IP: clientIP(r), Result: in.Status, Data: map[string]any{"type": typ, "exit_code": in.ExitCode}}, nil
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
	s.pres.touch(hostID)
	s.events.publish(Event{Type: "command.updated", HostID: hostID, Data: map[string]any{"id": id, "status": in.Status}})
	if in.Status == "done" {
		s.events.publish(Event{Type: "host.updated", HostID: hostID})
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": in.Status})
}

// ───────── script library (two-person) ─────────

func (s *Server) listScripts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT id, name, COALESCE(description,''), body, sha256, timeout_s, status,
		created_by, approved_by, created_at, approved_at FROM scripts ORDER BY status='retired', created_at DESC`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var id, name, desc, body, sum, status, by string
		var appr *string
		var timeout int
		var created time.Time
		var approvedAt *time.Time
		err := row.Scan(&id, &name, &desc, &body, &sum, &timeout, &status, &by, &appr, &created, &approvedAt)
		return map[string]any{"id": id, "name": name, "description": desc, "body": body, "sha256": sum, "timeout_s": timeout,
			"status": status, "created_by": by, "approved_by": appr, "created_at": created, "approved_at": approvedAt}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// scriptsClosed answers POST /v1/scripts: new script text is not accepted over the API any
// more (a stolen sign-in could otherwise save a script and run it on every machine).
func (s *Server) scriptsClosed(w http.ResponseWriter, r *http.Request) {
	_ = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		e := s.entry(r, "script.create_refused", "script", "", nil)
		e.Result = "denied"
		return e, nil
	})
	writeErr(w, http.StatusForbidden, "SWIM-API-403", "การบันทึกสคริปต์ใหม่ถูกปิดไว้เพื่อความปลอดภัย")
}

// createScript is kept for reference and tests; no route reaches it.
func (s *Server) createScript(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
		TimeoutS    int    `json:"timeout_s"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || strings.TrimSpace(in.Body) == "" || len(in.Body) > 65536 {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "name and body (≤ 64 KB) required")
		return
	}
	if in.TimeoutS == 0 {
		in.TimeoutS = 600
	}
	in.TimeoutS = max(10, min(in.TimeoutS, 3600))
	sum := sha256.Sum256([]byte(in.Body))
	id := ids.New("scr_")
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		// saved scripts are usable at once (no approval step); the body is in the audit entry too
		_, err := tx.Exec(r.Context(), `INSERT INTO scripts(id, name, description, body, sha256, timeout_s, created_by, status, approved_by, approved_at)
			VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,'approved',$7,now())`, id, in.Name, in.Description, in.Body, hex.EncodeToString(sum[:]), in.TimeoutS, adminFrom(r).UserID)
		return s.entry(r, "script.create", "script", id, map[string]any{"name": in.Name, "sha256": hex.EncodeToString(sum[:]), "body": in.Body}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "approved", "sha256": hex.EncodeToString(sum[:])})
}

func (s *Server) scriptAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	me := adminFrom(r)
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var status, createdBy string
		if err := tx.QueryRow(r.Context(), `SELECT status, created_by FROM scripts WHERE id=$1 FOR UPDATE`, id).Scan(&status, &createdBy); err != nil {
			return audit.Entry{}, err
		}
		switch action {
		case "approve":
			if status != "pending" {
				return audit.Entry{}, errStatus{"script is " + status}
			}
			if s.cfg.TwoPerson && createdBy == me.UserID {
				return audit.Entry{}, errStatus{"the author cannot approve their own script (2-person rule)"}
			}
			_, err := tx.Exec(r.Context(), `UPDATE scripts SET status='approved', approved_by=$2, approved_at=now() WHERE id=$1`, id, me.UserID)
			return s.entry(r, "script.approve", "script", id, nil), err
		case "retire":
			_, err := tx.Exec(r.Context(), `UPDATE scripts SET status='retired' WHERE id=$1`, id)
			return s.entry(r, "script.retire", "script", id, nil), err
		}
		return audit.Entry{}, errStatus{"unknown action"}
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
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "action": action})
}
