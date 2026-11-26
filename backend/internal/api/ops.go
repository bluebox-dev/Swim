package api

// Operations log: clearing a machine's command history (the audit log keeps it) and
// the filtered audit listing behind the Logging page.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/backend/internal/wol"
)

// clearCommandHistory (POST /v1/hosts/commands/clear {host_ids}): hide finished
// commands from these machines' history. Open commands stay; nothing is deleted.
func (s *Server) clearCommandHistory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostIDs []string `json:"host_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.HostIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "host_ids required")
		return
	}
	var n int64
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE agent_commands SET cleared_at=now()
			WHERE host_id = ANY($1) AND cleared_at IS NULL AND status IN ('done','failed','expired','cancelled')`, in.HostIDs)
		n = tag.RowsAffected()
		return s.entry(r, "commands.clear", "host", strings.Join(in.HostIDs, ","), map[string]any{"hosts": in.HostIDs, "commands_hidden": n}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	for _, h := range in.HostIDs {
		s.events.publish(Event{Type: "command.updated", HostID: h})
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": n})
}

// listAuditFiltered (GET /v1/audit?actor=&host=&action=&q=&from=&to=&limit=): newest first,
// with the entry's data (e.g. a command's full script text) for the Logging page.
func (s *Server) listAuditFiltered(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where, args := []string{"id >= " + auditFloorSQL}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if v := q.Get("actor"); v != "" {
		add("actor ILIKE ?", "%"+v+"%")
	}
	if v := q.Get("action"); v != "" {
		add("action ILIKE ?", v+"%")
	}
	if v := q.Get("host"); v != "" {
		add("(object_id ILIKE ? OR data::text ILIKE ?)", "%"+v+"%") // same $n for both
	}
	if v := q.Get("q"); v != "" {
		add("(action || ' ' || COALESCE(object_id,'') || ' ' || COALESCE(reason,'') || ' ' || COALESCE(data::text,'')) ILIKE ?", "%"+v+"%")
	}
	if t, err := time.Parse(time.RFC3339, q.Get("from")); err == nil {
		add("at >= ?", t)
	}
	if t, err := time.Parse(time.RFC3339, q.Get("to")); err == nil {
		add("at < ?", t)
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	limit = min(limit, 5000)
	rows, err := s.pool.Query(r.Context(), `SELECT id, at, actor, action, COALESCE(object_type,''), COALESCE(object_id,''),
		COALESCE(reason,''), COALESCE(result,''), COALESCE(host(ip),''), COALESCE(data,'null'::jsonb), encode(hash,'hex')
		FROM audit_log WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT `+strconv.Itoa(limit), args...)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	type item struct {
		ID         int64           `json:"id"`
		At         time.Time       `json:"at"`
		Actor      string          `json:"actor"`
		Action     string          `json:"action"`
		ObjectType string          `json:"object_type"`
		ObjectID   string          `json:"object_id"`
		Reason     string          `json:"reason,omitempty"`
		Result     string          `json:"result"`
		IP         string          `json:"ip"`
		Data       json.RawMessage `json:"data"`
		Hash       string          `json:"hash"`
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
		var it item
		var data []byte
		err := row.Scan(&it.ID, &it.At, &it.Actor, &it.Action, &it.ObjectType, &it.ObjectID, &it.Reason, &it.Result, &it.IP, &data, &it.Hash)
		it.Data = data
		return it, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// wakeHosts (POST /v1/hosts/wake {host_ids}): Wake-on-LAN through the host-network waker,
// using the MAC addresses the agents reported in their inventory.
func (s *Server) wakeHosts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostIDs []string `json:"host_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, ce := s.wake(r.Context(), "user:"+adminFrom(r).Name, clientIP(r), in.HostIDs)
	if ce != nil {
		writeErr(w, ce.status, fmt.Sprintf("SWIM-API-%d", ce.status), ce.msg)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// wake sends Wake-on-LAN to machines (server broadcast plus one agent relay per site).
func (s *Server) wake(ctx context.Context, actor, ip string, hostIDs []string) (map[string]any, *cmdErr) {
	type target struct {
		ID       string   `json:"host_id"`
		Hostname string   `json:"hostname"`
		MACs     []string `json:"macs,omitempty"`
		Reason   string   `json:"reason,omitempty"`
	}
	var sent, skipped []target
	var macs []string
	bySite := map[string][]string{} // site → MACs of machines to wake there (for agent relays)
	for _, id := range hostIDs {
		var hostname, site string
		var inv []byte
		if err := s.pool.QueryRow(ctx, `SELECT COALESCE(hostname,''), COALESCE(inventory->'network','[]'::jsonb), COALESCE(site_id,'') FROM hosts
			WHERE id=$1 AND retired_at IS NULL`, id).Scan(&hostname, &inv, &site); err != nil {
			skipped = append(skipped, target{ID: id, Reason: "ไม่พบเครื่อง"})
			continue
		}
		var nics []struct {
			MAC string `json:"mac"`
		}
		_ = json.Unmarshal(inv, &nics)
		t := target{ID: id, Hostname: hostname}
		for _, n := range nics {
			if _, err := wol.ParseMAC(n.MAC); err == nil {
				t.MACs = append(t.MACs, n.MAC)
			}
		}
		if len(t.MACs) == 0 {
			t.Reason = "ยังไม่รู้ MAC address (agent ยังไม่ส่งข้อมูลเครือข่าย)"
			skipped = append(skipped, t)
			continue
		}
		if s.pres.isOnline(id) {
			t.Reason = "เครื่องเปิดอยู่แล้ว"
			skipped = append(skipped, t)
			continue
		}
		sent = append(sent, t)
		macs = append(macs, t.MACs...)
		bySite[site] = append(bySite[site], t.MACs...)
	}
	// Relays: one online agent (≥ minWakeAgent) per site also broadcasts on its own LAN,
	// which reaches machines on subnets the server is not on.
	type relay struct {
		Site     string `json:"site_id"`
		HostID   string `json:"host_id"`
		Hostname string `json:"hostname"`
		Command  string `json:"command_id,omitempty"`
		Error    string `json:"error,omitempty"`
	}
	var relays []relay
	for site, m := range bySite {
		rows, err := s.pool.Query(ctx, `SELECT id, COALESCE(hostname,''), COALESCE(agent_version,'') FROM hosts
			WHERE retired_at IS NULL AND review_at IS NULL AND COALESCE(site_id,'')=$1`, site)
		if err != nil {
			continue
		}
		type cand struct{ id, name, ver string }
		cands, _ := pgx.CollectRows(rows, func(row pgx.CollectableRow) (cand, error) {
			var c cand
			return c, row.Scan(&c.id, &c.name, &c.ver)
		})
		for _, c := range cands {
			if !s.pres.isOnline(c.id) || versionLess(c.ver, minWakeAgent) {
				continue
			}
			args, _ := json.Marshal(map[string]any{"macs": m})
			rl := relay{Site: site, HostID: c.id, Hostname: c.name}
			if p, ce := s.planCommand(ctx, c.id, "wake", args, "", actor); ce != nil {
				rl.Error = ce.msg
			} else if err := s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
				if err := insertCommand(ctx, tx, p, "", actor, nil); err != nil {
					return audit.Entry{}, err
				}
				return audit.Entry{Actor: actor, Action: "command.wake", ObjectType: "host", ObjectID: c.id, IP: ip, Result: "ok", Data: map[string]any{"command_id": p.ID, "args": p.AuditArgs, "relay_for": m}}, nil
			}); err != nil {
				rl.Error = err.Error()
			} else {
				rl.Command = p.ID
				s.pres.nudge(c.id)
			}
			relays = append(relays, rl)
			break
		}
	}
	var targets []string
	var werr error
	if len(macs) > 0 {
		targets, werr = wol.Ask(s.cfg.WakerSock, macs)
	}
	_ = s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
		res := map[string]any{"sent": sent, "skipped": skipped, "broadcasts": targets, "relays": relays}
		if werr != nil {
			res["error"] = werr.Error()
		}
		return audit.Entry{Actor: actor, Action: "host.wake", ObjectType: "host", ObjectID: strings.Join(hostIDs, ","), IP: ip, Result: "ok", Data: res}, nil
	})
	relayed := 0
	for _, rl := range relays {
		if rl.Command != "" {
			relayed++
		}
	}
	if werr != nil && relayed == 0 {
		return nil, &cmdErr{http.StatusServiceUnavailable, "ส่งสัญญาณปลุกไม่ได้: " + werr.Error()}
	}
	out := map[string]any{"sent": sent, "skipped": skipped, "broadcasts": targets, "relays": relays}
	if werr != nil {
		out["waker_error"] = werr.Error()
	}
	return out, nil
}
