package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/backend/internal/ids"
)

// SMBIOS UUIDs that many vendors ship unset (spec 03 identity: weak identity).
var weakUUIDs = map[string]bool{
	"00000000-0000-0000-0000-000000000000": true,
	"ffffffff-ffff-ffff-ffff-ffffffffffff": true,
	"03000200-0400-0500-0006-000700080009": true,
}

type inventory struct {
	SmbiosUUID string `json:"smbios_uuid"`
	Serial     string `json:"serial"`
	Vendor     string `json:"vendor"`
	Model      string `json:"model"`
	Hostname   string `json:"hostname"`
	Arch       string `json:"arch"`
	Firmware   string `json:"firmware"`
	SecureBoot *bool  `json:"secure_boot"`
	TPMVersion string `json:"tpm_version"`
}

// enroll: {token, x25519_pub, inventory} → {host_id, host_token, keyset, config}.
//
// DEV: mTLS client certs (step-ca) arrive in phase 3. Until then the agent gets
// a random host token; only its hash is stored (hosts.dev_token_hash).
func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token     string    `json:"token"`
		Ticket    string    `json:"reimage_ticket"`
		X25519Pub string    `json:"x25519_pub"`
		Inventory inventory `json:"inventory"`
	}
	if !decode(w, r, &in) {
		return
	}
	inv := in.Inventory
	if (in.Token == "" && in.Ticket == "") || inv.SmbiosUUID == "" || inv.Serial == "" {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "token or reimage_ticket, inventory.smbios_uuid and inventory.serial required")
		return
	}
	uuid := strings.ToLower(inv.SmbiosUUID)
	weak := weakUUIDs[uuid]
	var xpub []byte
	if in.X25519Pub != "" {
		b, err := base64.RawURLEncoding.DecodeString(in.X25519Pub)
		if err != nil || len(b) != 32 {
			writeErr(w, http.StatusBadRequest, "SWIM-API-400", "x25519_pub must be 32 bytes base64url")
			return
		}
		xpub = b
	}

	tokHash := sha256.Sum256([]byte(in.Token))
	rawHostTok := make([]byte, 32)
	if _, err := rand.Read(rawHostTok); err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "rng")
		return
	}
	hostTok := "swim_ht_" + hex.EncodeToString(rawHostTok)
	hostTokHash := sha256.Sum256([]byte(hostTok))

	var hostID string
	reenrolled := false
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		// Reimage ticket: the same machine coming back after SWIM wiped it. No token
		// needed and the host keeps its id, ring, site and history (spec 03 §3, D-18).
		if in.Ticket != "" {
			th := sha256.Sum256([]byte(in.Ticket))
			var used *time.Time
			var tExp time.Time
			err := tx.QueryRow(r.Context(), `SELECT host_id, expires_at, used_at FROM reimage_tickets WHERE ticket_hash=$1 FOR UPDATE`,
				th[:]).Scan(&hostID, &tExp, &used)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return audit.Entry{}, errAuth{"unknown reimage ticket"}
			case err != nil:
				return audit.Entry{}, err
			case used != nil:
				return audit.Entry{}, errAuth{"reimage ticket already used"}
			case time.Now().After(tExp):
				return audit.Entry{}, errAuth{"reimage ticket expired"}
			}
			var hUUID *string
			if err := tx.QueryRow(r.Context(), `SELECT smbios_uuid::text FROM hosts WHERE id=$1 FOR UPDATE`, hostID).Scan(&hUUID); err != nil {
				return audit.Entry{}, err
			}
			if hUUID == nil || !strings.EqualFold(*hUUID, uuid) {
				return audit.Entry{}, errAuth{"reimage ticket belongs to a different machine"}
			}
			if _, err := tx.Exec(r.Context(), `UPDATE reimage_tickets SET used_at=now() WHERE ticket_hash=$1`, th[:]); err != nil {
				return audit.Entry{}, err
			}
			if _, err := tx.Exec(r.Context(), `UPDATE hosts SET hostname=$2, vendor=$3, model=$4, arch=$5, firmware=$6,
				secure_boot=$7, tpm_version=$8, x25519_pub=$9, dev_token_hash=$10, install_source='swim', last_seen=now(),
				retired_at=NULL, retired_reason=NULL, removed_token_hash=NULL, deleted_at=NULL, removing_at=NULL WHERE id=$1`, hostID, inv.Hostname, inv.Vendor, inv.Model, inv.Arch, inv.Firmware, inv.SecureBoot,
				inv.TPMVersion, xpub, hostTokHash[:]); err != nil {
				return audit.Entry{}, err
			}
			reenrolled = true
			return audit.Entry{Actor: "reimage_ticket", Action: "host.reenroll", ObjectType: "host", ObjectID: hostID,
				IP: clientIP(r), Result: "ok", Data: map[string]any{"hostname": inv.Hostname}}, nil
		}

		var tokID string
		var siteID *string
		var maxUses *int
		var uses int
		var expires time.Time
		err := tx.QueryRow(r.Context(), `SELECT id, site_id, max_uses, uses, expires_at FROM enrollment_tokens
			WHERE token_hash=$1 FOR UPDATE`, tokHash[:]).Scan(&tokID, &siteID, &maxUses, &uses, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return audit.Entry{}, errAuth{"unknown enrollment token"}
		} else if err != nil {
			return audit.Entry{}, err
		}
		if time.Now().After(expires) {
			enrollRefusals.Store(tokID, refusal{"enrollment token expired", inv.Hostname, time.Now()})
			return audit.Entry{}, errAuth{"enrollment token expired"}
		}
		if maxUses != nil && uses >= *maxUses {
			enrollRefusals.Store(tokID, refusal{"enrollment token used up", inv.Hostname, time.Now()})
			return audit.Entry{}, errAuth{"enrollment token used up"}
		}
		if _, err := tx.Exec(r.Context(), `UPDATE enrollment_tokens SET uses=uses+1 WHERE id=$1`, tokID); err != nil {
			return audit.Entry{}, err
		}

		if !weak {
			err = tx.QueryRow(r.Context(), `SELECT id FROM hosts
				WHERE smbios_uuid=$1 AND serial=$2 AND NOT weak_identity FOR UPDATE`, uuid, inv.Serial).Scan(&hostID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return audit.Entry{}, err
			}
			reenrolled = err == nil
			if reenrolled {
				// Every installation from an enrollment token is a NEW machine with a clean
				// history (only SWIM's own reinstall, the reimage ticket above, keeps the id).
				// The old row is hidden, gives up its hardware id and its tombstone.
				if _, err := tx.Exec(r.Context(), `UPDATE hosts SET deleted_at=COALESCE(deleted_at, now()), retired_at=COALESCE(retired_at, now()),
					removed_smbios_uuid=smbios_uuid, smbios_uuid=NULL, removed_token_hash=NULL, dev_token_hash=NULL, review_at=NULL, removing_at=NULL
					WHERE id=$1`, hostID); err != nil {
					return audit.Entry{}, err
				}
				reenrolled = false
			}
		}
		if reenrolled {
			// Same machine coming back (USB reinstall, spec 09 §4.1): keep history.
			_, err = tx.Exec(r.Context(), `UPDATE hosts SET hostname=$2, vendor=$3, model=$4, arch=$5, firmware=$6,
				secure_boot=$7, tpm_version=$8, x25519_pub=$9, dev_token_hash=$10, site_id=COALESCE($11, site_id),
				install_source='manual_install', last_seen=now(),
				retired_at=NULL, retired_reason=NULL, removed_token_hash=NULL, deleted_at=NULL, removing_at=NULL WHERE id=$1`,
				hostID, inv.Hostname, inv.Vendor, inv.Model, inv.Arch, inv.Firmware, inv.SecureBoot, inv.TPMVersion,
				xpub, hostTokHash[:], siteID)
		} else {
			hostID = ids.New("h_")
			_, err = tx.Exec(r.Context(), `INSERT INTO hosts(id, smbios_uuid, serial, vendor, model, hostname, arch, firmware,
				secure_boot, tpm_version, weak_identity, site_id, x25519_pub, dev_token_hash, last_seen)
				VALUES ($1,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,now())`,
				hostID, uuid, inv.Serial, inv.Vendor, inv.Model, inv.Hostname, inv.Arch, inv.Firmware,
				inv.SecureBoot, inv.TPMVersion, weak, siteID, xpub, hostTokHash[:])
		}
		if err != nil {
			return audit.Entry{}, err
		}
		return audit.Entry{Actor: "enroll:" + tokID, Action: "host.enroll", ObjectType: "host", ObjectID: hostID,
			IP: clientIP(r), Result: "ok", Data: map[string]any{"reenrolled": reenrolled, "weak_identity": weak,
				"model": inv.Model, "serial": inv.Serial}}, nil
	})
	var ea errAuth
	if errors.As(err, &ea) {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", ea.msg)
		return
	}
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"host_id":       hostID,
		"reenrolled":    reenrolled,
		"weak_identity": weak,
		"host_token":    hostTok,
		"cert":          nil, // phase 3: step-ca issues a 90-day mTLS cert here
		"keyset":        []map[string]string{{"kid": s.key.Kid, "alg": "Ed25519", "pub": base64.RawURLEncoding.EncodeToString(s.key.Pub)}},
		"config":        map[string]any{"heartbeat_s": s.cfg.HeartbeatS, "commands_wait_s": 55},
	})
}

type errAuth struct{ msg string }

func (e errAuth) Error() string { return e.msg }

// heartbeat: {agent_version, svc_version, state, stage_free} → {halt, halt_reason, commands_pending}.
// heartbeat: {agent_version, svc_version, state, inventory?} → {halt, halt_reason, commands_pending}.
// state "stopping" = the agent is shutting down (reboot, power off, service stop): the
// machine is shown offline immediately instead of after the presence timeout.
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	tok := bearer(r)
	if tok == "" {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "host token required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in struct {
		AgentVersion string          `json:"agent_version"`
		SvcVersion   string          `json:"svc_version"`
		State        string          `json:"state"`
		StageFree    *int64          `json:"stage_free"`
		Inventory    json.RawMessage `json:"inventory"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "invalid JSON")
		return
	}
	var inv *string
	var basic inventory
	if len(in.Inventory) > 2 && json.Unmarshal(in.Inventory, &basic) == nil {
		v := string(in.Inventory)
		inv = &v
	}
	h := sha256.Sum256([]byte(tok))
	var hostID string
	var siteID, channelID *string
	beat := func() error {
		return s.pool.QueryRow(r.Context(), `UPDATE hosts SET last_seen=now(),
		agent_version=COALESCE(NULLIF($2,''), agent_version), svc_version=COALESCE(NULLIF($3,''), svc_version),
		stage_size_bytes=COALESCE($4, stage_size_bytes),
		inventory=COALESCE($5::jsonb, inventory), inventory_at=CASE WHEN $5::jsonb IS NULL THEN inventory_at ELSE now() END,
		hostname=COALESCE(NULLIF($6,''), hostname)
		WHERE dev_token_hash=$1 AND retired_at IS NULL RETURNING id, site_id, channel_id`,
			h[:], in.AgentVersion, in.SvcVersion, in.StageFree, inv, basic.Hostname).Scan(&hostID, &siteID, &channelID)
	}
	err := beat()
	if errors.Is(err, pgx.ErrNoRows) {
		// agent of a removed machine: don't bring it back — point it at its uninstall command
		if tid, _, ok := s.tombstoned(r, h[:]); ok {
			writeJSON(w, http.StatusOK, map[string]any{"host_id": tid, "halt": false, "commands_pending": true})
			return
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "unknown host token")
		return
	}
	if err != nil {
		s.dbErr(w, err)
		return
	}
	if in.State == "stopping" {
		if s.pres.goodbye(hostID) {
			s.events.publish(Event{Type: "host.presence", HostID: hostID, Data: map[string]any{"online": false, "reason": "stopping"}})
		}
		writeJSON(w, http.StatusOK, map[string]any{"host_id": hostID})
		return
	}
	s.seenIP.Store(hostID, clientIP(r))
	s.markOnline(hostID, s.pres.touch(hostID))
	s.autoUpdateAgent(r.Context(), hostID, in.AgentVersion)
	s.reconcilePolicy(r.Context(), hostID, in.AgentVersion)
	s.reconcilePrestage(r.Context(), hostID, in.AgentVersion)
	s.watchHandoff(r.Context(), hostID)
	if inv != nil {
		s.events.publish(Event{Type: "host.updated", HostID: hostID})
	}
	// Any active switch that covers this host halts it (spec 07 §4).
	var scope, reason string
	err = s.pool.QueryRow(r.Context(), `SELECT scope, COALESCE(reason,'') FROM kill_switches WHERE active AND (
		scope='global' OR (scope='site' AND scope_id=$1) OR (scope='channel' AND scope_id=$2))
		ORDER BY CASE scope WHEN 'global' THEN 0 WHEN 'channel' THEN 1 ELSE 2 END LIMIT 1`,
		siteID, channelID).Scan(&scope, &reason)
	halt := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.dbErr(w, err)
		return
	}
	resp := map[string]any{"host_id": hostID, "halt": halt, "commands_pending": false}
	if halt {
		resp["halt_reason"] = scope + ": " + reason
	}
	writeJSON(w, http.StatusOK, resp)
}
