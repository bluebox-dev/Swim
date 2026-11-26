package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/nacl/box"

	"swim/backend/internal/audit"
	"swim/pkg/manifest"
)

const (
	minRemoteSessionAgent = "0.5.15"
	remoteAccount         = "swim-remote" // dedicated local admin account the agent manages
	remoteTTL             = 10 * time.Minute
)

// randPassword: 28 random letters/digits plus a fixed tail that satisfies Windows
// complexity rules (about 166 bits; used once, the account is disabled after remoteTTL).
func randPassword() string {
	const abc = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	b := make([]byte, 28)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b) + "-aZ9"
}

// Remote Desktop into a managed machine, in the browser, through the Guacamole proxy
// that already serves the builder VM consoles.
//
//	POST /v1/hosts/{id}/remote   body (optional, old agents only): {"enable": true, "username": "..."}
//
// → {"url": "/guacamole/#/client/…", "address": "10.0.0.5", "command_id": "cmd_…", "passwordless": true}
//
// Agents ≥ 0.5.15 — one click, no password prompt: the server makes a random one-time
// password, seals it to the machine's key and sends a signed remote_session command; the
// agent puts it on the dedicated local admin account "swim-remote", turns Remote Desktop
// on for this server's address only, and disables the account again after 10 minutes
// (an open session keeps running). The same password goes into the 5-minute Guacamole
// link, encrypted with the Guacamole key. It is stored nowhere in clear and shown to nobody.
// Wait for command_id to finish before opening the link.
//
// Older agents: "enable" sends a script that turns Remote Desktop on, and Guacamole asks
// for a Windows account. Every use is in the audit log (host.remote).
func (s *Server) hostRemote(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("id")
	var in struct {
		Enable   bool   `json:"enable"`
		Username string `json:"username"`
	}
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	var hostname, agentVer string
	var inv, xpub []byte
	if err := s.pool.QueryRow(r.Context(), `SELECT COALESCE(hostname,''), COALESCE(inventory->'network','[]'::jsonb),
		COALESCE(agent_version,''), x25519_pub FROM hosts WHERE id=$1 AND retired_at IS NULL`, hostID).Scan(&hostname, &inv, &agentVer, &xpub); err != nil {
		s.dbErr(w, err)
		return
	}
	var nics []struct {
		IPv4 []string `json:"ipv4"`
	}
	_ = json.Unmarshal(inv, &nics)
	addr := ""
	for _, n := range nics {
		for _, ip := range n.IPv4 {
			if p := net.ParseIP(ip); p != nil && !p.IsLoopback() && !p.IsLinkLocalUnicast() && addr == "" {
				addr = ip
			}
		}
	}
	if addr == "" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "this machine has not reported an IP address yet — refresh its inventory first")
		return
	}
	actor := "user:" + adminFrom(r).Name
	// RDP is opened only for the address admins use to reach this server (when it is an IP)
	from := "LocalSubnet"
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		from = ip.String()
	}
	var p *cmdPlan
	var ce *cmdErr
	password := ""
	passwordless := len(xpub) == 32 && !versionLess(agentVer, minRemoteSessionAgent)
	switch {
	case passwordless:
		password = randPassword()
		secrets, _ := json.Marshal(manifest.RemoteSecrets{User: remoteAccount, Password: password})
		var pub [32]byte
		copy(pub[:], xpub)
		sealed, err := box.SealAnonymous(nil, secrets, &pub, rand.Reader)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "seal: "+err.Error())
			return
		}
		raw, _ := json.Marshal(manifest.RemoteSessionArgs{Sealed: base64.StdEncoding.EncodeToString(sealed), AllowFrom: from,
			TTLS: int(remoteTTL.Seconds())})
		p, ce = s.planCommand(r.Context(), hostID, "remote_session", raw, "", actor)
	case in.Enable:
		body := fmt.Sprintf(`# SWIM: turn Remote Desktop on, reachable only from the SWIM server (%[1]s)
Set-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name fDenyTSConnections -Value 0
Set-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp' -Name UserAuthentication -Value 1
$rules = Get-NetFirewallRule -Group '@FirewallAPI.dll,-28752'
$rules | Set-NetFirewallRule -Enabled True -Profile Any -RemoteAddress '%[1]s'
Set-Service TermService -StartupType Manual; Start-Service TermService -ErrorAction SilentlyContinue
"Remote Desktop is on; firewall allows RDP from %[1]s only ($($rules.Count) rules)"
`, from)
		raw, _ := json.Marshal(map[string]any{"name": "เปิด Remote Desktop (เฉพาะจาก SWIM server)", "body": body, "timeout_s": 120})
		p, ce = s.planCommand(r.Context(), hostID, "run_script", raw, "", actor)
	}
	if ce != nil {
		writeErr(w, ce.status, fmt.Sprintf("SWIM-API-%d", ce.status), ce.msg)
		return
	}
	name := hostname
	if name == "" {
		name = hostID
	}
	params := map[string]string{
		"hostname": addr, "port": "3389", "security": "any", "ignore-cert": "true",
		"resize-method": "display-update", "color-depth": "24", "clipboard-encoding": "UTF-8",
		// show the desktop as the user sees it: Guacamole turns these off by default (black background, flat theme)
		"enable-wallpaper": "true", "enable-theming": "true", "enable-font-smoothing": "true", "enable-desktop-composition": "true",
	}
	if passwordless {
		params["username"], params["password"] = remoteAccount, password
	} else if in.Username != "" {
		params["username"] = in.Username
	}
	payload, _ := json.Marshal(map[string]any{
		"username":    adminFrom(r).Name,
		"expires":     time.Now().Add(5 * time.Minute).UnixMilli(),
		"connections": map[string]any{name: map[string]any{"protocol": "rdp", "parameters": params}},
	})
	data, err := guacSign(s.cfg.GuacSecret, payload)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", err.Error())
		return
	}
	out := map[string]any{"address": addr, "passwordless": passwordless,
		"url": "/guacamole/#/client/" + base64.StdEncoding.EncodeToString([]byte(name+"\x00c\x00json")) + "?data=" + url.QueryEscape(data)}
	err = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		data := map[string]any{"address": addr, "passwordless": passwordless, "enable": in.Enable, "username": in.Username}
		if passwordless {
			data["username"] = remoteAccount
		}
		e := s.entry(r, "host.remote", "host", hostID, data)
		if p == nil {
			return e, nil
		}
		data["command_id"] = p.ID
		return e, insertCommand(r.Context(), tx, p, "", actor, nil)
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	if p != nil {
		out["command_id"] = p.ID
		s.pres.nudge(hostID)
		s.events.publish(Event{Type: "command.updated", HostID: hostID, Data: map[string]any{"id": p.ID, "status": "queued"}})
	}
	writeJSON(w, http.StatusOK, out)
}
