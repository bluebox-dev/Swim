package api

// Scheduled work (ตารางงาน): reimage, reboot, wake, update the agent or shut down a set
// of machines at a time, once or repeating daily/weekly (Thailand time).
//
//	POST   /v1/schedules                                 create
//	GET    /v1/schedules/occurrences?from=&to=           occurrences in a window (≤ 62 days)
//	DELETE /v1/schedules/{id}                            cancel the schedule
//	DELETE /v1/schedules/{id}/occurrences/{occurrence}   skip one occurrence (key = unix seconds)
//	POST   /v1/schedules/{id}/run-now                    run once now
//
// At run time, targets are resolved (a site's current machines), machines that are off are
// woken first and given up to 5 minutes to come online, then each one gets the same signed
// command or reimage job an admin would send by hand. Per-machine results are kept in
// schedule_runs.result. The deploy admin password is stored only encrypted (AES-256-GCM
// with a key file in the server's key folder) and removed after a one-time schedule ran.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/backend/internal/ids"
	"swim/pkg/manifest"
)

var thaiTZ = time.FixedZone("ICT", 7*3600) // Thailand has no daylight saving

type scheduleRepeat struct {
	Freq string `json:"freq"`           // none | daily | weekly
	Days []int  `json:"days,omitempty"` // weekly: 1 = Monday … 7 = Sunday
}

type scheduleTargets struct {
	HostIDs []string `json:"host_ids,omitempty"`
	SiteID  string   `json:"site_id,omitempty"`
}

type schedule struct {
	ID        string
	Kind      string
	Title     string
	StartAt   time.Time
	Repeat    scheduleRepeat
	Targets   scheduleTargets
	Args      json.RawMessage
	Secret    []byte
	Skips     []string
	CreatedBy string
	Cancelled *time.Time
}

const scheduleCols = `id, kind, title, start_at, repeat, targets, args, secret, skips, created_by, cancelled_at`

func scanSchedule(row pgx.Row) (schedule, error) {
	var sc schedule
	var rep, tg, sk []byte
	err := row.Scan(&sc.ID, &sc.Kind, &sc.Title, &sc.StartAt, &rep, &tg, &sc.Args, &sc.Secret, &sk, &sc.CreatedBy, &sc.Cancelled)
	_ = json.Unmarshal(rep, &sc.Repeat)
	_ = json.Unmarshal(tg, &sc.Targets)
	_ = json.Unmarshal(sk, &sc.Skips)
	return sc, err
}

// occurrences between from and to (inclusive of from), in time order.
func (sc schedule) occurrences(from, to time.Time) []time.Time {
	start := sc.StartAt.In(thaiTZ)
	var out []time.Time
	switch sc.Repeat.Freq {
	case "daily", "weekly":
		day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, thaiTZ)
		if f := from.In(thaiTZ); f.After(day) {
			day = time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, thaiTZ).AddDate(0, 0, -1)
		}
		tod := time.Duration(start.Hour())*time.Hour + time.Duration(start.Minute())*time.Minute + time.Duration(start.Second())*time.Second
		for i := 0; i < 400; i++ {
			at := day.Add(tod)
			if at.After(to) {
				break
			}
			wd := int(at.Weekday())
			if wd == 0 {
				wd = 7
			}
			ok := sc.Repeat.Freq == "daily"
			for _, d := range sc.Repeat.Days {
				ok = ok || d == wd
			}
			if ok && !at.Before(start) && !at.Before(from) {
				out = append(out, at)
			}
			day = day.AddDate(0, 0, 1)
		}
	default:
		if !start.Before(from) && !start.After(to) {
			out = append(out, start)
		}
	}
	return out
}

func occKey(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func (sc schedule) skipped(t time.Time) bool {
	k := occKey(t)
	for _, s := range sc.Skips {
		if s == k {
			return true
		}
	}
	return false
}

// ───────── secret (deploy admin password) ─────────

func (s *Server) scheduleKey() ([]byte, error) {
	dir := os.Getenv("SWIM_KEY_DIR")
	if dir == "" {
		dir = "/keys"
	}
	p := filepath.Join(dir, "schedule-secret.key")
	if b, err := os.ReadFile(p); err == nil && len(b) == 32 {
		return b, nil
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if b, rerr := os.ReadFile(p); rerr == nil && len(b) == 32 { // created concurrently
			return b, nil
		}
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(k); err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Server) sealSecret(plain string) ([]byte, error) {
	k, err := s.scheduleKey()
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	nonce := make([]byte, g.NonceSize())
	_, _ = rand.Read(nonce)
	return g.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (s *Server) openSecret(b []byte) (string, error) {
	k, err := s.scheduleKey()
	if err != nil {
		return "", err
	}
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	if len(b) < g.NonceSize() {
		return "", errors.New("stored password is damaged")
	}
	p, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	return string(p), err
}

// ───────── API ─────────

type deployScheduleArgs struct {
	ImageID       string               `json:"image_id"`
	Disk          manifest.DiskProfile `json:"disk"`
	AdminUser     string               `json:"admin_user"`
	AdminPassword string               `json:"admin_password,omitempty"`
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind    string          `json:"kind"`
		Title   string          `json:"title"`
		StartAt time.Time       `json:"start_at"`
		Repeat  scheduleRepeat  `json:"repeat"`
		Targets scheduleTargets `json:"targets"`
		Args    json.RawMessage `json:"args"`
	}
	if !decode(w, r, &in) {
		return
	}
	bad := func(m string) { writeErr(w, http.StatusBadRequest, "SWIM-API-400", m) }
	switch in.Kind {
	case "deploy", "reboot", "wake", "update_agent", "shutdown":
	default:
		bad("kind must be deploy, reboot, wake, update_agent or shutdown")
		return
	}
	if in.Repeat.Freq == "" {
		in.Repeat.Freq = "none"
	}
	if in.Repeat.Freq != "none" && in.Repeat.Freq != "daily" && in.Repeat.Freq != "weekly" {
		bad("repeat.freq must be none, daily or weekly")
		return
	}
	if in.Repeat.Freq == "weekly" {
		if len(in.Repeat.Days) == 0 {
			bad("weekly repeat needs at least one day (1 = Monday … 7 = Sunday)")
			return
		}
		for _, d := range in.Repeat.Days {
			if d < 1 || d > 7 {
				bad("repeat.days: 1 = Monday … 7 = Sunday")
				return
			}
		}
	}
	if in.StartAt.IsZero() {
		bad("start_at required")
		return
	}
	if in.Repeat.Freq == "none" && in.StartAt.Before(time.Now().Add(-time.Minute)) {
		bad("start_at is in the past")
		return
	}
	if (len(in.Targets.HostIDs) == 0) == (in.Targets.SiteID == "") {
		bad("targets: host_ids or site_id")
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		in.Title = map[string]string{"deploy": "ล้างเครื่องและติดตั้ง Windows", "reboot": "รีบูตเครื่อง", "wake": "เปิดเครื่อง",
			"update_agent": "อัปเดต Agent", "shutdown": "ปิดเครื่อง"}[in.Kind]
	}
	args := json.RawMessage(`{}`)
	var secret []byte
	if in.Kind == "deploy" {
		var d deployScheduleArgs
		if len(in.Args) == 0 || json.Unmarshal(in.Args, &d) != nil || d.ImageID == "" {
			bad("deploy: args.image_id required")
			return
		}
		if d.Disk.Mode == "" {
			d.Disk.Mode = "refresh"
		}
		if strings.TrimSpace(d.AdminUser) == "" || len(d.AdminPassword) < 8 {
			bad("local administrator name and a password of at least 8 characters are required")
			return
		}
		var status string
		if err := s.pool.QueryRow(r.Context(), `SELECT status FROM images WHERE id=$1`, d.ImageID).Scan(&status); err != nil || status != "published" {
			writeErr(w, http.StatusConflict, "SWIM-API-409", "image must exist and be published")
			return
		}
		var err error
		if secret, err = s.sealSecret(d.AdminPassword); err != nil {
			writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "cannot protect the password: "+err.Error())
			return
		}
		d.AdminPassword = ""
		args, _ = json.Marshal(d)
	}
	id := ids.New("sch_")
	rep, _ := json.Marshal(in.Repeat)
	tg, _ := json.Marshal(in.Targets)
	me := adminFrom(r).Name
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO schedules(id, kind, title, start_at, repeat, targets, args, secret, created_by)
			VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7::jsonb,$8,$9)`, id, in.Kind, in.Title, in.StartAt, string(rep), string(tg), string(args), secret, me)
		return s.entry(r, "schedule.create", "schedule", id, map[string]any{"kind": in.Kind, "title": in.Title, "start_at": in.StartAt,
			"repeat": in.Repeat, "targets": in.Targets, "args": json.RawMessage(args)}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	s.events.publish(Event{Type: "schedule.updated", Data: map[string]any{"id": id}})
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) cancelSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE schedules SET cancelled_at=now(), cancelled_by=$2, secret=NULL WHERE id=$1 AND cancelled_at IS NULL`,
			id, adminFrom(r).Name)
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
		return s.entry(r, "schedule.cancel", "schedule", id, nil), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	s.events.publish(Event{Type: "schedule.updated", Data: map[string]any{"id": id}})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "cancelled": true})
}

func (s *Server) skipOccurrence(w http.ResponseWriter, r *http.Request) {
	id, occ := r.PathValue("id"), r.PathValue("occurrence")
	if _, err := strconv.ParseInt(occ, 10, 64); err != nil {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "occurrence: unix seconds as returned by occurrences")
		return
	}
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE schedules SET skips = skips || to_jsonb($2::text)
			WHERE id=$1 AND cancelled_at IS NULL AND NOT skips ? $2`, id, occ)
		if err == nil && tag.RowsAffected() == 0 {
			var exists bool
			_ = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM schedules WHERE id=$1 AND cancelled_at IS NULL)`, id).Scan(&exists)
			if !exists {
				err = pgx.ErrNoRows
			}
		}
		return s.entry(r, "schedule.skip", "schedule", id, map[string]any{"occurrence": occ}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	s.events.publish(Event{Type: "schedule.updated", Data: map[string]any{"id": id}})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "skipped": occ})
}

func (s *Server) targetLabel(ctx context.Context, t scheduleTargets) (string, int) {
	if t.SiteID != "" {
		var name string
		var n int
		_ = s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT name FROM sites WHERE id=$1),$1),
			(SELECT count(*) FROM hosts WHERE site_id=$1 AND retired_at IS NULL AND deleted_at IS NULL)`, t.SiteID).Scan(&name, &n)
		return fmt.Sprintf("ห้อง %s, %d เครื่อง", name, n), n
	}
	if len(t.HostIDs) == 1 {
		var name string
		_ = s.pool.QueryRow(ctx, `SELECT COALESCE(hostname, id) FROM hosts WHERE id=$1`, t.HostIDs[0]).Scan(&name)
		if name != "" {
			return name, 1
		}
	}
	return fmt.Sprintf("%d เครื่อง", len(t.HostIDs)), len(t.HostIDs)
}

var thaiDays = []string{"", "จันทร์", "อังคาร", "พุธ", "พฤหัสบดี", "ศุกร์", "เสาร์", "อาทิตย์"}

func repeatLabel(r scheduleRepeat) string {
	switch r.Freq {
	case "daily":
		return "ทุกวัน"
	case "weekly":
		var d []string
		for _, x := range r.Days {
			d = append(d, thaiDays[x])
		}
		return "ทุกวัน" + strings.Join(d, ", ")
	}
	return "ครั้งเดียว"
}

func (s *Server) listOccurrences(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > 62*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "from and to (RFC 3339), at most 62 days apart")
		return
	}
	rows, err := s.pool.Query(r.Context(), `SELECT `+scheduleCols+` FROM schedules WHERE start_at <= $1 ORDER BY start_at`, to)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	scheds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (schedule, error) { return scanSchedule(row) })
	if err != nil {
		s.dbErr(w, err)
		return
	}
	type runInfo struct {
		ID, Status string
		Result     json.RawMessage
	}
	runs := map[string]runInfo{}
	rrows, err := s.pool.Query(r.Context(), `SELECT schedule_id, occurrence, id, status, COALESCE(result,'{}'::jsonb) FROM schedule_runs
		WHERE occurrence >= $1 AND occurrence <= $2`, from, to)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	var runAts = map[string][]time.Time{}
	for rrows.Next() {
		var sid string
		var at time.Time
		var ri runInfo
		if err := rrows.Scan(&sid, &at, &ri.ID, &ri.Status, &ri.Result); err == nil {
			runs[sid+"|"+occKey(at)] = ri
			runAts[sid] = append(runAts[sid], at)
		}
	}
	rrows.Close()
	out := []map[string]any{}
	now := time.Now()
	for _, sc := range scheds {
		label, n := s.targetLabel(r.Context(), sc.Targets)
		occs := sc.occurrences(from, to)
		// runs started by run-now are not on the regular pattern: list them too
		seen := map[string]bool{}
		for _, t := range occs {
			seen[occKey(t)] = true
		}
		for _, t := range runAts[sc.ID] {
			if !seen[occKey(t)] {
				occs = append(occs, t)
			}
		}
		for _, at := range occs {
			key := occKey(at)
			it := map[string]any{"id": sc.ID + ":" + key, "schedule_id": sc.ID, "occurrence": key, "kind": sc.Kind, "title": sc.Title,
				"at": at.UTC(), "duration_min": map[bool]int{true: 45, false: 5}[sc.Kind == "deploy"], "target_label": label,
				"targets_count": n, "repeat_label": repeatLabel(sc.Repeat), "created_by": sc.CreatedBy}
			if ri, ok := runs[sc.ID+"|"+key]; ok {
				it["run_id"] = ri.ID
				st, res := s.liveRun(r.Context(), ri.Status, ri.Result)
				it["status"], it["detail"] = st, res
				it["result"] = map[string]any{"ok": res["ok"], "fail": res["fail"], "running": res["running"], "progress": res["progress"]}
			} else {
				switch {
				case sc.skipped(at) || (sc.Cancelled != nil && at.After(*sc.Cancelled)):
					if sc.Cancelled != nil && !sc.skipped(at) && at.After(now) {
						continue // future occurrences of a cancelled schedule are simply gone
					}
					it["status"] = "cancelled"
				case at.Before(now.Add(-15 * time.Minute)):
					it["status"] = "missed" // the server was not running at that time
				default:
					it["status"] = "scheduled"
				}
			}
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["at"].(time.Time).Before(out[j]["at"].(time.Time)) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) runScheduleNow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sc, err := scanSchedule(s.pool.QueryRow(r.Context(), `SELECT `+scheduleCols+` FROM schedules WHERE id=$1`, id))
	if err != nil {
		s.dbErr(w, err)
		return
	}
	if sc.Cancelled != nil {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "this schedule was cancelled")
		return
	}
	runID, ok := s.claimRun(context.Background(), sc, time.Now().Truncate(time.Second))
	if !ok {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "a run of this schedule has just started")
		return
	}
	_ = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		return s.entry(r, "schedule.run_now", "schedule", id, map[string]any{"run_id": runID}), nil
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID})
}

// ───────── executor ─────────

func (s *Server) scheduleLoop() {
	time.Sleep(30 * time.Second)
	for {
		s.runDueSchedules(context.Background())
		time.Sleep(30 * time.Second)
	}
}

func (s *Server) runDueSchedules(ctx context.Context) {
	if s.pool == nil {
		return
	}
	now := time.Now()
	rows, err := s.pool.Query(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE cancelled_at IS NULL AND start_at <= $1`, now)
	if err != nil {
		return
	}
	scheds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (schedule, error) { return scanSchedule(row) })
	if err != nil {
		return
	}
	for _, sc := range scheds {
		for _, at := range sc.occurrences(now.Add(-15*time.Minute), now) { // 15 min grace after a restart
			if !sc.skipped(at) {
				s.claimRun(ctx, sc, at)
			}
		}
	}
}

// claimRun records the run (once per occurrence, even with several backends) and starts it.
func (s *Server) claimRun(ctx context.Context, sc schedule, at time.Time) (string, bool) {
	runID := ids.New("run_")
	tag, err := s.pool.Exec(ctx, `INSERT INTO schedule_runs(id, schedule_id, occurrence, status) VALUES ($1,$2,$3,'running')
		ON CONFLICT (schedule_id, occurrence) DO NOTHING`, runID, sc.ID, at)
	if err != nil || tag.RowsAffected() == 0 {
		return "", false
	}
	s.log.Info("schedule run", "schedule", sc.ID, "kind", sc.Kind, "occurrence", at, "run", runID)
	s.events.publish(Event{Type: "schedule.updated", Data: map[string]any{"id": sc.ID, "run_id": runID, "status": "running"}})
	go s.executeRun(sc, runID, at)
	return runID, true
}

type runHost struct {
	HostID    string `json:"host_id"`
	Hostname  string `json:"hostname"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail"`
	Woken     bool   `json:"woken,omitempty"`
	JobID     string `json:"job_id,omitempty"`
	CommandID string `json:"command_id,omitempty"`
}

func (s *Server) executeRun(sc schedule, runID string, at time.Time) {
	ctx := context.Background()
	actor := "schedule:" + sc.CreatedBy
	var hosts []runHost
	q := `SELECT id, COALESCE(hostname,id) FROM hosts WHERE retired_at IS NULL AND deleted_at IS NULL AND `
	var rows pgx.Rows
	var err error
	if sc.Targets.SiteID != "" {
		rows, err = s.pool.Query(ctx, q+`site_id=$1 ORDER BY hostname`, sc.Targets.SiteID)
	} else {
		rows, err = s.pool.Query(ctx, q+`id = ANY($1) ORDER BY hostname`, sc.Targets.HostIDs)
	}
	if err == nil {
		for rows.Next() {
			var h runHost
			if rows.Scan(&h.HostID, &h.Hostname) == nil {
				hosts = append(hosts, h)
			}
		}
		rows.Close()
	}
	finish := func(note string) {
		ok, fail := 0, 0
		for _, h := range hosts {
			if h.OK {
				ok++
			} else {
				fail++
			}
		}
		status := "done"
		if ok == 0 && (fail > 0 || note != "") {
			status = "failed"
		}
		res, _ := json.Marshal(map[string]any{"ok": ok, "fail": fail, "hosts": hosts, "note": note})
		_, _ = s.pool.Exec(ctx, `UPDATE schedule_runs SET status=$2, finished_at=now(), result=$3::jsonb WHERE id=$1`, runID, status, string(res))
		if sc.Repeat.Freq == "none" {
			_, _ = s.pool.Exec(ctx, `UPDATE schedules SET secret=NULL WHERE id=$1`, sc.ID)
		}
		_ = s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
			return audit.Entry{Actor: actor, Action: "schedule.run", ObjectType: "schedule", ObjectID: sc.ID, Result: status,
				Data: map[string]any{"run_id": runID, "occurrence": at, "kind": sc.Kind, "ok": ok, "fail": fail, "note": note}}, nil
		})
		s.events.publish(Event{Type: "schedule.updated", Data: map[string]any{"id": sc.ID, "run_id": runID, "status": status}})
		s.log.Info("schedule run finished", "run", runID, "status", status, "ok", ok, "fail", fail)
	}
	if len(hosts) == 0 {
		finish("ไม่มีเครื่องในเป้าหมาย")
		return
	}

	// wake what is off
	var off []string
	for i := range hosts {
		if !s.pres.isOnline(hosts[i].HostID) {
			off = append(off, hosts[i].HostID)
			hosts[i].Woken = true
		}
	}
	if len(off) > 0 {
		_, ce := s.wake(ctx, actor, "", off)
		if sc.Kind == "wake" {
			for i := range hosts {
				switch {
				case !hosts[i].Woken:
					hosts[i].OK, hosts[i].Detail = true, "เปิดอยู่แล้ว"
				case ce != nil:
					hosts[i].Detail = "ส่งสัญญาณปลุกไม่ได้: " + ce.msg
				default:
					hosts[i].OK, hosts[i].Detail = true, "ส่งสัญญาณปลุกแล้ว"
				}
			}
			finish("")
			return
		}
		deadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(deadline) {
			waiting := false
			for _, id := range off {
				if !s.pres.isOnline(id) {
					waiting = true
				}
			}
			if !waiting {
				break
			}
			time.Sleep(15 * time.Second)
		}
	} else if sc.Kind == "wake" {
		for i := range hosts {
			hosts[i].OK, hosts[i].Detail = true, "เปิดอยู่แล้ว"
		}
		finish("")
		return
	}

	var dep deployScheduleArgs
	if sc.Kind == "deploy" {
		_ = json.Unmarshal(sc.Args, &dep)
		pw, err := s.openSecret(sc.Secret)
		if err != nil || pw == "" {
			for i := range hosts {
				hosts[i].Detail = "อ่านรหัสผ่านผู้ดูแลที่เก็บไว้ไม่ได้ — สร้างตารางงานใหม่"
			}
			finish("password unavailable")
			return
		}
		dep.AdminPassword = pw
	}
	for i := range hosts {
		h := &hosts[i]
		if !s.pres.isOnline(h.HostID) {
			h.Detail = "เครื่องไม่ออนไลน์ (ปลุกแล้วไม่ตอบภายใน 5 นาที)"
			if !h.Woken {
				h.Detail = "เครื่องไม่ออนไลน์"
			}
			continue
		}
		switch sc.Kind {
		case "deploy":
			res, ce := s.startDeploy(ctx, actor, "", h.HostID, deployRequest{ImageID: dep.ImageID, Disk: dep.Disk,
				AdminUser: dep.AdminUser, AdminPassword: dep.AdminPassword})
			if ce != nil {
				h.Detail = ce.msg
				continue
			}
			h.OK, h.Detail = true, "เริ่มติดตั้งแล้ว"
			h.JobID, _ = res["job_id"].(string)
		default:
			typ, args := sc.Kind, json.RawMessage(nil)
			switch sc.Kind {
			case "reboot":
				args, _ = json.Marshal(map[string]any{"delay_s": 120, "message": "เครื่องจะรีสตาร์ตตามตารางงานของ IT ใน 2 นาที กรุณาบันทึกงาน"})
			case "shutdown":
				typ = "run_script"
				args, _ = json.Marshal(map[string]any{"name": "ปิดเครื่อง (ตารางงาน)", "timeout_s": 60,
					"body": `shutdown.exe /s /t 120 /c "เครื่องจะปิดตามตารางงานของ IT ใน 2 นาที กรุณาบันทึกงาน"`})
			}
			p, ce := s.planCommand(ctx, h.HostID, typ, args, "", actor)
			if ce != nil {
				if typ == "update_agent" && strings.HasPrefix(ce.msg, "already on agent") {
					h.OK, h.Detail = true, "Agent เป็นรุ่นล่าสุดแล้ว"
				} else {
					h.Detail = ce.msg
				}
				continue
			}
			err := s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
				return audit.Entry{Actor: actor, Action: "command." + typ, ObjectType: "host", ObjectID: h.HostID, Result: "ok",
					Data: map[string]any{"command_id": p.ID, "args": p.AuditArgs, "schedule_id": sc.ID, "run_id": runID}}, insertCommand(ctx, tx, p, "", actor, nil)
			})
			if err != nil {
				h.Detail = "บันทึกคำสั่งไม่ได้"
				continue
			}
			s.pres.nudge(h.HostID)
			s.events.publish(Event{Type: "command.updated", HostID: h.HostID, Data: map[string]any{"id": p.ID, "status": "queued"}})
			h.OK, h.Detail, h.CommandID = true, "ส่งคำสั่งแล้ว", p.ID
		}
	}
	finish("")
}

// liveRun follows a run's dispatched work to its end: per machine the current state of
// its reimage job (phase, %, message) or command, and for the run "running" until every
// machine has finished. ok/fail count finished outcomes, running counts the rest.
func (s *Server) liveRun(ctx context.Context, runStatus string, raw json.RawMessage) (string, map[string]any) {
	var res map[string]any
	if json.Unmarshal(raw, &res) != nil || res == nil {
		res = map[string]any{}
	}
	hostsAny, _ := res["hosts"].([]any)
	ok, fail, running, sum, n := 0, 0, 0, 0, 0
	for _, x := range hostsAny {
		h, _ := x.(map[string]any)
		if h == nil {
			continue
		}
		state, progress := "failed", 0
		if okv, _ := h["ok"].(bool); okv {
			state, progress = "done", 100
		}
		if jid, _ := h["job_id"].(string); jid != "" {
			var st, phase, msg string
			var pct *int
			var upd time.Time
			if err := s.pool.QueryRow(ctx, `SELECT status, COALESCE(phase,''), COALESCE(message,''), progress, updated_at
				FROM deployment_jobs WHERE id=$1`, jid).Scan(&st, &phase, &msg, &pct, &upd); err == nil {
				h["job_status"], h["phase"], h["message"], h["updated_at"] = st, phase, msg, upd
				var can bool
				_ = s.pool.QueryRow(ctx, `SELECT `+jobCols[strings.LastIndex(jobCols, "(j.status='aborted'"):]+` FROM deployment_jobs j WHERE j.id=$1`, jid).Scan(&can)
				h["can_rehandoff"] = can
				if pct != nil {
					progress = *pct
					h["progress"] = *pct
				}
				switch st {
				case "completed":
					state, progress = "done", 100
				case "failed", "aborted", "cancelled":
					state = "failed"
				default:
					state = "running"
				}
			}
		} else if cid, _ := h["command_id"].(string); cid != "" {
			var st string
			var errText string
			if err := s.pool.QueryRow(ctx, `SELECT status, COALESCE(result->>'error','') FROM agent_commands WHERE id=$1`, cid).Scan(&st, &errText); err == nil {
				h["command_status"] = st
				switch st {
				case "done":
					state, progress = "done", 100
				case "failed", "expired", "cancelled":
					state = "failed"
					if errText != "" {
						h["message"] = errText
					}
				default:
					state, progress = "running", 0
				}
			}
		}
		h["state"] = state
		switch state {
		case "done":
			ok++
		case "failed":
			fail++
		default:
			running++
		}
		sum += progress
		n++
	}
	res["ok"], res["fail"], res["running"] = ok, fail, running
	if n > 0 {
		res["progress"] = sum / n
	} else {
		res["progress"] = 0
	}
	status := runStatus
	switch {
	case runStatus == "running" || running > 0:
		status = "running"
	case ok == 0 && fail > 0:
		status = "failed"
	case n > 0:
		status = "done"
	}
	return status, res
}
