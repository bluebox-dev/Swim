// Package api serves the v1 HTTP API (spec 11).
package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"swim/backend/internal/audit"
	"swim/backend/internal/signing"
	"swim/backend/internal/vm"
)

// Admin is one operator. Until SSO lands (phase 3) operators authenticate with
// a bearer token each, so two-person rules are real: two different tokens.
type Admin struct {
	Name   string
	UserID string
	token  [32]byte // sha256 of the token
	// sign-in method: "token" (bearer token from the server's .env) or "session" (account)
	Auth      string
	SessionID string
	AccountID string
}

type Config struct {
	StorageDir string
	Libvirt    string // libvirt URI, e.g. qemu:///system
	Version    string
	GuacSecret string // 32 hex chars shared with guacamole JSON_SECRET_KEY
	ISODir     string // where installer ISOs live
	SnapDir    string // NVRAM/TPM copies of VM snapshots
	CertFile   string // nginx TLS cert (PEM) — its SHA-256 is the agent pin
	HeartbeatS int    // agent heartbeat interval handed out at enroll
	// TwoPerson: image/script approval and clearing the global emergency stop need a
	// second admin. Off for a single-admin install (SWIM_TWO_PERSON=true turns it on).
	TwoPerson bool
	WakerSock string // Unix socket of the host-network Wake-on-LAN sender
}

type Server struct {
	pool    *pgxpool.Pool
	log     *slog.Logger
	key     *signing.Key
	admins  []Admin
	cfg     Config
	lv      vm.Libvirt
	events  *hub
	pres    *presence
	seenIP  sync.Map // host id → address its agent last connected from (differs from its own address behind NAT)
	started time.Time
}

func New(pool *pgxpool.Pool, log *slog.Logger, key *signing.Key, cfg Config) *Server {
	s := &Server{pool: pool, log: log, key: key, cfg: cfg, lv: vm.Libvirt{URI: cfg.Libvirt, SnapDir: cfg.SnapDir},
		events: newHub(), pres: newPresence(), started: time.Now()}
	go s.sampleOnlineLoop()
	go s.scheduleLoop()
	return s
}

// SetAdmins parses "name:token,name:token" and upserts the users + admin role.
func (s *Server) SetAdmins(ctx context.Context, spec string) error {
	for _, part := range strings.Split(spec, ",") {
		name, tok, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok || name == "" || len(tok) < 24 {
			return errors.New("SWIM_ADMIN_TOKENS: want name:token pairs, token ≥ 24 chars")
		}
		a := Admin{Name: name, UserID: "u_" + name, token: sha256.Sum256([]byte(tok))}
		if _, err := s.pool.Exec(ctx, `INSERT INTO users(id, subject, display_name) VALUES ($1,$2,$3)
			ON CONFLICT DO NOTHING`, a.UserID, "token:"+name, name); err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO user_roles(user_id, role, site_id) VALUES ($1,'admin',NULL)
			ON CONFLICT DO NOTHING`, a.UserID); err != nil {
			return err
		}
		s.admins = append(s.admins, a)
	}
	if len(s.admins) == 0 {
		return errors.New("SWIM_ADMIN_TOKENS is empty")
	}
	return nil
}

func (s *Server) adminByToken(tok string) *Admin {
	if tok == "" {
		return nil
	}
	h := sha256.Sum256([]byte(tok))
	var found *Admin
	for i := range s.admins {
		if subtle.ConstantTimeCompare(h[:], s.admins[i].token[:]) == 1 {
			found = &s.admins[i]
		}
	}
	return found
}

func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(v, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

type ctxKey int

const adminKey ctxKey = 1

func (s *Server) requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := s.adminByToken(bearer(r))
		if a != nil {
			c := *a
			c.Auth = "token"
			a = &c
		} else if a = s.adminBySession(r); a != nil {
			// cookie auth: changes need the custom header a cross-site form cannot send
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-SWIM") != "1" {
				writeErr(w, http.StatusForbidden, "SWIM-API-403", "missing X-SWIM header")
				return
			}
		}
		if a == nil {
			// A token was presented and it is wrong: that is a failed sign-in. It is logged with
			// its address, and 20 of them in 15 minutes stop that address (same limit as passwords).
			if bearer(r) != "" {
				ip := clientIP(r)
				if d := loginLimiter.blocked(ip); d > 0 {
					tooMany(w, d)
					return
				}
				loginLimiter.fail(ip)
				info := failInfo(r, "token", "", false)
				info["path"] = r.URL.Path
				s.authAudit(r.Context(), "token", "auth.login_failed", ip, "denied", info)
			}
			writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "กรุณาเข้าสู่ระบบ")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), adminKey, a)))
	}
}

func adminFrom(r *http.Request) *Admin { return r.Context().Value(adminKey).(*Admin) }

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	// public
	m.HandleFunc("GET /v1/health", s.health)
	m.HandleFunc("GET /metrics", s.metrics)
	m.HandleFunc("GET /v1/agent/keyset", s.keyset)
	m.HandleFunc("GET /v1/auth/config", s.authConfig)
	m.HandleFunc("POST /v1/auth/login", s.authLogin)
	m.HandleFunc("POST /v1/auth/totp", s.authTOTP)
	m.HandleFunc("POST /v1/auth/setup/start", s.authSetupStart)
	m.HandleFunc("POST /v1/auth/setup/finish", s.authSetupFinish)
	m.HandleFunc("POST /v1/auth/logout", s.authLogout)
	// agent
	m.HandleFunc("POST /v1/agent/enroll", s.enroll)
	m.HandleFunc("POST /v1/agent/heartbeat", s.heartbeat)
	m.HandleFunc("GET /v1/agent/commands", s.agentCommands)
	m.HandleFunc("GET /v1/agent/files/{path...}", s.agentFile)
	m.HandleFunc("POST /v1/agent/jobs/{id}/events", s.agentJobEvent)
	m.HandleFunc("POST /v1/agent/jobs/{id}/drivers", s.agentDrivers)
	m.HandleFunc("POST /v1/pe/jobs/{id}/events", s.peJobEvent)
	m.HandleFunc("GET /v1/pe/jobs/{id}/next", s.peNext)
	m.HandleFunc("GET /v1/pe/jobs/{id}/files/{path...}", s.peFile)
	m.HandleFunc("POST /v1/agent/commands/{id}/result", s.agentCommandResult)
	m.HandleFunc("GET /v1/agent/tunnel/{id}", s.agentTunnel)
	// admin
	a := s.requireAdmin
	m.HandleFunc("GET /v1/whoami", a(s.whoami))
	m.HandleFunc("GET /v1/users", a(s.listUsers))
	m.HandleFunc("POST /v1/users", a(s.createUser))
	m.HandleFunc("POST /v1/users/{id}/{action}", a(s.userAction))
	m.HandleFunc("POST /v1/me/password", a(s.mePassword))
	m.HandleFunc("POST /v1/me/recovery-codes", a(s.meRecoveryCodes))
	m.HandleFunc("GET /v1/me/sessions", a(s.meSessions))
	m.HandleFunc("DELETE /v1/me/sessions/{id}", a(s.meRevokeSession))
	m.HandleFunc("GET /v1/events", a(s.eventStream))
	m.HandleFunc("PATCH /v1/sites/{id}", a(s.updateSite))
	m.HandleFunc("DELETE /v1/sites/{id}", a(s.deleteSite))
	m.HandleFunc("POST /v1/hosts/site", a(s.assignSite))
	m.HandleFunc("POST /v1/batches", a(s.createBatch))
	m.HandleFunc("GET /v1/batches", a(s.listBatches))
	m.HandleFunc("GET /v1/batches/{id}", a(s.getBatch))
	m.HandleFunc("GET /v1/sites", a(s.listSites))
	m.HandleFunc("POST /v1/sites", a(s.createSite))
	m.HandleFunc("GET /v1/channels", a(s.listChannels))
	m.HandleFunc("POST /v1/channels", a(s.createChannel))
	m.HandleFunc("GET /v1/images", a(s.listImages))
	m.HandleFunc("POST /v1/images/import", a(s.importImage))
	m.HandleFunc("GET /v1/images/{id}", a(s.getImage))
	m.HandleFunc("DELETE /v1/images/{id}", a(s.deleteImage))
	m.HandleFunc("POST /v1/images/{id}/approve", a(s.approveImage))
	m.HandleFunc("GET /v1/hosts", a(s.listHosts))
	m.HandleFunc("GET /v1/hosts/{id}", a(s.getHost))
	m.HandleFunc("GET /v1/hosts/{id}/commands", a(s.listHostCommands))
	m.HandleFunc("POST /v1/hosts/{id}/commands", a(s.createCommand))
	m.HandleFunc("POST /v1/hosts/{id}/retire", a(s.removeHost))
	m.HandleFunc("POST /v1/hosts/commands/clear", a(s.clearCommandHistory))
	m.HandleFunc("POST /v1/hosts/wake", a(s.wakeHosts))
	m.HandleFunc("POST /v1/hosts/{id}/deploy", a(s.createDeploy))
	m.HandleFunc("GET /v1/hosts/{id}/jobs", a(s.listHostJobs))
	m.HandleFunc("GET /v1/jobs/{id}", a(s.getJob))
	m.HandleFunc("POST /v1/jobs/{id}/cancel", a(s.cancelJob))
	m.HandleFunc("POST /v1/jobs/{id}/retry", a(s.retryJob))
	m.HandleFunc("POST /v1/jobs/{id}/rehandoff", a(s.rehandoffJob))
	m.HandleFunc("DELETE /v1/jobs/{id}", a(s.deleteJob))
	m.HandleFunc("DELETE /v1/hosts/{id}/jobs", a(s.clearHostJobs))
	m.HandleFunc("DELETE /v1/hosts/{id}", a(s.deleteHost))
	m.HandleFunc("GET /v1/commands/{id}", a(s.getCommand))
	m.HandleFunc("POST /v1/commands/{id}/cancel", a(s.cancelCommand))
	m.HandleFunc("GET /v1/scripts", a(s.listScripts))
	m.HandleFunc("POST /v1/scripts", a(s.scriptsClosed))
	m.HandleFunc("POST /v1/tools/preview", a(s.toolPreview))
	m.HandleFunc("POST /v1/scripts/{id}/{action}", a(s.scriptAction))
	m.HandleFunc("POST /v1/enrollment-tokens", a(s.createEnrollToken))
	m.HandleFunc("GET /v1/enrollment-tokens", a(s.listEnrollTokens))
	m.HandleFunc("POST /v1/enrollment-tokens/{id}/revoke", a(s.revokeEnrollToken))
	m.HandleFunc("GET /v1/enrollment-tokens/{id}", a(s.enrollTokenStatus))
	m.HandleFunc("GET /v1/enroll/info", a(s.enrollInfo))
	m.HandleFunc("POST /v1/enroll/msi", a(s.enrollMSI))
	m.HandleFunc("POST /v1/hosts/{id}/reimage-ticket", a(s.issueReimageTicket))
	m.HandleFunc("GET /v1/stats/online", a(s.statsOnline))
	m.HandleFunc("GET /v1/capacity/server", a(s.capacity))
	m.HandleFunc("GET /v1/capacity/server/detail", a(s.capacityDetail))
	m.HandleFunc("POST /v1/capacity/server/cleanup", a(s.capacityCleanup))
	m.HandleFunc("POST /v1/hosts/{id}/remote", a(s.hostRemote))
	m.HandleFunc("PUT /v1/hosts/{id}/policy", a(s.putHostPolicy))
	m.HandleFunc("POST /v1/schedules", a(s.createSchedule))
	m.HandleFunc("GET /v1/schedules/occurrences", a(s.listOccurrences))
	m.HandleFunc("DELETE /v1/schedules/{id}", a(s.cancelSchedule))
	m.HandleFunc("DELETE /v1/schedules/{id}/occurrences/{occurrence}", a(s.skipOccurrence))
	m.HandleFunc("POST /v1/schedules/{id}/run-now", a(s.runScheduleNow))
	m.HandleFunc("POST /v1/hosts/policy", a(s.bulkHostPolicy))
	m.HandleFunc("GET /v1/builder/vms", a(s.listVMs))
	m.HandleFunc("GET /v1/builder/isos", a(s.listISOs))
	m.HandleFunc("POST /v1/builder/isos/{name}/inspect", a(s.isoInspect))
	m.HandleFunc("POST /v1/builder/isos/{name}/import", a(s.isoImport))
	m.HandleFunc("POST /v1/builder/vms/{name}/power/{action}", a(s.vmAction))
	m.HandleFunc("POST /v1/builder/vms/{name}/media", a(s.vmMedia))
	m.HandleFunc("POST /v1/builder/vms/{name}/console", a(s.vmConsole))
	m.HandleFunc("POST /v1/builder/vms/{name}/capture", a(s.vmCapture))
	m.HandleFunc("POST /v1/builder/vms/{name}/{op}", a(s.vmSnapshot))
	m.HandleFunc("GET /v1/tasks", a(s.listTasks))
	m.HandleFunc("GET /v1/tasks/{id}/log", a(s.taskLog))
	m.HandleFunc("GET /v1/audit", a(s.listAuditFiltered))
	m.HandleFunc("GET /v1/audit/verify", a(s.verifyAudit))
	m.HandleFunc("POST /v1/audit/archive", a(s.archiveAudit))
	m.HandleFunc("GET /v1/audit/archives", a(s.listAuditArchives))
	m.HandleFunc("GET /v1/audit/archives/{file}", a(s.downloadAuditArchive))
	return s.logRequests(m)
}

func (s *Server) logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		h.ServeHTTP(sw, r)
		if r.URL.Path != "/v1/health" && r.URL.Path != "/v1/agent/commands" && r.URL.Path != "/v1/agent/heartbeat" && r.URL.Path != "/v1/events" && !strings.HasPrefix(r.URL.Path, "/v1/agent/files/") && !strings.HasSuffix(r.URL.Path, "/next") {
			s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
				"ms", time.Since(start).Milliseconds(), "ip", clientIP(r))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }

// Hijack lets a handler take over the connection (the agent's remote tunnel) through the logging wrapper.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("connection cannot be taken over")
	}
	w.status = http.StatusSwitchingProtocols
	return hj.Hijack()
}

// Flush keeps Server-Sent Events working through the logging wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ───────── helpers ─────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	// answers can hold one-time links, recovery codes and tokens: never keep them in a browser or proxy cache
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	// an empty body counts as {} (e.g. actions whose fields are all optional)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// dbErr maps constraint violations to 409 and hides everything else.
func (s *Server) dbErr(w http.ResponseWriter, err error) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			writeErr(w, http.StatusConflict, "SWIM-API-409", "already exists ("+pe.ConstraintName+")")
			return
		case "23514":
			writeErr(w, http.StatusConflict, "SWIM-API-409", "rule violated ("+pe.ConstraintName+")")
			return
		case "23503":
			writeErr(w, http.StatusUnprocessableEntity, "SWIM-API-422", "unknown reference ("+pe.ConstraintName+")")
			return
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "SWIM-API-404", "not found")
		return
	}
	s.log.Error("db", "err", err)
	writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "internal error")
}

// inTx runs fn in a transaction and writes one audit entry on success.
func (s *Server) inTx(ctx context.Context, fn func(pgx.Tx) (audit.Entry, error)) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	e, err := fn(tx)
	if err != nil {
		return err
	}
	if e.Action != "" {
		if err := audit.Write(ctx, tx, e); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Server) entry(r *http.Request, action, otype, oid string, data any) audit.Entry {
	return audit.Entry{Actor: "user:" + adminFrom(r).Name, Action: action, ObjectType: otype, ObjectID: oid,
		IP: clientIP(r), Result: "ok", Data: data}
}
