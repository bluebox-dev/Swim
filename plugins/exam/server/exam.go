// Package examserver is the server half of the exam plugin ("โหมดสอบ").
//
// It is part of the SWIM server only when the plugin is enabled at build time
// (plugins/enabled); the core never refers to it. Everything it owns is its own:
//
//   - tables exam_profiles, exam_hosts, exam_events (migrations/);
//   - a directory for screenshots (Host.DataDir), which the web server does not serve — a
//     screenshot is read only through GET /v1/exam/events/{id}/shot;
//   - routes under /v1/exam and one agent route; the web files in web/;
//   - the signed command set_exam, which only this plugin's agent half understands.
//
// Screenshots are evidence about people: an administrator can delete them at any time, what
// is kept is deleted automatically after SWIM_EXAM_KEEP_DAYS (default 30), and the person
// at the machine is told that it is watched when exam mode starts.
package examserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/plugin"
	"swim/plugins/exam/contract"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

//go:embed web
var webFiles embed.FS

const version = "1.0.0"

func init() {
	mig, _ := fs.Sub(migrationFiles, "migrations")
	web, _ := fs.Sub(webFiles, "web")
	plugin.Register(plugin.Plugin{
		Name: "exam", Title: "Exam mode", Version: version,
		Description: "Limits websites and programs during an exam and keeps screenshots as evidence.",
		Migrations:  mig,
		Web:         web,
		Commands: []plugin.CommandType{{Type: "set_exam", Risk: "change", MinAgent: minExamAgent,
			Check: func(raw json.RawMessage) (any, map[string]any, error) {
				var a contract.ExamArgs
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, nil, errors.New("set_exam: invalid arguments")
				}
				if a.On {
					if err := a.Config.Normalize(); err != nil {
						return nil, nil, err
					}
				} else {
					a.Config = contract.ExamConfig{}
					_ = a.Config.Normalize()
				}
				return a, map[string]any{"on": a.On}, nil
			}}},
		Tools:  map[string]func() (string, string){"exam-check": examCheck},
		Routes: func(m *http.ServeMux, h plugin.Host) { srv{h}.routes(m) },
		Heartbeat: func(ctx context.Context, h plugin.Host, hostID, agentVer string) {
			srv{h}.reconcile(ctx, hostID, agentVer)
		},
		Hourly: func(ctx context.Context, h plugin.Host) { srv{h}.prune(ctx) },
	})
}

// srv is the plugin's handlers around the Host the server gave it.
type srv struct{ h plugin.Host }

const (
	minExamAgent     = "0.5.39" // first agent that can carry plugins
	examMaxShot      = 2 << 20  // a screenshot, after base64 decoding
	examEventsPerHr  = 600      // per machine; more is refused (429) and the agent keeps it for later
	examListMax      = 200
	examDetailValues = 300
)

func examKeepDays() int {
	if v, err := strconv.Atoi(os.Getenv("SWIM_EXAM_KEEP_DAYS")); err == nil && v >= 1 && v <= 3650 {
		return v
	}
	return 30
}

// examRecommended is the set-up the web page starts from: a web exam on listed sites.
func examRecommended() contract.ExamConfig {
	return contract.ExamConfig{WebMode: "allow", Sites: []string{}, BlockSites: []string{}, NoExtensions: true, NoDevTools: true, NoPrivate: true,
		AppGroups: []string{"ai", "chat", "remote"}, BlockApps: []string{}, NoCopilot: true, BlockUSB: true,
		WatchAIWords: true, Keywords: []string{}, ShotOnViolate: true}
}

func (s srv) routes(m *http.ServeMux) {
	a := s.h.Admin
	m.HandleFunc("GET /v1/exam/config", a(s.examConfig))
	m.HandleFunc("POST /v1/agent/exam/events", s.agentExamEvent)
	m.HandleFunc("GET /v1/exam/hosts", a(s.examHosts))
	m.HandleFunc("POST /v1/exam/apply", a(s.examApply))
	m.HandleFunc("GET /v1/exam/profiles", a(s.examProfiles))
	m.HandleFunc("POST /v1/exam/profiles", a(s.examSaveProfile))
	m.HandleFunc("DELETE /v1/exam/profiles/{id}", a(s.examDeleteProfile))
	m.HandleFunc("GET /v1/exam/events", a(s.examEvents))
	m.HandleFunc("GET /v1/exam/events/{id}/shot", a(s.examShot))
	m.HandleFunc("DELETE /v1/exam/events/{id}", a(s.examDeleteEvent))
	m.HandleFunc("POST /v1/exam/events/delete", a(s.examDeleteEvents))
}

func (s srv) examConfig(w http.ResponseWriter, r *http.Request) {
	s.h.JSON(w, http.StatusOK, map[string]any{"enabled": true, "keep_days": examKeepDays(), "min_agent": minExamAgent,
		"recommended": examRecommended(), "ai_sites": contract.ExamAISites, "app_groups": contract.ExamAppGroups, "ai_words": contract.ExamAIWords})
}

// ───────── set-ups ─────────

func (s srv) examProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.h.DB().Query(r.Context(), `SELECT id, name, config, created_at, created_by FROM exam_profiles ORDER BY lower(name)`)
	if err != nil {
		s.h.DBError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, by string
		var cfg json.RawMessage
		var at time.Time
		if err := rows.Scan(&id, &name, &cfg, &at, &by); err != nil {
			s.h.DBError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "config": cfg, "created_at": at, "created_by": by})
	}
	s.h.JSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s srv) examSaveProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string              `json:"name"`
		Config contract.ExamConfig `json:"config"`
	}
	if !s.h.Decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if n := len([]rune(in.Name)); n < 1 || n > 80 {
		s.h.Error(w, http.StatusBadRequest, "name: 1 to 80 characters")
		return
	}
	if err := in.Config.Normalize(); err != nil {
		s.h.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, _ := json.Marshal(in.Config)
	id := s.h.NewID("exp_")
	err := s.h.Do(r, "exam.profile_saved", "exam_profile", in.Name, map[string]any{"name": in.Name}, func(tx pgx.Tx) error {
		// saving under an existing name replaces that set-up
		return tx.QueryRow(r.Context(), `INSERT INTO exam_profiles(id, name, config, created_by) VALUES($1,$2,$3,$4)
			ON CONFLICT (name) DO UPDATE SET config=EXCLUDED.config, created_at=now(), created_by=EXCLUDED.created_by RETURNING id`,
			id, in.Name, string(cfg), s.h.AdminName(r)).Scan(&id)
	})
	if err != nil {
		s.h.DBError(w, err)
		return
	}
	s.h.JSON(w, http.StatusOK, map[string]any{"id": id, "name": in.Name, "config": in.Config})
}

func (s srv) examDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data := map[string]any{}
	err := s.h.Do(r, "exam.profile_deleted", "exam_profile", id, data, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(r.Context(), `DELETE FROM exam_profiles WHERE id=$1 RETURNING name`, id).Scan(&name); err != nil {
			return err
		}
		data["name"] = name
		return nil
	})
	if err != nil {
		s.h.DBError(w, err)
		return
	}
	s.h.JSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// ───────── turning exam mode on and off ─────────

// examApply sets exam mode on machines.
//
//	POST /v1/exam/apply  {"host_ids": [...] | "site_id": "...", "on": true, "config": {...}, "profile_name": "..."}
func (s srv) examApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostIDs     []string            `json:"host_ids"`
		SiteID      string              `json:"site_id"`
		On          bool                `json:"on"`
		Config      contract.ExamConfig `json:"config"`
		ProfileName string              `json:"profile_name"`
	}
	if !s.h.Decode(w, r, &in) {
		return
	}
	if (len(in.HostIDs) == 0) == (in.SiteID == "") {
		s.h.Error(w, http.StatusBadRequest, "either host_ids or site_id required")
		return
	}
	if in.On {
		if err := in.Config.Normalize(); err != nil {
			s.h.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		if !in.Config.Restricts() {
			s.h.Error(w, http.StatusBadRequest, "this set-up limits nothing — choose at least one limit")
			return
		}
	} else {
		in.Config, in.ProfileName = contract.ExamConfig{}, ""
		_ = in.Config.Normalize()
	}
	hostIDs := in.HostIDs
	if in.SiteID != "" {
		rows, err := s.h.DB().Query(r.Context(), `SELECT id FROM hosts WHERE site_id=$1 AND retired_at IS NULL AND deleted_at IS NULL`, in.SiteID)
		if err != nil {
			s.h.DBError(w, err)
			return
		}
		if hostIDs, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			s.h.DBError(w, err)
			return
		}
	}
	if len(hostIDs) > 2000 {
		s.h.Error(w, http.StatusBadRequest, "at most 2000 machines at a time")
		return
	}
	actor := "user:" + s.h.AdminName(r)
	cfg, _ := json.Marshal(in.Config)
	updated, queued := 0, 0
	skipped := []map[string]string{}
	for _, id := range hostIDs {
		cmd, planErr := s.h.PlanCommand(r.Context(), id, "set_exam", contract.ExamArgs{On: in.On, Config: in.Config}, actor)
		sent := false
		data := map[string]any{"on": in.On, "profile": in.ProfileName}
		if in.On {
			data["config"] = in.Config
		}
		err := s.h.Do(r, "exam.apply", "host", id, data, func(tx pgx.Tx) error {
			var ok bool
			if err := tx.QueryRow(r.Context(), `SELECT true FROM hosts WHERE id=$1 AND retired_at IS NULL AND deleted_at IS NULL`, id).Scan(&ok); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO exam_hosts(host_id, enabled, config, profile_name, updated_by) VALUES($1,$2,$3,$4,$5)
				ON CONFLICT (host_id) DO UPDATE SET enabled=EXCLUDED.enabled, config=EXCLUDED.config, profile_name=EXCLUDED.profile_name,
					updated_at=now(), updated_by=EXCLUDED.updated_by`, id, in.On, string(cfg), in.ProfileName, s.h.AdminName(r)); err != nil {
				return err
			}
			if planErr != nil {
				data["deferred"] = planErr.Error() // old agent, or the machine cannot take it now: the heartbeat sends it later
				return nil
			}
			if err := cmd.Queue(r.Context(), tx); err != nil {
				return err
			}
			data["command_id"], sent = cmd.ID, true
			return nil
		})
		if err != nil {
			msg := "database error"
			if errors.Is(err, pgx.ErrNoRows) {
				msg = "no such machine"
			}
			skipped = append(skipped, map[string]string{"host_id": id, "reason": msg})
			continue
		}
		updated++
		if sent {
			queued++
			s.h.Nudge(id)
		}
		s.h.Publish("host.updated", id, nil)
	}
	s.h.JSON(w, http.StatusOK, map[string]any{"updated": updated, "queued": queued, "skipped": skipped})
}

// examHosts lists the machines that have (or had) an exam set-up, with what each reports
// and how many findings came in during the last 24 hours.
func (s srv) examHosts(w http.ResponseWriter, r *http.Request) {
	q := `SELECT e.host_id, COALESCE(h.hostname,''), e.enabled, e.config, e.profile_name, e.updated_at, e.updated_by,
		COALESCE(h.inventory->'plugins'->'exam','null'::jsonb), COALESCE(h.agent_version,''), h.last_seen,
		(SELECT count(*) FROM exam_events v WHERE v.host_id=e.host_id AND v.at > now() - interval '24 hours' AND v.kind NOT IN ('periodic','app_background')),
		(SELECT max(v.at) FROM exam_events v WHERE v.host_id=e.host_id)
		FROM exam_hosts e JOIN hosts h ON h.id=e.host_id WHERE h.retired_at IS NULL AND h.deleted_at IS NULL`
	args := []any{}
	if id := r.URL.Query().Get("host_id"); id != "" {
		q += ` AND e.host_id=$1`
		args = append(args, id)
	} else {
		q += ` AND (e.enabled OR e.updated_at > now() - interval '30 days')`
	}
	rows, err := s.h.DB().Query(r.Context(), q+` ORDER BY e.enabled DESC, lower(COALESCE(h.hostname,'')) LIMIT 2000`, args...)
	if err != nil {
		s.h.DBError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, profile, by, ver string
		var on bool
		var cfg, measured json.RawMessage
		var at time.Time
		var seen, last *time.Time
		var n int
		if err := rows.Scan(&id, &name, &on, &cfg, &profile, &at, &by, &measured, &ver, &seen, &n, &last); err != nil {
			s.h.DBError(w, err)
			return
		}
		out = append(out, map[string]any{"host_id": id, "hostname": name, "on": on, "config": cfg, "profile_name": profile, "updated_at": at,
			"updated_by": by, "measured": measured, "agent_version": ver, "last_seen": seen, "online": s.h.Online(id), "findings_24h": n, "last_finding": last})
	}
	s.h.JSON(w, http.StatusOK, map[string]any{"items": out})
}

var examSeen sync.Map // hostID → last reconcile attempt

// reconcileExam (heartbeat hook): send set_exam again when the machine's exam state differs
// from the desired one — a reinstalled machine, an expired command, an agent that was too old.
func (s srv) reconcile(ctx context.Context, hostID, agentVer string) {
	if agentVer == "" || s.h.VersionLess(agentVer, minExamAgent) {
		return
	}
	if t, ok := examSeen.Load(hostID); ok && time.Since(t.(time.Time)) < 2*time.Minute {
		return
	}
	examSeen.Store(hostID, time.Now())
	var on bool
	var cfgRaw, have []byte
	if err := s.h.DB().QueryRow(ctx, `SELECT e.enabled, e.config, h.inventory->'plugins'->'exam' FROM exam_hosts e JOIN hosts h ON h.id=e.host_id
		WHERE e.host_id=$1`, hostID).Scan(&on, &cfgRaw, &have); err != nil || have == nil {
		return // exam mode never set for this machine, or the agent has not reported it yet
	}
	var want contract.ExamConfig
	var m struct {
		Desired bool                `json:"desired"`
		Config  contract.ExamConfig `json:"config"`
	}
	if json.Unmarshal(cfgRaw, &want) != nil || json.Unmarshal(have, &m) != nil {
		return
	}
	if on == m.Desired {
		if !on {
			return
		}
		_ = want.Normalize()
		_ = m.Config.Normalize()
		a, _ := json.Marshal(want)
		b, _ := json.Marshal(m.Config)
		if bytes.Equal(a, b) {
			return
		}
	}
	var busy bool
	if err := s.h.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_jobs WHERE host_id=$1 AND finished_at IS NULL)
		OR EXISTS(SELECT 1 FROM agent_commands WHERE host_id=$1 AND type='set_exam'
			AND (status IN ('queued','delivered','running') OR created_at > now() - interval '10 minutes'))`, hostID).Scan(&busy); err != nil || busy {
		return
	}
	const actor = "system:exam"
	cmd, err := s.h.PlanCommand(ctx, hostID, "set_exam", contract.ExamArgs{On: on, Config: want}, actor)
	if err != nil {
		return
	}
	if err := s.h.DoAs(ctx, actor, "command.set_exam", "host", hostID,
		map[string]any{"command_id": cmd.ID, "on": on, "reason": "machine state differs from the exam set-up"},
		func(tx pgx.Tx) error { return cmd.Queue(ctx, tx) }); err != nil {
		s.h.Log().Error("exam reconcile", "host", hostID, "err", err)
		return
	}
	s.h.Nudge(hostID)
}

// ───────── findings and screenshots ─────────

// app_background and periodic are records, not violations: a program that ran without a
// window proves nothing about the person at the machine.
var examKinds = map[string]bool{"app_background": true, "app_blocked": true, "keyword": true, "periodic": true, "watcher_stopped": true, "settings_changed": true}

// agentExamEvent stores one finding from a machine's watcher.
func (s srv) agentExamEvent(w http.ResponseWriter, r *http.Request) {
	hostID, ok := s.h.AgentAuth(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, examMaxShot*4/3+64<<10)
	var in struct {
		At     time.Time         `json:"at"`
		Kind   string            `json:"kind"`
		Detail map[string]string `json:"detail"`
		Shot   string            `json:"shot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.h.Error(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if !examKinds[in.Kind] {
		s.h.Error(w, http.StatusBadRequest, "unknown kind")
		return
	}
	now := time.Now()
	if in.At.IsZero() || in.At.After(now.Add(10*time.Minute)) || in.At.Before(now.Add(-7*24*time.Hour)) {
		in.At = now // a machine with a wrong clock: keep the finding, use the server's time
	}
	detail := map[string]string{}
	for k, v := range in.Detail {
		if len(detail) >= 12 || len(k) > 40 {
			break
		}
		if rs := []rune(v); len(rs) > examDetailValues {
			v = string(rs[:examDetailValues])
		}
		detail[k] = v
	}
	// only machines that are in exam mode may send findings, and not without limit
	var on bool
	var recent int
	if err := s.h.DB().QueryRow(r.Context(), `SELECT COALESCE((SELECT enabled OR updated_at > now() - interval '1 hour' FROM exam_hosts WHERE host_id=$1), false),
		(SELECT count(*) FROM exam_events WHERE host_id=$1 AND received_at > now() - interval '1 hour')`, hostID).Scan(&on, &recent); err != nil {
		s.h.DBError(w, err)
		return
	}
	if !on {
		s.h.Error(w, http.StatusConflict, "this machine is not in exam mode")
		return
	}
	if recent >= examEventsPerHr {
		s.h.Error(w, http.StatusTooManyRequests, "too many findings from this machine in the last hour")
		return
	}
	id := s.h.NewID("exe_")
	var file, sum *string
	var size *int
	if in.Shot != "" {
		jpg, err := base64.StdEncoding.DecodeString(in.Shot)
		if err != nil || len(jpg) > examMaxShot || len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 || jpg[2] != 0xFF {
			s.h.Error(w, http.StatusBadRequest, "shot: a JPEG of at most 2 MB")
			return
		}
		rel := filepath.Join(hostID, in.At.UTC().Format("2006-01-02"), id+".jpg") // ids are generated here: no path from the machine
		full := filepath.Join(s.h.DataDir(), rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			s.h.Log().Error("exam evidence dir", "err", err)
			s.h.Error(w, http.StatusInternalServerError, "cannot store the screenshot")
			return
		}
		if err := os.WriteFile(full, jpg, 0o640); err != nil {
			s.h.Log().Error("exam evidence write", "err", err)
			s.h.Error(w, http.StatusInternalServerError, "cannot store the screenshot")
			return
		}
		h := sha256.Sum256(jpg)
		hx, n := hex.EncodeToString(h[:]), len(jpg)
		file, sum, size = &rel, &hx, &n
	}
	d, _ := json.Marshal(detail)
	if _, err := s.h.DB().Exec(r.Context(), `INSERT INTO exam_events(id, host_id, at, kind, detail, shot_file, shot_sha256, shot_bytes)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, hostID, in.At, in.Kind, string(d), file, sum, size); err != nil {
		if file != nil {
			_ = os.Remove(filepath.Join(s.h.DataDir(), *file))
		}
		s.h.DBError(w, err)
		return
	}
	s.h.Touch(hostID)
	s.h.Publish("exam.event", hostID, map[string]any{"id": id, "kind": in.Kind})
	s.h.JSON(w, http.StatusOK, map[string]any{"id": id})
}

// examEvents lists findings, newest first.
//
//	GET /v1/exam/events?host_id=&kind=&before=<RFC3339>&limit=
func (s srv) examEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where, args := examWhere(q.Get("host_id"), q.Get("kind"))
	if v := q.Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			args = append(args, t)
			where = append(where, "v.at < $"+strconv.Itoa(len(args)))
		}
	}
	limit := 60
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= examListMax {
		limit = n
	}
	rows, err := s.h.DB().Query(r.Context(), `SELECT v.id, v.host_id, COALESCE(h.hostname,''), v.at, v.received_at, v.kind, v.detail,
		v.shot_file IS NOT NULL, COALESCE(v.shot_sha256,''), COALESCE(v.shot_bytes,0)
		FROM exam_events v JOIN hosts h ON h.id=v.host_id WHERE `+strings.Join(where, " AND ")+` ORDER BY v.at DESC LIMIT `+strconv.Itoa(limit), args...)
	if err != nil {
		s.h.DBError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, host, name, kind, sum string
		var at, rec time.Time
		var detail json.RawMessage
		var shot bool
		var size int
		if err := rows.Scan(&id, &host, &name, &at, &rec, &kind, &detail, &shot, &sum, &size); err != nil {
			s.h.DBError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "host_id": host, "hostname": name, "at": at, "received_at": rec, "kind": kind, "detail": detail,
			"has_shot": shot, "shot_sha256": sum, "shot_bytes": size})
	}
	s.h.JSON(w, http.StatusOK, map[string]any{"items": out, "keep_days": examKeepDays()})
}

// examShot returns one screenshot. The file is checked against the hash stored when it
// arrived, so a file changed on disk afterwards is refused instead of shown as evidence.
func (s srv) examShot(w http.ResponseWriter, r *http.Request) {
	var rel, sum string
	if err := s.h.DB().QueryRow(r.Context(), `SELECT COALESCE(shot_file,''), COALESCE(shot_sha256,'') FROM exam_events WHERE id=$1`, r.PathValue("id")).Scan(&rel, &sum); err != nil {
		s.h.DBError(w, err)
		return
	}
	if rel == "" {
		s.h.Error(w, http.StatusNotFound, "this finding has no screenshot")
		return
	}
	b, err := os.ReadFile(filepath.Join(s.h.DataDir(), filepath.Clean("/"+rel)))
	if err != nil {
		s.h.Error(w, http.StatusNotFound, "the screenshot file is gone")
		return
	}
	if h := sha256.Sum256(b); hex.EncodeToString(h[:]) != sum {
		s.h.Log().Error("exam evidence changed on disk", "event", r.PathValue("id"))
		s.h.Error(w, http.StatusConflict, "the screenshot file does not match the hash recorded when it arrived")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// examWhere builds the filter shared by the list and the bulk delete, so "delete what the
// filter shows" removes exactly the rows the admin was looking at.
func examWhere(hostID, kind string) ([]string, []any) {
	where, args := []string{"true"}, []any{}
	if hostID != "" {
		args = append(args, hostID)
		where = append(where, "v.host_id=$"+strconv.Itoa(len(args)))
	}
	if kind == "violations" {
		where = append(where, "v.kind NOT IN ('periodic','app_background')")
	} else if kind != "" {
		args = append(args, kind)
		where = append(where, "v.kind=$"+strconv.Itoa(len(args)))
	}
	return where, args
}

// examDeleteEvent removes one finding and its screenshot, for good.
func (s srv) examDeleteEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var file string
	data := map[string]any{"count": 1}
	err := s.h.Do(r, "exam.evidence_deleted", "exam_event", id, data, func(tx pgx.Tx) error {
		var host, kind string
		var at time.Time
		if err := tx.QueryRow(r.Context(), `DELETE FROM exam_events WHERE id=$1 RETURNING host_id, kind, at, COALESCE(shot_file,'')`, id).Scan(&host, &kind, &at, &file); err != nil {
			return err
		}
		data["host_id"], data["kind"], data["at"] = host, kind, at
		return nil
	})
	if err != nil {
		s.h.DBError(w, err)
		return
	}
	if file != "" {
		_ = os.Remove(filepath.Join(s.h.DataDir(), filepath.Clean("/"+file)))
	}
	s.h.JSON(w, http.StatusOK, map[string]any{"deleted": 1})
}

// examDeleteEvents removes every finding that matches the filter, with the screenshots.
//
//	POST /v1/exam/events/delete  {"host_id": "", "kind": "violations" | "" | <kind>, "confirm": true}
//
// An empty filter means everything; "confirm" must be true so a malformed request cannot do that.
func (s srv) examDeleteEvents(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostID  string `json:"host_id"`
		Kind    string `json:"kind"`
		Confirm bool   `json:"confirm"`
	}
	if !s.h.Decode(w, r, &in) {
		return
	}
	if !in.Confirm {
		s.h.Error(w, http.StatusBadRequest, "confirm must be true")
		return
	}
	if in.Kind != "" && in.Kind != "violations" && !examKinds[in.Kind] {
		s.h.Error(w, http.StatusBadRequest, "unknown kind")
		return
	}
	where, args := examWhere(in.HostID, in.Kind)
	var files []string
	data := map[string]any{"host_id": in.HostID, "kind": in.Kind}
	err := s.h.Do(r, "exam.evidence_deleted", "exam", "events", data, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `DELETE FROM exam_events v WHERE `+strings.Join(where, " AND ")+` RETURNING COALESCE(v.shot_file,'')`, args...)
		if err != nil {
			return err
		}
		if files, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return err
		}
		data["count"] = len(files)
		return nil
	})
	if err != nil {
		s.h.DBError(w, err)
		return
	}
	for _, f := range files {
		if f != "" {
			_ = os.Remove(filepath.Join(s.h.DataDir(), filepath.Clean("/"+f)))
		}
	}
	s.h.JSON(w, http.StatusOK, map[string]any{"deleted": len(files)})
}

// pruneExam deletes findings and screenshots older than the retention period. Runs at
// start-up and every hour (Run).
func (s srv) prune(ctx context.Context) {
	days := examKeepDays()
	rows, err := s.h.DB().Query(ctx, `DELETE FROM exam_events WHERE at < now() - make_interval(days => $1) RETURNING COALESCE(shot_file,'')`, days)
	if err != nil {
		s.h.Log().Error("exam retention", "err", err)
		return
	}
	files, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		s.h.Log().Error("exam retention", "err", err)
		return
	}
	for _, f := range files {
		if f != "" {
			_ = os.Remove(filepath.Join(s.h.DataDir(), filepath.Clean("/"+f)))
		}
	}
	// files without a row (a machine that was deleted): by age, then empty folders
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	_ = filepath.Walk(s.h.DataDir(), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
		return nil
	})
	if hosts, err := os.ReadDir(s.h.DataDir()); err == nil {
		for _, h := range hosts {
			if days, err := os.ReadDir(filepath.Join(s.h.DataDir(), h.Name())); err == nil {
				for _, d := range days {
					_ = os.Remove(filepath.Join(s.h.DataDir(), h.Name(), d.Name())) // only succeeds when empty
				}
			}
			_ = os.Remove(filepath.Join(s.h.DataDir(), h.Name()))
		}
	}
	if len(files) > 0 {
		s.h.Log().Info("exam retention", "deleted", len(files), "keep_days", days)
	}
}

// examCheck is a read-only ready-made tool: what exam mode wrote on the machine and whether
// its watcher runs. Run it with run_script {"tool": "exam-check"}.
func examCheck() (string, string) {
	return "ตรวจโหมดสอบบนเครื่อง", `foreach ($b in 'Microsoft\Edge','Google\Chrome','BraveSoftware\Brave','Mozilla\Firefox') {
  $k = "HKLM:\SOFTWARE\Policies\$b"
  if (!(Test-Path $k)) { "[$b] ไม่มีการตั้งค่า"; continue }
  $p = Get-ItemProperty $k
  "[$b] SwimExam=" + $p.SwimExam + ' ค่าอื่น: ' + ((($p.PSObject.Properties | Where-Object { $_.Name -notlike 'PS*' -and $_.Name -ne 'SwimExam' }) | ForEach-Object { $_.Name + '=' + $_.Value }) -join ', ')
  foreach ($n in 'URLBlocklist','URLAllowlist','ExtensionInstallBlocklist','WebsiteFilter\Block','WebsiteFilter\Exceptions') {
    $i = Get-Item "$k\$n" -ErrorAction SilentlyContinue
    if ($i) { $v = @($i.Property | ForEach-Object { $i.GetValue($_) }); "  $n (" + $v.Count + '): ' + (($v | Select-Object -First 8) -join ', ') + $(if ($v.Count -gt 8) { ' …' }) }
  }
}
'--- ผู้ใช้ที่เข้าใช้งานอยู่ ---'
quser.exe 2>&1 | Out-String
'--- โปรแกรมที่เกี่ยวข้อง ---'
Get-Process msedge,chrome,brave,firefox,opera,charmap,swim-exam-watch* -ErrorAction SilentlyContinue | Group-Object Name,SessionId | ForEach-Object { $_.Name + ' จำนวน ' + $_.Count }
'--- ตัวเฝ้าดู ---'
$d = Join-Path $env:ProgramData 'SWIM\exam'
if (Test-Path "$d\watch.json") { Get-Content "$d\watch.json" -Raw; 'เวลาเครื่องตอนนี้ (UTC): ' + [DateTime]::UtcNow.ToString('o') } else { 'ยังไม่มีไฟล์สถานะของตัวเฝ้าดู' }
'หลักฐานค้างส่ง: ' + @(Get-ChildItem "$d\spool" -Filter *.json -ErrorAction SilentlyContinue).Count + ' รายการ'`
}
