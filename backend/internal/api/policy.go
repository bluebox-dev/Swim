package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/pkg/manifest"
)

// Machine policy ("นโยบายเครื่อง"). For now one setting: install_lock — only SWIM,
// which runs as SYSTEM on the machine, can install software.
//
//	PUT  /v1/hosts/{id}/policy   {"install_lock": true}
//	POST /v1/hosts/policy        {"host_ids": [...] | "site_id": "...", "install_lock": true} → {updated, queued, skipped}
//
// The desired state is stored in hosts.policy and sent as a signed set_policy command.
// The agent reports the measured state in inventory.policy.install_lock; when the two
// disagree (a reimaged or re-enrolled machine, a command that expired) the server sends
// set_policy again from the heartbeat, at most every 10 minutes per machine.

const minPolicyAgent = "0.5.18"

// minSignOutAgent: first agent that reports who is still signed in with rights from before the
// lock (inventory policy.install_lock.pending_sign_out) and can sign them out (sign_out_stale).
const minSignOutAgent = "0.5.28"

// reimageAdmin is the local admin account SWIM created at the machine's last install;
// it stays an administrator under the install lock.
func (s *Server) reimageAdmin(ctx context.Context, hostID string) string {
	var name string
	_ = s.pool.QueryRow(ctx, `SELECT COALESCE(args->>'admin_user','') FROM agent_commands WHERE host_id=$1 AND type='reimage'
		AND status='done' ORDER BY created_at DESC LIMIT 1`, hostID).Scan(&name)
	return name
}

// lockSignOutS: how long users the lock demoted get to save their work before they are signed
// out, when an admin turns the lock on. The automatic re-send never signs anybody out.
const lockSignOutS = 60

func (s *Server) policyArgs(ctx context.Context, hostID string, lock bool, signOutS int) json.RawMessage {
	a := manifest.SetPolicyArgs{InstallLock: lock}
	if lock {
		a.SignOutS = signOutS
	}
	if n := s.reimageAdmin(ctx, hostID); n != "" {
		a.Keep = []string{n}
	}
	b, _ := json.Marshal(a)
	return b
}

// setHostPolicy stores the desired policy and queues set_policy; returns the command id
// ("" when the agent is too old — it updates itself and the heartbeat sends it then).
func (s *Server) setHostPolicy(r *http.Request, hostID string, lock bool, batch *string) (string, error) {
	actor := "user:" + adminFrom(r).Name
	p, ce := s.planCommand(r.Context(), hostID, "set_policy", s.policyArgs(r.Context(), hostID, lock, lockSignOutS), "", actor)
	var cmdID string
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE hosts SET policy = policy || jsonb_build_object('install_lock', $2::boolean)
			WHERE id=$1 AND retired_at IS NULL AND deleted_at IS NULL`, hostID, lock)
		if err != nil {
			return audit.Entry{}, err
		}
		if tag.RowsAffected() == 0 {
			return audit.Entry{}, pgx.ErrNoRows
		}
		data := map[string]any{"install_lock": lock}
		if ce == nil {
			if err := insertCommand(r.Context(), tx, p, "", actor, batch); err != nil {
				return audit.Entry{}, err
			}
			cmdID, data["command_id"] = p.ID, p.ID
		} else {
			data["deferred"] = ce.msg
		}
		return s.entry(r, "host.policy", "host", hostID, data), nil
	})
	if err != nil {
		return "", err
	}
	if cmdID != "" {
		s.pres.nudge(hostID)
	}
	s.events.publish(Event{Type: "host.updated", HostID: hostID})
	return cmdID, nil
}

func (s *Server) putHostPolicy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InstallLock   *bool   `json:"install_lock"`
		PrestageImage *string `json:"prestage_image_id"` // "" = keep no image on the machine
	}
	if !decode(w, r, &in) {
		return
	}
	if in.InstallLock == nil && in.PrestageImage == nil {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "install_lock and/or prestage_image_id required")
		return
	}
	id := r.PathValue("id")
	out := map[string]any{"id": id}
	if in.InstallLock != nil {
		cmdID, err := s.setHostPolicy(r, id, *in.InstallLock, nil)
		if err != nil {
			s.dbErr(w, err)
			return
		}
		out["command_id"] = cmdID
	}
	if in.PrestageImage != nil {
		cmdID, ce := s.setPrestage(r, id, *in.PrestageImage)
		if ce != nil {
			writeErr(w, ce.status, fmt.Sprintf("SWIM-API-%d", ce.status), ce.msg)
			return
		}
		out["prestage_command_id"] = cmdID
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) bulkHostPolicy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostIDs       []string `json:"host_ids"`
		SiteID        string   `json:"site_id"`
		InstallLock   *bool    `json:"install_lock"`
		PrestageImage *string  `json:"prestage_image_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if (in.InstallLock == nil && in.PrestageImage == nil) || (len(in.HostIDs) == 0) == (in.SiteID == "") {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "install_lock and/or prestage_image_id, and either host_ids or site_id required")
		return
	}
	ids := in.HostIDs
	if in.SiteID != "" {
		rows, err := s.pool.Query(r.Context(), `SELECT id FROM hosts WHERE site_id=$1 AND retired_at IS NULL AND deleted_at IS NULL`, in.SiteID)
		if err != nil {
			s.dbErr(w, err)
			return
		}
		if ids, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			s.dbErr(w, err)
			return
		}
	}
	updated, queued := 0, 0
	skipped := []map[string]string{}
	for _, id := range ids {
		var cmdID string
		if in.InstallLock != nil {
			c, err := s.setHostPolicy(r, id, *in.InstallLock, nil)
			if err != nil {
				msg := err.Error()
				if errors.Is(err, pgx.ErrNoRows) {
					msg = "no such machine"
				}
				skipped = append(skipped, map[string]string{"host_id": id, "reason": msg})
				continue
			}
			cmdID = c
		}
		if in.PrestageImage != nil {
			c, ce := s.setPrestage(r, id, *in.PrestageImage)
			if ce != nil {
				skipped = append(skipped, map[string]string{"host_id": id, "reason": ce.msg})
				continue
			}
			if c != "" {
				cmdID = c
			}
		}
		updated++
		if cmdID != "" {
			queued++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated, "queued": queued, "skipped": skipped})
}

var policySeen sync.Map // hostID → last reconcile attempt

// reconcilePolicy (heartbeat): re-send set_policy when the measured state disagrees with
// the desired one and nothing is pending.
func (s *Server) reconcilePolicy(ctx context.Context, hostID, agentVer string) {
	if agentVer == "" || versionLess(agentVer, minPolicyAgent) {
		return
	}
	if t, ok := policySeen.Load(hostID); ok && time.Since(t.(time.Time)) < 2*time.Minute {
		return
	}
	policySeen.Store(hostID, time.Now())
	var desired, measured *bool
	var reported bool
	if err := s.pool.QueryRow(ctx, `SELECT (policy->>'install_lock')::boolean,
		(inventory->'policy'->'install_lock'->>'desired')::boolean, inventory->'policy' IS NOT NULL
		FROM hosts WHERE id=$1`, hostID).Scan(&desired, &measured, &reported); err != nil || desired == nil || !reported {
		return // no desired policy set, or the agent has not reported policy yet
	}
	if measured != nil && *measured == *desired {
		return
	}
	var busy bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_jobs WHERE host_id=$1 AND finished_at IS NULL)
		OR EXISTS(SELECT 1 FROM agent_commands WHERE host_id=$1 AND type='set_policy'
			AND (status IN ('queued','delivered','running') OR created_at > now() - interval '10 minutes'))`, hostID).Scan(&busy); err != nil || busy {
		return
	}
	const actor = "system:policy"
	p, ce := s.planCommand(ctx, hostID, "set_policy", s.policyArgs(ctx, hostID, *desired, 0), "", actor)
	if ce != nil {
		return
	}
	err := s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
		return audit.Entry{Actor: actor, Action: "command.set_policy", ObjectType: "host", ObjectID: hostID, Result: "ok",
				Data: map[string]any{"command_id": p.ID, "install_lock": *desired, "reason": "machine state differs from policy"}},
			insertCommand(ctx, tx, p, "", actor, nil)
	})
	if err != nil {
		s.log.Error("policy reconcile", "host", hostID, "err", err)
		return
	}
	s.log.Info("policy re-sent", "host", hostID, "install_lock", *desired)
	s.pres.nudge(hostID)
}

// ───────── pre-staged image ─────────

const minPrestageAgent = "0.5.19"

func (s *Server) prestageArgs(ctx context.Context, imageID string) (json.RawMessage, *cmdErr) {
	a := manifest.PrestageArgs{}
	if imageID != "" {
		var status string
		if err := s.pool.QueryRow(ctx, `SELECT version, COALESCE(file_path,''), COALESCE(sha256,''), COALESCE(size_bytes,0), status
			FROM images WHERE id=$1`, imageID).Scan(&a.Image.Version, &a.Image.Path, &a.Image.SHA256, &a.Image.Size, &status); err != nil {
			return nil, &cmdErr{http.StatusNotFound, "no such image"}
		}
		if status != "published" || a.Image.Path == "" {
			return nil, &cmdErr{http.StatusConflict, "only a published image can be kept on machines"}
		}
		a.ImageID = imageID
	}
	b, _ := json.Marshal(a)
	return b, nil
}

// setPrestage stores which image the machine keeps (""= none) and sends prestage_image.
func (s *Server) setPrestage(r *http.Request, hostID, imageID string) (string, *cmdErr) {
	args, ce := s.prestageArgs(r.Context(), imageID)
	if ce != nil {
		return "", ce
	}
	actor := "user:" + adminFrom(r).Name
	p, pce := s.planCommand(r.Context(), hostID, "prestage_image", args, "", actor)
	var cmdID string
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE hosts SET policy = policy || jsonb_build_object('prestage_image_id', $2::text)
			WHERE id=$1 AND retired_at IS NULL AND deleted_at IS NULL`, hostID, imageID)
		if err != nil {
			return audit.Entry{}, err
		}
		if tag.RowsAffected() == 0 {
			return audit.Entry{}, pgx.ErrNoRows
		}
		data := map[string]any{"prestage_image_id": imageID}
		if pce == nil {
			if err := insertCommand(r.Context(), tx, p, "", actor, nil); err != nil {
				return audit.Entry{}, err
			}
			cmdID, data["command_id"] = p.ID, p.ID
		} else {
			data["deferred"] = pce.msg
		}
		return s.entry(r, "host.prestage", "host", hostID, data), nil
	})
	if err != nil {
		return "", dbCmdErr(err)
	}
	if cmdID != "" {
		s.pres.nudge(hostID)
	}
	s.events.publish(Event{Type: "host.updated", HostID: hostID})
	return cmdID, nil
}

var prestageSeen sync.Map

// reconcilePrestage (heartbeat): (re)send prestage_image when the machine does not hold the
// chosen image and is not downloading it.
func (s *Server) reconcilePrestage(ctx context.Context, hostID, agentVer string) {
	if agentVer == "" || versionLess(agentVer, minPrestageAgent) {
		return
	}
	if t, ok := prestageSeen.Load(hostID); ok && time.Since(t.(time.Time)) < 2*time.Minute {
		return
	}
	prestageSeen.Store(hostID, time.Now())
	var desired *string
	var haveID, state string
	if err := s.pool.QueryRow(ctx, `SELECT policy->>'prestage_image_id', COALESCE(inventory->'prestage'->>'image_id',''),
		COALESCE(inventory->'prestage'->>'state','') FROM hosts WHERE id=$1`, hostID).Scan(&desired, &haveID, &state); err != nil || desired == nil {
		return
	}
	switch {
	case state == "": // agent has not reported pre-staging yet
		return
	case *desired == "" && state == "none":
		return
	case *desired != "" && haveID == *desired && (state == "ready" || state == "downloading"):
		return
	}
	var busy bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_jobs WHERE host_id=$1 AND finished_at IS NULL)
		OR EXISTS(SELECT 1 FROM agent_commands WHERE host_id=$1 AND type='prestage_image'
			AND (status IN ('queued','delivered','running') OR created_at > now() - interval '30 minutes'))`, hostID).Scan(&busy); err != nil || busy {
		return
	}
	// bandwidth: start new background downloads only while few machines are downloading,
	// and (optionally) only inside the configured hours
	if *desired != "" && !s.prestageSlotFree(ctx) {
		return
	}
	args, ce := s.prestageArgs(ctx, *desired)
	if ce != nil {
		return
	}
	const actor = "system:policy"
	p, ce := s.planCommand(ctx, hostID, "prestage_image", args, "", actor)
	if ce != nil {
		return
	}
	if err := s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
		return audit.Entry{Actor: actor, Action: "command.prestage_image", ObjectType: "host", ObjectID: hostID, Result: "ok",
				Data: map[string]any{"command_id": p.ID, "prestage_image_id": *desired, "reason": "machine does not hold the chosen image"}},
			insertCommand(ctx, tx, p, "", actor, nil)
	}); err != nil {
		s.log.Error("prestage reconcile", "host", hostID, "err", err)
		return
	}
	s.log.Info("prestage sent", "host", hostID, "image", *desired)
	s.pres.nudge(hostID)
}

// prestageSlotFree: fewer than SWIM_MAX_PRESTAGE (default 2) machines are downloading a
// pre-staged image or installing, and the time is inside SWIM_PRESTAGE_HOURS ("18-7" =
// 18:00 to 07:00 Thailand time; empty = any time). Installs always take precedence.
func (s *Server) prestageSlotFree(ctx context.Context) bool {
	max := 2
	if v, err := strconv.Atoi(os.Getenv("SWIM_MAX_PRESTAGE")); err == nil && v > 0 {
		max = v
	}
	if win := os.Getenv("SWIM_PRESTAGE_HOURS"); win != "" {
		var from, to int
		if _, err := fmt.Sscanf(win, "%d-%d", &from, &to); err == nil {
			h := time.Now().In(thaiTZ).Hour()
			in := (from <= to && h >= from && h < to) || (from > to && (h >= from || h < to))
			if !in {
				return false
			}
		}
	}
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM hosts WHERE retired_at IS NULL AND inventory->'prestage'->>'state'='downloading')
		+ (SELECT count(*) FROM agent_commands WHERE type='prestage_image' AND status IN ('queued','delivered'))
		+ (SELECT count(*) FROM deployment_jobs WHERE finished_at IS NULL AND phase IN ('staging','downloading','preflight'))`).Scan(&n)
	return n < max
}
