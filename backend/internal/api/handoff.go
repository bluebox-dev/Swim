package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
)

// watchHandoff (heartbeat): a job waiting at "handoff" means the machine should be in SWIM
// WinPE. If its agent is reporting from Windows and the machine has restarted since the
// hand-off (by its uptime, which does not depend on the machine's clock), WinPE never ran:
// abort the job with the reason. Nothing on the disk was changed; the stored image stays.
func (s *Server) watchHandoff(ctx context.Context, hostID string) {
	var jobID string
	var handoffAt, invAt time.Time
	var uptime *float64
	err := s.pool.QueryRow(ctx, `SELECT j.id, j.updated_at, h.inventory_at, (h.inventory->>'uptime_s')::float8
		FROM deployment_jobs j JOIN hosts h ON h.id=j.host_id
		WHERE j.host_id=$1 AND j.status='handoff' AND j.finished_at IS NULL AND j.updated_at < now() - interval '5 minutes'`,
		hostID).Scan(&jobID, &handoffAt, &invAt, &uptime)
	if err != nil || uptime == nil || !invAt.After(handoffAt) {
		return
	}
	bootedAt := invAt.Add(-time.Duration(*uptime) * time.Second)
	if !bootedAt.After(handoffAt.Add(10 * time.Second)) {
		return // not restarted yet (the reboot is still pending)
	}
	msg := "the machine restarted into the old Windows instead of SWIM WinPE — nothing was changed on the disk; try the install again"
	data, _ := json.Marshal(map[string]any{"message": msg, "progress": 0, "restarted_at": bootedAt})
	err = s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(ctx, `UPDATE deployment_jobs SET status='aborted', phase='handoff', message=$2, finished_at=now(), updated_at=now()
			WHERE id=$1 AND status='handoff'`, jobID, msg)
		if err != nil || tag.RowsAffected() == 0 {
			return audit.Entry{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO job_events(job_id, seq, source, status, step, severity, data, at)
			VALUES ($1, COALESCE((SELECT max(seq)+1 FROM job_events WHERE job_id=$1),1), 'server', 'aborted', 'handoff', 'error', $2::jsonb, now())`,
			jobID, string(data)); err != nil {
			return audit.Entry{}, err
		}
		return audit.Entry{Actor: "system:watchdog", Action: "job.aborted", ObjectType: "host", ObjectID: hostID, Result: "aborted",
			Data: map[string]any{"job_id": jobID, "reason": "missed hand-off", "restarted_at": bootedAt}}, nil
	})
	if err != nil {
		s.log.Error("handoff watchdog", "job", jobID, "err", err)
		return
	}
	s.log.Warn("missed hand-off: job aborted", "job", jobID, "host", hostID)
	s.events.publish(Event{Type: "job.updated", HostID: hostID, Data: map[string]any{"id": jobID, "status": "aborted"}})
}
