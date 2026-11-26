package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Online history for the dashboard. The backend notes every 5 minutes how many managed
// machines hold their command channel open (the same "online" the UI shows).

const onlineSampleEvery = 5 * time.Minute

func (s *Server) sampleOnlineLoop() {
	time.Sleep(2 * time.Minute) // after a restart agents need a moment to reconnect
	for {
		s.sampleOnline(context.Background())
		time.Sleep(onlineSampleEvery)
	}
}

func (s *Server) sampleOnline(ctx context.Context) {
	if s.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT id FROM hosts WHERE retired_at IS NULL AND deleted_at IS NULL`)
	if err != nil {
		return
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return
	}
	online := 0
	for _, id := range ids {
		if s.pres.isOnline(id) {
			online++
		}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO online_samples(online, total) VALUES ($1,$2) ON CONFLICT DO NOTHING`, online, len(ids)); err != nil {
		s.log.Error("online sample", "err", err)
		return
	}
	_, _ = s.pool.Exec(ctx, `DELETE FROM online_samples WHERE at < now() - interval '30 days'`)
}

// statsOnline returns one point per hour, oldest first: the most machines seen online
// in that hour and the number of managed machines.
//
//	GET /v1/stats/online?hours=24   (1–720)
func (s *Server) statsOnline(w http.ResponseWriter, r *http.Request) {
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours < 1 || hours > 720 {
		hours = 24
	}
	rows, err := s.pool.Query(r.Context(), `SELECT date_trunc('hour', at) AS h, max(online), max(total) FROM online_samples
		WHERE at > now() - make_interval(hours => $1) GROUP BY h ORDER BY h`, hours)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	type point struct {
		At     time.Time `json:"at"`
		Online int       `json:"online"`
		Total  int       `json:"total"`
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (point, error) {
		var p point
		return p, row.Scan(&p.At, &p.Online, &p.Total)
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	if out == nil {
		out = []point{}
	}
	writeJSON(w, http.StatusOK, out)
}
