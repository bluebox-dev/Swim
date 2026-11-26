package api

// Removing machines and agents that come back.
//
//   - Remove an ONLINE machine: a signed uninstall_agent is queued; the host is retired
//     when the agent confirms (command result), or after removalGrace if it never does.
//   - Remove an OFFLINE machine: retired at once; its token hash is kept as a tombstone.
//   - An agent that later presents a tombstoned token is NOT let back in: it gets one signed
//     uninstall_agent (the only thing it may fetch), removes itself, and no machine
//     reappears. Only the audit log records it.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/backend/internal/ids"
	"swim/pkg/manifest"
)

const removalGrace = 15 * time.Minute

// reviewAllowed are the only commands for a machine in pending review.
var reviewAllowed = map[string]bool{"ping": true, "refresh_inventory": true, "collect_logs": true, "uninstall_agent": true}

// retireTombstone retires a host and keeps its token as a tombstone.
const retireTombstone = `UPDATE hosts SET retired_at=now(), retired_reason=$2,
	removed_token_hash=COALESCE(dev_token_hash, removed_token_hash), dev_token_hash=NULL, removing_at=NULL, review_at=NULL
	WHERE id=$1 AND retired_at IS NULL`

// removeHost (POST /v1/hosts/{id}/retire): online → uninstall the agent first; offline → retire now.
func (s *Server) removeHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) // body is optional
	actor := "user:" + adminFrom(r).Name
	if s.pres.isOnline(id) {
		if p, ce := s.planCommand(r.Context(), id, "uninstall_agent", json.RawMessage(`{}`), "machine removed from SWIM", actor); ce == nil {
			err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
				if err := insertCommand(r.Context(), tx, p, "machine removed from SWIM", actor, nil); err != nil {
					return audit.Entry{}, err
				}
				if _, err := tx.Exec(r.Context(), `UPDATE hosts SET removing_at=now() WHERE id=$1`, id); err != nil {
					return audit.Entry{}, err
				}
				return s.entry(r, "host.remove", "host", id, map[string]string{"mode": "uninstall agent first"}), nil
			})
			if err != nil {
				s.dbErr(w, err)
				return
			}
			s.pres.nudge(id)
			s.events.publish(Event{Type: "host.updated", HostID: id})
			writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "uninstalling"})
			return
		}
	}
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		reason := in.Reason
		if reason == "" {
			reason = "removed by " + adminFrom(r).Name + " (agent offline — kept as a tombstone)"
		}
		tag, err := tx.Exec(r.Context(), retireTombstone, id, reason)
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE agent_commands SET status='cancelled', finished_at=now()
				WHERE host_id=$1 AND status IN ('queued','delivered')`, id)
		}
		return s.entry(r, "host.retire", "host", id, map[string]string{"mode": "offline, tombstone kept"}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	s.pres.goodbye(id)
	s.events.publish(Event{Type: "host.updated", HostID: id})
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "retired"})
}

// sweepRemovals finishes removals whose agent never confirmed the uninstall.
func (s *Server) sweepRemovals(ctx context.Context) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM hosts WHERE removing_at < $1 AND retired_at IS NULL`, time.Now().Add(-removalGrace))
	if err != nil {
		return
	}
	ids, _ := pgx.CollectRows(rows, pgx.RowTo[string])
	for _, id := range ids {
		if _, err := s.pool.Exec(ctx, retireTombstone, id, "removed (agent did not confirm the uninstall — kept as a tombstone)"); err == nil {
			_, _ = s.pool.Exec(ctx, `UPDATE agent_commands SET status='cancelled', finished_at=now() WHERE host_id=$1 AND status IN ('queued','delivered')`, id)
			s.events.publish(Event{Type: "host.updated", HostID: id})
		}
	}
}

// tombstoned: an agent presented the token of a machine that was removed. Make sure it
// has exactly one signed uninstall_agent waiting; it may fetch commands and report the
// result, nothing else. The machine stays removed (and hidden if it was deleted).
func (s *Server) tombstoned(r *http.Request, tokenHash []byte) (id, uuid string, ok bool) {
	if err := s.pool.QueryRow(r.Context(), `SELECT id, COALESCE(smbios_uuid, removed_smbios_uuid)::text FROM hosts
		WHERE removed_token_hash=$1 AND dev_token_hash IS NULL`, tokenHash).Scan(&id, &uuid); err != nil {
		return "", "", false
	}
	var open int
	_ = s.pool.QueryRow(r.Context(), `SELECT count(*) FROM agent_commands WHERE host_id=$1 AND type='uninstall_agent'
		AND status IN ('queued','delivered','running') AND expires_at > now()`, id).Scan(&open)
	if open > 0 {
		return id, uuid, true
	}
	now := time.Now().UTC().Truncate(time.Second)
	cmd := manifest.Command{Schema: 1, Kind: "command", CommandID: ids.New("cmd_"), Type: "uninstall_agent", Args: json.RawMessage(`{}`),
		Actor: "system:removed", IssuedAt: now, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(commandTTL)}
	cmd.Host.HostID, cmd.Host.SmbiosUUID = id, uuid
	payload, _ := json.Marshal(cmd)
	env, _ := json.Marshal(manifest.Sign(s.key.Priv, s.key.Kid, payload))
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO agent_commands(id, host_id, type, args, envelope, reason, created_by, expires_at)
			VALUES ($1,$2,'uninstall_agent','{}'::jsonb,$3::jsonb,'agent of a removed machine came back','system:removed',$4)`,
			cmd.CommandID, id, string(env), cmd.NotAfter)
		return audit.Entry{Actor: "host:" + id, Action: "host.removed_agent_returned", ObjectType: "host", ObjectID: id, IP: clientIP(r),
			Result: "uninstall sent", Data: map[string]string{"command_id": cmd.CommandID}}, err
	})
	if err != nil {
		s.log.Error("tombstone uninstall", "host", id, "err", err)
		return "", "", false
	}
	s.log.Warn("agent of a removed machine came back — uninstall sent", "host", id)
	return id, uuid, true
}

// ───────── live status of one installer (Add Machine page) ─────────

type refusal struct {
	Msg, Hostname string
	At            time.Time
}

// enrollRefusals remembers the last refused enrollment per token (in memory: it only
// feeds the live status card while the admin watches the install).
var enrollRefusals sync.Map

// enrollTokenStatus (GET /v1/enrollment-tokens/{id}): has the installer been used, and by which machine?
func (s *Server) enrollTokenStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var uses int
	var maxUses *int
	var expires time.Time
	if err := s.pool.QueryRow(r.Context(), `SELECT uses, max_uses, expires_at FROM enrollment_tokens WHERE id=$1`, id).Scan(&uses, &maxUses, &expires); err != nil {
		s.dbErr(w, err)
		return
	}
	out := map[string]any{"id": id, "uses": uses, "max_uses": maxUses, "active": time.Now().Before(expires) && (maxUses == nil || uses < *maxUses)}
	var hostID, hostname string
	var online bool
	if err := s.pool.QueryRow(r.Context(), `SELECT h.id, COALESCE(h.hostname,'') FROM audit_log a JOIN hosts h ON h.id=a.object_id
		WHERE a.actor='enroll:'||$1 AND a.action='host.enroll' ORDER BY a.at DESC LIMIT 1`, id).Scan(&hostID, &hostname); err == nil {
		online = s.pres.isOnline(hostID)
		out["host"] = map[string]any{"id": hostID, "hostname": hostname, "online": online}
	}
	if v, ok := enrollRefusals.Load(id); ok {
		f := v.(refusal)
		out["refused"] = map[string]any{"message": f.Msg, "hostname": f.Hostname, "at": f.At}
	}
	writeJSON(w, http.StatusOK, out)
}

// sweepStaleCommands closes commands an agent picked up but never answered (the agent or
// machine restarted mid-run). A command counts as lost once its validity window and the
// script's own timeout have both passed, plus a grace period. Reimage commands are left
// to their deployment job, which tracks them.
func (s *Server) sweepStaleCommands(ctx context.Context) {
	rows, err := s.pool.Query(ctx, `UPDATE agent_commands SET status='expired', finished_at=now(),
		result=COALESCE(result,'{}'::jsonb) || jsonb_build_object('error', 'เครื่องไม่ได้ส่งผลกลับ (agent อาจรีสตาร์ทระหว่างทำงาน)')
		WHERE status IN ('delivered','running') AND type <> 'reimage'
		  AND expires_at + make_interval(secs => COALESCE((args->>'timeout_s')::int, 0)) + interval '30 minutes' < now()
		RETURNING id, host_id`)
	if err != nil {
		return
	}
	type closed struct{ id, host string }
	list, _ := pgx.CollectRows(rows, func(r pgx.CollectableRow) (closed, error) {
		var c closed
		return c, r.Scan(&c.id, &c.host)
	})
	for _, c := range list {
		s.log.Warn("command never answered — marked expired", "command", c.id, "host", c.host)
		s.events.publish(Event{Type: "command.updated", HostID: c.host, Data: map[string]any{"id": c.id, "status": "expired"}})
	}
}
