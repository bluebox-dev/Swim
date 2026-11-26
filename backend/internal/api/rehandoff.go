package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/pkg/manifest"
)

// rehandoffJob retries an install whose machine restarted into its old Windows instead of
// SWIM WinPE (aborted at "handoff", before the point of no return). The staged image,
// WinPE and answer file are still on the machine, so it only boots into WinPE again —
// no download and no new password.
//
//	POST /v1/jobs/{id}/rehandoff → {id, status: "handoff", command_id}
func (s *Server) rehandoffJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var hostID string
	var cmdID string
	actor := "user:" + adminFrom(r).Name
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		var status, phase string
		var ponr bool
		if err := tx.QueryRow(r.Context(), `SELECT host_id, status, COALESCE(phase,''), ponr_passed_at IS NOT NULL FROM deployment_jobs
			WHERE id=$1 FOR UPDATE`, id).Scan(&hostID, &status, &phase, &ponr); err != nil {
			return audit.Entry{}, err
		}
		if status != "aborted" || phase != "handoff" || ponr {
			return audit.Entry{}, errStatus{"only an install that came back into the old Windows (aborted at the restart into WinPE) can be retried this way"}
		}
		raw, _ := json.Marshal(manifest.RehandoffArgs{JobID: id})
		p, ce := s.planCommand(r.Context(), hostID, "rehandoff", raw, "", actor)
		if ce != nil {
			return audit.Entry{}, errStatus{ce.msg}
		}
		if _, err := tx.Exec(r.Context(), `UPDATE deployment_jobs SET status='handoff', phase='prepare', finished_at=NULL, updated_at=now(),
			message='retrying the restart into SWIM WinPE' WHERE id=$1`, id); err != nil {
			return audit.Entry{}, err
		}
		if err := insertCommand(r.Context(), tx, p, "", actor, nil); err != nil {
			return audit.Entry{}, err
		}
		cmdID = p.ID
		return s.entry(r, "job.rehandoff", "host", hostID, map[string]any{"job_id": id, "command_id": p.ID}), nil
	})
	var es errStatus
	if errors.As(err, &es) {
		writeErr(w, http.StatusConflict, "SWIM-API-409", es.msg)
		return
	}
	var pe interface{ SQLState() string }
	if errors.As(err, &pe) && pe.SQLState() == "23505" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "this machine already has another unfinished install")
		return
	}
	if err != nil {
		s.dbErr(w, err)
		return
	}
	s.pres.nudge(hostID)
	s.events.publish(Event{Type: "job.updated", HostID: hostID, Data: map[string]any{"id": id, "status": "handoff"}})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "handoff", "command_id": cmdID})
}
