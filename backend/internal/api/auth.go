package api

// Web sign-in: username + password + authenticator app (TOTP), server-side sessions.
//
// Public:   GET /v1/auth/config, POST /v1/auth/login, /v1/auth/totp, /v1/auth/setup/start,
//           /v1/auth/setup/finish, /v1/auth/logout
// Signed in: GET /v1/users, POST /v1/users, POST /v1/users/{id}/{reset-2fa|disable|enable},
//           POST /v1/me/password, POST /v1/me/recovery-codes, GET /v1/me/sessions,
//           DELETE /v1/me/sessions/{id}
//
// Session cookie swim_session: HttpOnly, Secure, SameSite=Strict; 12 h absolute, 2 h idle,
// stored server-side (hash only) so sign-out and revocation are immediate. Requests that
// change something with cookie auth must carry "X-SWIM: 1" (CSRF guard). The bearer tokens
// in the server's .env keep working for scripts and as break-glass.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"
	"rsc.io/qr"

	"swim/backend/internal/audit"
	"swim/backend/internal/ids"
)

const (
	sessionCookie   = "swim_session"
	sessionAbsolute = 12 * time.Hour
	sessionIdle     = 2 * time.Hour
	challengeTTL    = 5 * time.Minute
	setupTTL        = 24 * time.Hour
	maxFailures     = 5
	lockFor         = 15 * time.Minute
)

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)

// ───────── primitives ─────────

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sha(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	h := argon2.IDKey([]byte(pw), salt, 2, 64*1024, 2, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=2,p=2$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h))
}

func checkPassword(stored, pw string) bool {
	f := strings.Split(stored, "$")
	if len(f) != 6 || f[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(f[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(f[4])
	want, err2 := base64.RawStdEncoding.DecodeString(f[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

var dummyHash = hashPassword("swim-timing-equaliser")

func newRecoveryCodes() (codes []string, hashes []string) {
	const abc = "abcdefghjkmnpqrstuvwxyz23456789"
	for i := 0; i < 10; i++ {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		for j := range b {
			b[j] = abc[int(b[j])%len(abc)]
		}
		c := string(b[:4]) + "-" + string(b[4:])
		codes = append(codes, c)
		hashes = append(hashes, hex.EncodeToString(sha(c)))
	}
	return
}

func qrSVG(text string) string {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	n, q := c.Size, 4
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="100%%" height="100%%" fill="#fff"/><path fill="#000" d="`, n+2*q, n+2*q)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+q, y+q)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

// per-IP failure limiter (in memory): 20 failures in 15 minutes → wait
type ipLimiter struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

var loginLimiter = &ipLimiter{m: map[string][]time.Time{}}

func (l *ipLimiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	var keep []time.Time
	for _, t := range l.m[ip] {
		if time.Since(t) < lockFor {
			keep = append(keep, t)
		}
	}
	l.m[ip] = keep
	if len(keep) >= 20 {
		return lockFor - time.Since(keep[0])
	}
	return 0
}

func (l *ipLimiter) fail(ip string) {
	l.mu.Lock()
	if len(l.m) > 50000 { // a flood from many addresses must not grow this map without bound
		for k, v := range l.m {
			if len(v) == 0 || time.Since(v[len(v)-1]) > lockFor {
				delete(l.m, k)
			}
		}
	}
	l.m[ip] = append(l.m[ip], time.Now())
	l.mu.Unlock()
}

// Password hashing costs 64 MB each (Argon2id). hashSlots lets only a few run at once, so
// a burst of sign-in requests cannot use up the server's memory; the rest wait their turn.
var hashSlots = make(chan struct{}, 4)

func acquireHash(ctx context.Context) bool {
	select {
	case hashSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-time.After(10 * time.Second):
		return false
	}
}

func releaseHash() { <-hashSlots }

// attemptLimiter counts every sign-in request per address (right or wrong): 30 in a minute → wait.
type attemptLimiter struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

var loginAttempts = &attemptLimiter{m: map[string][]time.Time{}}

func (l *attemptLimiter) over(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 50000 {
		l.m = map[string][]time.Time{}
	}
	var keep []time.Time
	for _, t := range l.m[ip] {
		if time.Since(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	keep = append(keep, time.Now())
	l.m[ip] = keep
	return len(keep) > 30
}

func (s *Server) authAudit(ctx context.Context, actor, action, ip, result string, data map[string]any) {
	_ = s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
		return audit.Entry{Actor: actor, Action: action, ObjectType: "user", ObjectID: strings.TrimPrefix(actor, "user:"), IP: ip, Result: result, Data: data}, nil
	})
}

// failInfo is what a failed sign-in leaves in the activity log: the name that was typed,
// the address and browser it came from, whether that account exists and how many failures
// this address has had in the last 15 minutes. The password that was typed is never kept:
// a wrong password is very often the right one with a typo, or the person's password for
// another system, and the log is read by every administrator and copied into archives.
func failInfo(r *http.Request, step, username string, known bool) map[string]any {
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	return map[string]any{"step": step, "username": username, "known_user": known, "user_agent": ua,
		"failures_from_ip": loginLimiter.count(clientIP(r))}
}

func (l *ipLimiter) count(ip string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, t := range l.m[ip] {
		if time.Since(t) < lockFor {
			n++
		}
	}
	return n
}

func tooMany(w http.ResponseWriter, d time.Duration) {
	secs := int(d.Seconds()) + 1
	w.Header().Set("Retry-After", fmt.Sprint(secs))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{"code": "SWIM-API-429",
		"message": fmt.Sprintf("ลองผิดหลายครั้งเกินไป กรุณารอ %d นาที", (secs+59)/60)}, "retry_after": secs})
}

// ───────── users and sessions ─────────

type adminUser struct {
	ID, Username, DisplayName string
	PasswordHash              *string
	TOTPSecret                []byte
	TOTPConfirmed             *time.Time
	LastStep                  int64
	Recovery                  []string
	Disabled                  *time.Time
	Failed                    int
	LockedUntil               *time.Time
}

const userCols = `id, username, display_name, password_hash, totp_secret, totp_confirmed_at, totp_last_step, recovery_hashes, disabled_at, failed_count, locked_until`

func scanUser(row pgx.Row) (adminUser, error) {
	var u adminUser
	var rec []byte
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.TOTPSecret, &u.TOTPConfirmed, &u.LastStep, &rec, &u.Disabled, &u.Failed, &u.LockedUntil)
	_ = json.Unmarshal(rec, &u.Recovery)
	return u, err
}

// ensureUserRow keeps the users table (referenced by images, rollouts …) in step.
func (s *Server) ensureUserRow(ctx context.Context, username, display string) {
	_, _ = s.pool.Exec(ctx, `INSERT INTO users(id, subject, display_name) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
		"u_"+username, "local:"+username, display)
	_, _ = s.pool.Exec(ctx, `INSERT INTO user_roles(user_id, role, site_id) VALUES ($1,'admin',NULL) ON CONFLICT DO NOTHING`, "u_"+username)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u adminUser) error {
	tok := randHex(32)
	sid := ids.New("ses_")
	if _, err := s.pool.Exec(r.Context(), `INSERT INTO admin_sessions(id, user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1,$2,$3,$4,$5,$6)`, sid, u.ID, sha(tok), time.Now().Add(sessionAbsolute), clientIP(r), r.UserAgent()); err != nil {
		return err
	}
	_, _ = s.pool.Exec(r.Context(), `UPDATE admin_users SET last_login_at=now(), failed_count=0, locked_until=NULL WHERE id=$1`, u.ID)
	s.ensureUserRow(r.Context(), u.Username, u.DisplayName)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionAbsolute.Seconds())})
	return nil
}

// adminBySession resolves the session cookie (and slides its idle timer).
func (s *Server) adminBySession(r *http.Request) *Admin {
	c, err := r.Cookie(sessionCookie)
	if err != nil || len(c.Value) != 64 {
		return nil
	}
	var sid, uid, username, display string
	var last time.Time
	err = s.pool.QueryRow(r.Context(), `SELECT s.id, u.id, u.username, u.display_name, s.last_seen_at FROM admin_sessions s
		JOIN admin_users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > now() AND s.last_seen_at > now() - make_interval(secs => $2)
		  AND u.disabled_at IS NULL`, sha(c.Value), sessionIdle.Seconds()).Scan(&sid, &uid, &username, &display, &last)
	if err != nil {
		return nil
	}
	if time.Since(last) > time.Minute {
		_, _ = s.pool.Exec(r.Context(), `UPDATE admin_sessions SET last_seen_at=now() WHERE id=$1`, sid)
	}
	return &Admin{Name: username, UserID: "u_" + username, Auth: "session", SessionID: sid, AccountID: uid}
}

func (s *Server) usersExist(ctx context.Context) bool {
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_users WHERE totp_confirmed_at IS NOT NULL AND disabled_at IS NULL`).Scan(&n)
	return n > 0
}

func (s *Server) newChallenge(ctx context.Context, userID, next string) (string, error) {
	tok := randHex(24)
	_, _ = s.pool.Exec(ctx, `DELETE FROM auth_challenges WHERE expires_at < now()`)
	_, err := s.pool.Exec(ctx, `INSERT INTO auth_challenges(token_hash, user_id, next, expires_at) VALUES ($1,$2,$3,$4)`,
		sha(tok), userID, next, time.Now().Add(challengeTTL))
	return tok, err
}

// takeChallenge reads a challenge; consume=true deletes it (single use).
func (s *Server) takeChallenge(ctx context.Context, tok, next string, consume bool) (adminUser, bool) {
	var uid string
	if err := s.pool.QueryRow(ctx, `SELECT user_id FROM auth_challenges WHERE token_hash=$1 AND next=$2 AND expires_at > now()`,
		sha(tok), next).Scan(&uid); err != nil {
		return adminUser{}, false
	}
	if consume {
		_, _ = s.pool.Exec(ctx, `DELETE FROM auth_challenges WHERE token_hash=$1`, sha(tok))
	}
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM admin_users WHERE id=$1 AND disabled_at IS NULL`, uid))
	return u, err == nil
}

func (s *Server) userBySetupToken(ctx context.Context, tok string) (adminUser, bool) {
	if tok == "" {
		return adminUser{}, false
	}
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM admin_users WHERE setup_token_hash=$1 AND setup_expires_at > now()
		AND disabled_at IS NULL`, sha(tok)))
	return u, err == nil
}

// ───────── public endpoints ─────────

func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	exist := s.usersExist(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"users_exist": exist, "token_login": !exist})
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if !decode(w, r, &in) {
		return
	}
	ip := clientIP(r)
	if d := loginLimiter.blocked(ip); d > 0 {
		tooMany(w, d)
		return
	}
	if loginAttempts.over(ip) {
		tooMany(w, time.Minute)
		return
	}
	name := strings.ToLower(strings.TrimSpace(in.Username))
	if len(name) > 64 || len(in.Password) > 1024 { // nothing valid is this long; keep it out of the log and the hasher
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}
	u, err := scanUser(s.pool.QueryRow(r.Context(), `SELECT `+userCols+` FROM admin_users WHERE username=$1`, name))
	if err == nil && u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
		tooMany(w, time.Until(*u.LockedUntil))
		return
	}
	if !acquireHash(r.Context()) {
		w.Header().Set("Retry-After", "5")
		writeErr(w, http.StatusServiceUnavailable, "SWIM-API-503", "ระบบกำลังตรวจรหัสผ่านหลายรายการ ลองใหม่อีกครั้งในไม่กี่วินาที")
		return
	}
	ok := err == nil && u.Disabled == nil && u.PasswordHash != nil && checkPassword(*u.PasswordHash, in.Password)
	if err != nil || u.PasswordHash == nil {
		checkPassword(dummyHash, in.Password) // same time whether or not the user exists
	}
	releaseHash()
	if !ok {
		loginLimiter.fail(ip)
		if err == nil {
			s.countFailure(r.Context(), u, ip)
		}
		s.authAudit(r.Context(), "user:"+name, "auth.login_failed", ip, "denied", failInfo(r, "password", name, err == nil))
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}
	// An account whose authenticator was reset is set up again only through the one-time
	// link an administrator hands over. Otherwise the password alone would be enough to
	// attach a new authenticator, and the second factor would protect nothing.
	if u.TOTPConfirmed == nil {
		s.authAudit(r.Context(), "user:"+name, "auth.login_failed", ip, "denied", failInfo(r, "setup_required", name, true))
		writeErr(w, http.StatusForbidden, "SWIM-API-403", "บัญชีนี้ต้องตั้งค่าแอปยืนยันตัวตนใหม่ ขอลิงก์ตั้งค่าจากผู้ดูแลระบบ")
		return
	}
	next := "totp"
	ch, err := s.newChallenge(r.Context(), u.ID, next)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"challenge": ch, "next": next})
}

func (s *Server) countFailure(ctx context.Context, u adminUser, ip string) {
	if u.Failed+1 >= maxFailures {
		_, _ = s.pool.Exec(ctx, `UPDATE admin_users SET failed_count=0, locked_until=$2 WHERE id=$1`, u.ID, time.Now().Add(lockFor))
		s.authAudit(ctx, "user:"+u.Username, "auth.lockout", ip, "locked", map[string]any{"minutes": int(lockFor.Minutes())})
		return
	}
	_, _ = s.pool.Exec(ctx, `UPDATE admin_users SET failed_count=failed_count+1 WHERE id=$1`, u.ID)
}

func (s *Server) authTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge    string `json:"challenge"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := clientIP(r)
	if d := loginLimiter.blocked(ip); d > 0 {
		tooMany(w, d)
		return
	}
	u, ok := s.takeChallenge(r.Context(), in.Challenge, "totp", false)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "หมดเวลายืนยันตัวตน กรุณาเข้าสู่ระบบใหม่")
		return
	}
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
		tooMany(w, time.Until(*u.LockedUntil))
		return
	}
	good, method := false, "totp"
	if in.RecoveryCode != "" {
		method = "recovery_code"
		h := hex.EncodeToString(sha(strings.ToLower(strings.TrimSpace(in.RecoveryCode))))
		for i, x := range u.Recovery {
			if subtle.ConstantTimeCompare([]byte(x), []byte(h)) == 1 {
				u.Recovery = append(u.Recovery[:i:i], u.Recovery[i+1:]...)
				b, _ := json.Marshal(u.Recovery)
				_, _ = s.pool.Exec(r.Context(), `UPDATE admin_users SET recovery_hashes=$2::jsonb WHERE id=$1`, u.ID, string(b))
				good = true
				break
			}
		}
	} else if secret, err := s.openSecret(u.TOTPSecret); err == nil {
		if st := totpCheck([]byte(secret), in.Code, time.Now(), u.LastStep); st > 0 {
			_, _ = s.pool.Exec(r.Context(), `UPDATE admin_users SET totp_last_step=$2 WHERE id=$1`, u.ID, st)
			good = true
		}
	}
	if !good {
		loginLimiter.fail(ip)
		s.countFailure(r.Context(), u, ip)
		s.authAudit(r.Context(), "user:"+u.Username, "auth.login_failed", ip, "denied", failInfo(r, method, u.Username, true))
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "รหัสไม่ถูกต้อง")
		return
	}
	_, _ = s.pool.Exec(r.Context(), `DELETE FROM auth_challenges WHERE token_hash=$1`, sha(in.Challenge))
	if err := s.startSession(w, r, u); err != nil {
		s.dbErr(w, err)
		return
	}
	s.authAudit(r.Context(), "user:"+u.Username, "auth.login", ip, "ok", map[string]any{"method": method, "recovery_left": len(u.Recovery), "user_agent": failInfo(r, "", "", true)["user_agent"]})
	writeJSON(w, http.StatusOK, map[string]any{"user": map[string]string{"username": u.Username, "display_name": u.DisplayName},
		"recovery_left": len(u.Recovery)})
}

func (s *Server) authSetupStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge  string `json:"challenge"`
		SetupToken string `json:"setup_token"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, ok := s.userBySetupToken(r.Context(), in.SetupToken)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "ลิงก์ตั้งค่าหมดอายุหรือใช้ไปแล้ว")
		return
	}
	if u.TOTPConfirmed != nil {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "บัญชีนี้ตั้งค่าแอปยืนยันตัวตนแล้ว")
		return
	}
	raw := make([]byte, 20)
	_, _ = rand.Read(raw)
	sealed, err := s.sealSecret(string(raw))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "cannot protect the secret: "+err.Error())
		return
	}
	if _, err := s.pool.Exec(r.Context(), `UPDATE admin_users SET totp_secret=$2, totp_last_step=0 WHERE id=$1`, u.ID, sealed); err != nil {
		s.dbErr(w, err)
		return
	}
	secret := b32.EncodeToString(raw)
	uri := fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=SWIM&algorithm=SHA1&digits=6&period=30",
		url.PathEscape("SWIM:"+u.Username), secret)
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "otpauth_uri": uri, "qr_svg": qrSVG(uri), "username": u.Username,
		"password_required": true}) // the link holder always chooses a new password
}

func (s *Server) authSetupFinish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge  string `json:"challenge"`
		SetupToken string `json:"setup_token"`
		Code       string `json:"code"`
		Password   string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	ip := clientIP(r)
	if d := loginLimiter.blocked(ip); d > 0 {
		tooMany(w, d)
		return
	}
	u, ok := s.userBySetupToken(r.Context(), in.SetupToken)
	viaToken := ok
	if !ok {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "ลิงก์ตั้งค่าหมดอายุหรือใช้ไปแล้ว")
		return
	}
	if u.TOTPConfirmed != nil || len(u.TOTPSecret) == 0 {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "เริ่มตั้งค่าใหม่ (สแกน QR อีกครั้ง)")
		return
	}
	if viaToken && (len(in.Password) < 10 || len(in.Password) > 200) {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "ตั้งรหัสผ่านอย่างน้อย 10 ตัวอักษร")
		return
	}
	if len(in.Code) > 16 {
		in.Code = ""
	}
	secret, err := s.openSecret(u.TOTPSecret)
	st := int64(0)
	if err == nil {
		st = totpCheck([]byte(secret), in.Code, time.Now(), 0)
	}
	if st == 0 {
		loginLimiter.fail(ip)
		s.authAudit(r.Context(), "user:"+u.Username, "auth.login_failed", ip, "denied", failInfo(r, "setup_code", u.Username, true))
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "รหัสจากแอปไม่ถูกต้อง ตรวจว่าเวลาในมือถือตรง แล้วลองรหัสใหม่")
		return
	}
	codes, hashes := newRecoveryCodes()
	hb, _ := json.Marshal(hashes)
	q := `UPDATE admin_users SET totp_confirmed_at=now(), totp_last_step=$2, recovery_hashes=$3::jsonb, setup_token_hash=NULL,
		setup_expires_at=NULL WHERE id=$1`
	args := []any{u.ID, st, string(hb)}
	if viaToken {
		q = strings.Replace(q, "WHERE", ", password_hash=$4 WHERE", 1)
		args = append(args, hashPassword(in.Password))
	}
	if _, err := s.pool.Exec(r.Context(), q, args...); err != nil {
		s.dbErr(w, err)
		return
	}
	_, _ = s.pool.Exec(r.Context(), `DELETE FROM auth_challenges WHERE user_id=$1`, u.ID)
	if err := s.startSession(w, r, u); err != nil {
		s.dbErr(w, err)
		return
	}
	s.authAudit(r.Context(), "user:"+u.Username, "auth.setup", ip, "ok", map[string]any{"via": map[bool]string{true: "setup_link", false: "login"}[viaToken]})
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes, "user": map[string]string{"username": u.Username, "display_name": u.DisplayName}})
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		var uname string
		_ = s.pool.QueryRow(r.Context(), `UPDATE admin_sessions s SET revoked_at=now() FROM admin_users u
			WHERE s.token_hash=$1 AND u.id=s.user_id AND s.revoked_at IS NULL RETURNING u.username`, sha(c.Value)).Scan(&uname)
		if uname != "" {
			s.authAudit(r.Context(), "user:"+uname, "auth.logout", clientIP(r), "ok", nil)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ───────── account management (signed in) ─────────

func (s *Server) newSetupLink(ctx context.Context, userID string) (string, error) {
	tok := randHex(24)
	_, err := s.pool.Exec(ctx, `UPDATE admin_users SET setup_token_hash=$2, setup_expires_at=$3 WHERE id=$1`, userID, sha(tok), time.Now().Add(setupTTL))
	return "/#/setup?t=" + tok, err
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT u.id, u.username, u.display_name, u.totp_confirmed_at IS NOT NULL, u.disabled_at,
		u.locked_until, u.last_login_at, u.created_at, COALESCE(u.created_by,''), jsonb_array_length(u.recovery_hashes),
		u.setup_expires_at > now(), (SELECT count(*) FROM admin_sessions s WHERE s.user_id=u.id AND s.revoked_at IS NULL AND s.expires_at > now())
		FROM admin_users u ORDER BY u.username`)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var id, un, dn, cb string
		var ready bool
		var dis, lock, last *time.Time
		var created time.Time
		var rec, sessions int
		var pendingSetup *bool
		err := row.Scan(&id, &un, &dn, &ready, &dis, &lock, &last, &created, &cb, &rec, &pendingSetup, &sessions)
		return map[string]any{"id": id, "username": un, "display_name": dn, "totp_ready": ready, "disabled_at": dis,
			"locked_until": lock, "last_login_at": last, "created_at": created, "created_by": cb, "recovery_left": rec,
			"setup_pending": pendingSetup != nil && *pendingSetup, "active_sessions": sessions}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !usernameRe.MatchString(in.Username) {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "ชื่อผู้ใช้: a-z 0-9 . _ - 2 ถึง 32 ตัว")
		return
	}
	id := ids.New("usr_")
	link, err := "", error(nil)
	err = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `INSERT INTO admin_users(id, username, display_name, created_by) VALUES ($1,$2,$3,$4)`,
			id, in.Username, strings.TrimSpace(in.DisplayName), adminFrom(r).Name)
		return s.entry(r, "user.create", "user", in.Username, map[string]any{"display_name": in.DisplayName}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	if link, err = s.newSetupLink(r.Context(), id); err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "username": in.Username, "setup_link": link, "expires_in_h": int(setupTTL.Hours())})
}

func (s *Server) userAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	me := adminFrom(r)
	var username string
	var disabled *time.Time
	if err := s.pool.QueryRow(r.Context(), `SELECT username, disabled_at FROM admin_users WHERE id=$1`, id).Scan(&username, &disabled); err != nil {
		s.dbErr(w, err)
		return
	}
	if username == me.Name && me.Auth == "session" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "ทำกับบัญชีของตัวเองไม่ได้ ให้ผู้ดูแลคนอื่นทำ")
		return
	}
	out := map[string]any{"id": id, "username": username}
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		switch action {
		case "disable":
			var others int
			_ = tx.QueryRow(r.Context(), `SELECT count(*) FROM admin_users WHERE id<>$1 AND disabled_at IS NULL AND totp_confirmed_at IS NOT NULL`, id).Scan(&others)
			if others == 0 {
				return audit.Entry{}, errStatus{"ปิดบัญชีสุดท้ายที่ใช้งานได้ไม่ได้"}
			}
			if _, err := tx.Exec(r.Context(), `UPDATE admin_users SET disabled_at=now() WHERE id=$1`, id); err != nil {
				return audit.Entry{}, err
			}
			_, err := tx.Exec(r.Context(), `UPDATE admin_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id)
			return s.entry(r, "user.disable", "user", username, nil), err
		case "enable":
			_, err := tx.Exec(r.Context(), `UPDATE admin_users SET disabled_at=NULL, failed_count=0, locked_until=NULL WHERE id=$1`, id)
			return s.entry(r, "user.enable", "user", username, nil), err
		case "reset-2fa":
			if _, err := tx.Exec(r.Context(), `UPDATE admin_users SET totp_secret=NULL, totp_confirmed_at=NULL, totp_last_step=0,
				recovery_hashes='[]', failed_count=0, locked_until=NULL WHERE id=$1`, id); err != nil {
				return audit.Entry{}, err
			}
			_, err := tx.Exec(r.Context(), `UPDATE admin_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id)
			return s.entry(r, "user.reset_2fa", "user", username, nil), err
		}
		return audit.Entry{}, errStatus{"unknown action"}
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
	if action == "reset-2fa" {
		link, err := s.newSetupLink(r.Context(), id)
		if err != nil {
			s.dbErr(w, err)
			return
		}
		out["setup_link"], out["expires_in_h"] = link, int(setupTTL.Hours())
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) meUser(w http.ResponseWriter, r *http.Request) (adminUser, bool) {
	me := adminFrom(r)
	if me.Auth != "session" {
		writeErr(w, http.StatusConflict, "SWIM-API-409", "ใช้ได้เฉพาะเมื่อเข้าสู่ระบบด้วยบัญชีผู้ใช้")
		return adminUser{}, false
	}
	u, err := scanUser(s.pool.QueryRow(r.Context(), `SELECT `+userCols+` FROM admin_users WHERE id=$1`, me.AccountID))
	if err != nil {
		s.dbErr(w, err)
		return u, false
	}
	return u, true
}

func (s *Server) mePassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Current, New string }
	if !decode(w, r, &in) {
		return
	}
	u, ok := s.meUser(w, r)
	if !ok {
		return
	}
	if len(in.Current) > 1024 || u.PasswordHash == nil || !checkPassword(*u.PasswordHash, in.Current) {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "รหัสผ่านปัจจุบันไม่ถูกต้อง")
		return
	}
	if len(in.New) < 10 || len(in.New) > 200 {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "รหัสผ่านใหม่อย่างน้อย 10 ตัวอักษร")
		return
	}
	me := adminFrom(r)
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		if _, err := tx.Exec(r.Context(), `UPDATE admin_users SET password_hash=$2 WHERE id=$1`, u.ID, hashPassword(in.New)); err != nil {
			return audit.Entry{}, err
		}
		// other devices sign in again
		_, err := tx.Exec(r.Context(), `UPDATE admin_sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL`, u.ID, me.SessionID)
		return s.entry(r, "user.password_change", "user", u.Username, nil), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) meRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code string }
	if !decode(w, r, &in) {
		return
	}
	u, ok := s.meUser(w, r)
	if !ok {
		return
	}
	secret, err := s.openSecret(u.TOTPSecret)
	st := int64(0)
	if err == nil {
		st = totpCheck([]byte(secret), in.Code, time.Now(), u.LastStep)
	}
	if st == 0 {
		writeErr(w, http.StatusUnauthorized, "SWIM-API-401", "รหัสจากแอปไม่ถูกต้อง")
		return
	}
	codes, hashes := newRecoveryCodes()
	hb, _ := json.Marshal(hashes)
	err = s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		_, err := tx.Exec(r.Context(), `UPDATE admin_users SET recovery_hashes=$2::jsonb, totp_last_step=$3 WHERE id=$1`, u.ID, string(hb), st)
		return s.entry(r, "user.recovery_codes", "user", u.Username, nil), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (s *Server) meSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := s.meUser(w, r)
	if !ok {
		return
	}
	me := adminFrom(r)
	rows, err := s.pool.Query(r.Context(), `SELECT id, created_at, last_seen_at, expires_at, COALESCE(ip,''), COALESCE(user_agent,'')
		FROM admin_sessions WHERE user_id=$1 AND revoked_at IS NULL AND expires_at > now()
		AND last_seen_at > now() - make_interval(secs => $2) ORDER BY last_seen_at DESC`, u.ID, sessionIdle.Seconds())
	if err != nil {
		s.dbErr(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (map[string]any, error) {
		var id, ip, ua string
		var c, l, e time.Time
		err := row.Scan(&id, &c, &l, &e, &ip, &ua)
		return map[string]any{"id": id, "created_at": c, "last_seen_at": l, "expires_at": e, "ip": ip, "user_agent": ua, "current": id == me.SessionID}, err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) meRevokeSession(w http.ResponseWriter, r *http.Request) {
	u, ok := s.meUser(w, r)
	if !ok {
		return
	}
	sid := r.PathValue("id")
	err := s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		tag, err := tx.Exec(r.Context(), `UPDATE admin_sessions SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, sid, u.ID)
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
		return s.entry(r, "auth.session_revoke", "user", u.Username, map[string]any{"session_id": sid}), err
	})
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// CLI (ssh, inside the backend container): bootstrap and break-glass.
//
//	swim-server user add <username> [display name]
//	swim-server user reset-2fa <username>
func (s *Server) CLIUser(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", errors.New("usage: user add <username> [display name] | user reset-2fa <username>")
	}
	name := strings.ToLower(args[1])
	switch args[0] {
	case "add":
		if !usernameRe.MatchString(name) {
			return "", errors.New("username: a-z 0-9 . _ - (2–32)")
		}
		id := ids.New("usr_")
		display := strings.Join(args[2:], " ")
		if _, err := s.pool.Exec(ctx, `INSERT INTO admin_users(id, username, display_name, created_by) VALUES ($1,$2,$3,'cli')`, id, name, display); err != nil {
			return "", err
		}
		link, err := s.newSetupLink(ctx, id)
		s.authAudit(ctx, "cli", "user.create", "", "ok", map[string]any{"username": name})
		return link, err
	case "reset-2fa":
		var id string
		if err := s.pool.QueryRow(ctx, `UPDATE admin_users SET totp_secret=NULL, totp_confirmed_at=NULL, totp_last_step=0, recovery_hashes='[]',
			failed_count=0, locked_until=NULL, disabled_at=NULL WHERE username=$1 RETURNING id`, name).Scan(&id); err != nil {
			return "", fmt.Errorf("no such user %s", name)
		}
		_, _ = s.pool.Exec(ctx, `UPDATE admin_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id)
		link, err := s.newSetupLink(ctx, id)
		s.authAudit(ctx, "cli", "user.reset_2fa", "", "ok", map[string]any{"username": name})
		return link, err
	}
	return "", errors.New("usage: user add <username> [display name] | user reset-2fa <username>")
}
