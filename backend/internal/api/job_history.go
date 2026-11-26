package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
)

// Removing install jobs from a machine's history. Only finished jobs can go; the
// audit log keeps the record that the job existed and who removed it.

func deleteJobs(r *http.Request, tx pgx.Tx, where string, arg any) ([]string, error) {
	rows, err := tx.Query(r.Context(), `SELECT id FROM deployment_jobs WHERE `+where+` AND finished_at IS NOT NULL FOR UPDATE`, arg)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		return ids, err
	}
	for _, q := range []string{`DELETE FROM job_events WHERE job_id = ANY($1)`, `DELETE FROM job_summaries WHERE job_id = ANY($1)`,
		`DELETE FROM concurrency_leases WHERE job_id = ANY($1)`, `DELETE FROM deployment_jobs WHERE id = ANY($1)`} {
		if _, err := tx.Exec(r.Context(), q, ids); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// deleteJob removes one finished install job from the history.
//
//	DELETE /v1/jobs/{id}
func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var hostID string
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var status, image string
		var finished bool
		if err := tx.QueryRow(r.Context(), `SELECT j.host_id, j.status, j.finished_at IS NOT NULL, COALESCE(i.version,'')
			FROM deployment_jobs j LEFT JOIN images i ON i.id=j.image_id WHERE j.id=$1`, id).Scan(&hostID, &status, &finished, &image); err != nil {
			return audit.Entry{}, err
		}
		if !finished {
			return audit.Entry{}, errStatus{"this install is still in progress — cancel it or wait until it finishes"}
		}
		if _, err := deleteJobs(r, tx, "id=$1", id); err != nil {
			return audit.Entry{}, err
		}
		return s.entry(r, "job.delete", "host", hostID, map[string]any{"job_id": id, "was": status, "image": image}), nil
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
	s.releaseStagingIfUnused(r, hostID)
	s.events.publish(Event{Type: "job.updated", HostID: hostID, Data: map[string]any{"id": id, "status": "deleted"}})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// clearHostJobs removes every finished install job of one machine from the history.
//
//	DELETE /v1/hosts/{id}/jobs
func (s *Server) clearHostJobs(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("id")
	var ids []string
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var known bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM hosts WHERE id=$1)`, hostID).Scan(&known); err != nil {
			return audit.Entry{}, err
		}
		if !known {
			return audit.Entry{}, pgx.ErrNoRows
		}
		var err error
		ids, err = deleteJobs(r, tx, "host_id=$1", hostID)
		return s.entry(r, "jobs.clear", "host", hostID, map[string]any{"jobs_deleted": ids}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	s.releaseStagingIfUnused(r, hostID)
	s.events.publish(Event{Type: "job.updated", HostID: hostID})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": len(ids)})
}

// releaseStagingIfUnused: once no failed or aborted install is left in a machine's
// history (nothing to retry), files staged for those attempts are useless — ask the
// agent to delete its staging partition and give the space back to C:. Best effort:
// older agents and machines with an open job are skipped.
func (s *Server) releaseStagingIfUnused(r *http.Request, hostID string) {
	var keep bool
	if err := s.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM deployment_jobs WHERE host_id=$1
		AND (finished_at IS NULL OR status <> 'completed'))`, hostID).Scan(&keep); err != nil || keep {
		return
	}
	actor := "user:" + adminFrom(r).Name
	p, ce := s.planCommand(r.Context(), hostID, "release_staging", nil, "", actor)
	if ce != nil {
		return
	}
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		return s.entry(r, "command.release_staging", "host", hostID, map[string]any{"command_id": p.ID, "after": "install history removed"}),
			insertCommand(r.Context(), tx, p, "", actor, nil)
	})
	if err != nil {
		s.log.Error("release staging: queue command", "host", hostID, "err", err)
		return
	}
	s.pres.nudge(hostID)
	s.events.publish(Event{Type: "command.updated", HostID: hostID, Data: map[string]any{"id": p.ID, "status": "queued"}})
}
