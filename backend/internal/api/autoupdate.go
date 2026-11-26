package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/pkg/manifest"
)

// Automatic agent updates: when a machine checks in with an agent older than the build
// published on the server, the server queues the same signed update_agent command an
// admin would send by hand. The agent verifies the download's SHA-256 and rolls back by
// itself if the new build does not start. One attempt per machine and version every
// autoUpdateRetry, never while an install job or another update is open. Every attempt
// is in the audit log as actor "system:auto-update".
// SWIM_AGENT_AUTO_UPDATE=off disables it.

const autoUpdateRetry = 6 * time.Hour

var autoUpdateSeen sync.Map // hostID+"|"+version → time of the last look (in-memory throttle)

func (s *Server) autoUpdateAgent(ctx context.Context, hostID, agentVer string) {
	if agentVer == "" || os.Getenv("SWIM_AGENT_AUTO_UPDATE") == "off" {
		return
	}
	var latest manifest.UpdateArgs
	b, err := os.ReadFile(filepath.Join(s.cfg.StorageDir, "agent", "latest.json"))
	if err != nil || json.Unmarshal(b, &latest) != nil || latest.Version == "" || !versionLess(agentVer, latest.Version) {
		return
	}
	key := hostID + "|" + latest.Version
	if t, ok := autoUpdateSeen.Load(key); ok && time.Since(t.(time.Time)) < 2*time.Minute {
		return
	}
	autoUpdateSeen.Store(key, time.Now())
	var blocked bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_jobs WHERE host_id=$1 AND finished_at IS NULL)
		OR EXISTS(SELECT 1 FROM agent_commands WHERE host_id=$1 AND type='update_agent'
			AND (status IN ('queued','delivered','running') OR (args->>'version'=$2 AND created_at > now() - make_interval(secs => $3))))`,
		hostID, latest.Version, autoUpdateRetry.Seconds()).Scan(&blocked); err != nil || blocked {
		return
	}
	const actor = "system:auto-update"
	p, ce := s.planCommand(ctx, hostID, "update_agent", nil, "", actor)
	if ce != nil {
		return
	}
	err = s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
		return audit.Entry{Actor: actor, Action: "command.update_agent", ObjectType: "host", ObjectID: hostID, Result: "ok",
			Data: map[string]any{"command_id": p.ID, "args": p.AuditArgs, "automatic": true}}, insertCommand(ctx, tx, p, "", actor, nil)
	})
	if err != nil {
		s.log.Error("auto-update: queue command", "host", hostID, "err", err)
		return
	}
	s.log.Info("auto-update queued", "host", hostID, "from", agentVer, "to", latest.Version)
	s.pres.nudge(hostID)
	s.events.publish(Event{Type: "command.updated", HostID: hostID, Data: map[string]any{"id": p.ID, "status": "queued"}})
}
