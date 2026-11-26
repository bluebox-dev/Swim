package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"swim/backend/internal/audit"
	"swim/backend/internal/msi"
)

// certPin is the SHA-256 of the server certificate's DER bytes. The bootstrap
// command and the agent pin it, so a self-signed cert is still safe to trust.
func (s *Server) certPin() (string, error) {
	b, err := os.ReadFile(s.cfg.CertFile)
	if err != nil {
		return "", err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return "", os.ErrInvalid
	}
	sum := sha256.Sum256(blk.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// enrollInfo gives the "Add machine" page what it needs to build the install command.
func (s *Server) enrollInfo(w http.ResponseWriter, r *http.Request) {
	pin, err := s.certPin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "cannot read server certificate: "+err.Error())
		return
	}
	var latest map[string]any
	if b, err := os.ReadFile(filepath.Join(s.cfg.StorageDir, "agent", "latest.json")); err == nil {
		_ = json.Unmarshal(b, &latest)
	}
	writeJSON(w, http.StatusOK, map[string]any{"cert_sha256": pin, "agent": latest, "heartbeat_s": s.cfg.HeartbeatS})
}

func (s *Server) listEnrollTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT t.id, t.site_id, s.name, t.max_uses, t.uses, t.expires_at, COALESCE(t.created_by,'')
		FROM enrollment_tokens t LEFT JOIN sites s ON s.id = t.site_id
		WHERE t.expires_at > now() - interval '7 days' ORDER BY t.expires_at DESC LIMIT 50`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var id, createdBy string
		var siteID, siteName *string
		var maxUses *int
		var uses int
		var exp time.Time
		err := row.Scan(&id, &siteID, &siteName, &maxUses, &uses, &exp, &createdBy)
		return map[string]any{"id": id, "site_id": siteID, "site_name": siteName, "max_uses": maxUses, "uses": uses,
			"expires_at": exp, "created_by": createdBy, "active": exp.After(time.Now()) && (maxUses == nil || uses < *maxUses)}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// revokeEnrollToken expires a token now. Machines already enrolled are unaffected.
func (s *Server) revokeEnrollToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE enrollment_tokens SET expires_at=now() WHERE id=$1 AND expires_at > now()`, id)
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
		return s.entry(r, "enrollment_token.revoke", "enrollment_token", id, nil), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "revoked"})
}

// enrollMSI creates an enrollment token and returns an MSI with it baked in.
// The admin hands the file to a technician: double-click → UAC → done.
func (s *Server) enrollMSI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SiteID   *string `json:"site_id"`
		MaxUses  *int    `json:"max_uses"`
		TTLHours int     `json:"ttl_hours"`
		Server   string  `json:"server"`
	}
	if !decode(w, r, &in) {
		return
	}
	server := strings.TrimRight(in.Server, "/")
	if server == "" {
		server = "https://" + r.Host
	}
	if u, err := url.Parse(server); err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "server must be https://host[:port]")
		return
	}
	var latest struct{ Version, File string }
	if b, err := os.ReadFile(filepath.Join(s.cfg.StorageDir, "agent", "latest.json")); err != nil || json.Unmarshal(b, &latest) != nil || latest.File == "" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "no agent build on the server — run scripts/build-agent.sh")
		return
	}
	pin, err := s.certPin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "cannot read server certificate")
		return
	}
	site := ""
	if in.SiteID != nil && *in.SiteID != "" {
		if err := s.pool.QueryRow(r.Context(), `SELECT name FROM sites WHERE id=$1`, *in.SiteID).Scan(&site); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "SWIM-API-422", "unknown site")
			return
		}
	}
	tokID, token, expires, err := s.newEnrollToken(r, in.SiteID, in.MaxUses, in.TTLHours, "msi")
	if err != nil {
		s.dbErr(w, err)
		return
	}
	dir, err := os.MkdirTemp("", "swim-msi-")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", err.Error())
		return
	}
	defer os.RemoveAll(dir)
	path, err := msi.Build(r.Context(), msi.Params{Exe: filepath.Join(s.cfg.StorageDir, "agent", filepath.Base(latest.File)),
		Version: latest.Version, Server: server, Pin: pin, Token: token, Site: site}, dir)
	if err != nil {
		// Don't leave a live token behind for an installer nobody received.
		_, _ = s.pool.Exec(r.Context(), `UPDATE enrollment_tokens SET expires_at=now() WHERE id=$1`, tokID)
		s.log.Error("msi build", "err", err)
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "MSI build failed: "+err.Error())
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", err.Error())
		return
	}
	defer f.Close()
	name := "swim-agent-" + latest.Version
	if clean := safeName(site); clean != "" {
		name += "-" + clean
	}
	w.Header().Set("Content-Type", "application/x-msi")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.msi"`)
	w.Header().Set("X-SWIM-Token-Id", tokID)
	w.Header().Set("X-SWIM-Expires", expires.UTC().Format(time.RFC3339))
	_, _ = io.Copy(w, f)
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// issueReimageTicket: one-use, host-bound ticket so a freshly reimaged Windows
// re-enrolls as the same host without an enrollment token. In the full deploy
// flow the server issues it with the go-token and swim-pe writes it into the
// new OS as C:\ProgramData\SWIM\bootstrap.json; this endpoint exposes it for
// tests and manual recovery.
func (s *Server) issueReimageTicket(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("id")
	var in struct {
		TTLHours int `json:"ttl_hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.TTLHours <= 0 || in.TTLHours > 72 {
		in.TTLHours = 24
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "rng")
		return
	}
	ticket := "swim_rt_" + hex.EncodeToString(raw)
	th := sha256.Sum256([]byte(ticket))
	expires := time.Now().Add(time.Duration(in.TTLHours) * time.Hour)
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO reimage_tickets(ticket_hash, host_id, expires_at) VALUES ($1,$2,$3)`, th[:], hostID, expires)
		return s.entry(r, "reimage_ticket.issue", "host", hostID, map[string]any{"expires_at": expires}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	pin, _ := s.certPin()
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": ticket, "expires_at": expires, "cert_sha256": pin})
}
