package agent

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Enroll registers this machine with either an enrollment token (new machine,
// MSI or script) or a reimage ticket (same host coming back after SWIM wiped it).
func Enroll(server, pin, token, ticket string) (*State, error) {
	if server == "" || (token == "" && ticket == "") {
		return nil, errors.New("server and a token or reimage ticket are required")
	}
	inv, err := CollectInventory()
	if err != nil {
		return nil, fmt.Errorf("inventory: %w", err)
	}
	// X25519 key: the server seals per-job secrets (e.g. ODJ blobs) to it (spec 06 §4).
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	c := NewClient(server, pin)
	var out struct {
		HostID     string `json:"host_id"`
		HostToken  string `json:"host_token"`
		Reenrolled bool   `json:"reenrolled"`
		Config     struct {
			HeartbeatS int `json:"heartbeat_s"`
		} `json:"config"`
	}
	body := map[string]any{"x25519_pub": base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()), "inventory": inv}
	if ticket != "" {
		body["reimage_ticket"] = ticket
	} else {
		body["token"] = token
	}
	if err := c.PostJSON("/v1/agent/enroll", "", body, &out); err != nil {
		return nil, err
	}
	st := &State{Server: c.Server, Pin: pin, HostID: out.HostID, SmbiosUUID: strings.ToLower(inv.SmbiosUUID),
		HeartbeatS: out.Config.HeartbeatS, EnrolledAt: time.Now().UTC(), Reenrolled: out.Reenrolled, AgentVersion: Version}
	if st.SealedToken, err = seal([]byte(out.HostToken)); err != nil {
		return nil, fmt.Errorf("seal token: %w", err)
	}
	if st.SealedX25519, err = seal(priv.Bytes()); err != nil {
		return nil, fmt.Errorf("seal key: %w", err)
	}
	return st, st.Save()
}

// NewLogger writes to <DataDir>/logs/agent.log (rotated at 5 MB) and stdout.
func NewLogger() (*log.Logger, io.Closer) {
	dir := filepath.Join(DataDir(), "logs")
	_ = os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, "agent.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 5<<20 {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return log.New(os.Stdout, "", log.LstdFlags|log.LUTC), io.NopCloser(nil)
	}
	return log.New(io.MultiWriter(f, os.Stdout), "", log.LstdFlags|log.LUTC), f
}

// usableState returns the saved enrollment only if it belongs to this machine
// and its secrets still open. A Golden Master that was captured with an
// enrolled agent, or a disk moved to another PC, fails one of these checks:
// the state is set aside and the agent enrolls fresh instead of cloning an identity.
func usableState(lg *log.Logger, uuid string) *State {
	st, err := LoadState()
	if err != nil {
		if !errors.Is(err, ErrNotEnrolled) {
			lg.Printf("state unreadable: %v", err)
		}
		return nil
	}
	reason := ""
	if st.SmbiosUUID != "" && !strings.EqualFold(st.SmbiosUUID, uuid) {
		reason = fmt.Sprintf("state belongs to another machine (uuid %s, this is %s)", st.SmbiosUUID, uuid)
	} else if _, err := st.Token(); err != nil {
		reason = "sealed token cannot be opened on this Windows install (" + err.Error() + ")"
	}
	if reason != "" {
		aside := StatePath() + ".discarded-" + time.Now().UTC().Format("20060102-150405")
		_ = os.Rename(StatePath(), aside)
		lg.Printf("discarding enrollment: %s → %s", reason, filepath.Base(aside))
		return nil
	}
	if st.SmbiosUUID == "" && uuid != "" { // state from agent 0.1
		st.SmbiosUUID = strings.ToLower(uuid)
		_ = st.Save()
	}
	return st
}

// waitForEnrollment loops until the machine is enrolled, using whatever
// bootstrap material appears (MSI registry values or a reimage bootstrap.json).
func waitForEnrollment(ctx context.Context, lg *log.Logger) (*State, error) {
	// Clone guard needs this machine's UUID. Read it from firmware (no WMI), and
	// never fall back to "skip the check": an unknown machine waits instead.
	var uuid string
	for attempt := 1; ; attempt++ {
		u, err := firmwareUUID()
		if err == nil && u != "" {
			uuid = strings.ToLower(u)
			break
		}
		if attempt == 1 || attempt%10 == 0 {
			lg.Printf("cannot read SMBIOS UUID yet (%v) — retrying", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}
	if st := usableState(lg, uuid); st != nil {
		return st, nil
	}
	wait, fails, waitingLogged := time.Duration(0), 0, false
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		bs := FindBootstrap()
		if bs == nil {
			if !waitingLogged {
				lg.Printf("not enrolled and no bootstrap found — waiting (install the SWIM MSI or run swim-agent enroll)")
				waitingLogged = true
			}
			wait = time.Minute
			continue
		}
		kind := "enrollment token"
		if bs.Ticket != "" {
			kind = "reimage ticket"
		}
		st, err := Enroll(bs.Server, bs.Pin, bs.Token, bs.Ticket)
		if err == nil {
			if cerr := ClearBootstrap(bs); cerr != nil {
				lg.Printf("warning: could not remove used bootstrap (%s): %v", bs.Source, cerr)
			}
			how := "new machine"
			if st.Reenrolled {
				how = "same host, history kept"
			}
			lg.Printf("enrolled with %s from %s: host_id=%s (%s)", kind, bs.Source, st.HostID, how)
			return st, nil
		}
		fails++
		wait = min(time.Duration(fails)*30*time.Second, 5*time.Minute)
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 401 {
			wait = 15 * time.Minute // token expired/used up: retrying fast will not help
			lg.Printf("%s from %s rejected: %s — ask an admin for a new installer; retry in %s", kind, bs.Source, ae.Message, wait)
		} else {
			lg.Printf("enroll with %s failed (%d): %v — retry in %s", kind, fails, err, wait)
		}
	}
}

// powerSuspend is signalled by the Windows service handler on PBT_APMSUSPEND.
var powerSuspend = make(chan struct{}, 1)

// Run enrolls if needed, then sends heartbeats until ctx is cancelled.
// Errors back off (max 5 min) instead of exiting, so the service never stops on its own.
func Run(ctx context.Context, lg *log.Logger) error {
	lg.Printf("swim-agent %s starting", Version)
	platformInit(lg)
	st, err := waitForEnrollment(ctx, lg)
	if err != nil {
		return nil // context cancelled = service stop
	}
	tok, err := st.Token()
	if err != nil {
		return fmt.Errorf("unseal host token: %w", err)
	}
	c := NewClient(st.Server, st.Pin)
	interval := time.Duration(st.HeartbeatS) * time.Second
	if interval < 10*time.Second {
		interval = 60 * time.Second
	}
	lg.Printf("host=%s server=%s heartbeat=%s", st.HostID, st.Server, interval)
	go reportFinishedJob(c, tok, lg) // result of a reimage that finished while we were down
	go reportInterrupted(c, tok, lg) // commands that were running when the agent stopped
	go disableRemoteAccount(lg)      // a remote-session account left enabled across a restart

	// Command channel runs beside the heartbeat; a restart request ends Run.
	restart := make(chan struct{}, 1)
	invNow := make(chan struct{}, 1)
	cctx, stopCommands := context.WithCancel(ctx)
	defer stopCommands()
	inventoryKick = invNow
	pkgLog = lg
	go commandLoop(cctx, &execCtx{c: NewClient(st.Server, st.Pin), tok: tok, st: st, lg: lg, restart: restart, invNow: invNow})
	startPluginServices(cctx, NewClient(st.Server, st.Pin), tok, lg) // optional plugins that are linked in

	// Tell the server we are going away (shutdown, reboot, service stop, update) so the
	// machine shows offline at once instead of after the presence timeout.
	bye := func() {
		done := make(chan struct{})
		go func() {
			_ = c.PostJSON("/v1/agent/heartbeat", tok, map[string]any{"agent_version": Version, "state": "stopping"}, nil)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}

	// Inventory is checked continuously: collected every inventoryCheck and sent when
	// anything that matters changed (or at least every inventoryEvery), so the host page
	// stays current without anyone pressing "refresh".
	const (
		inventoryCheck = 2 * time.Minute
		inventoryEvery = 15 * time.Minute
	)
	var lastInv, lastSent time.Time
	var lastSig string
	wait, fails := time.Duration(0), 0
	for {
		select {
		case <-ctx.Done():
			lg.Printf("stopping")
			bye()
			return nil
		case <-restart:
			lg.Printf("restarting on server request")
			bye()
			return ErrRestart
		case <-invNow:
			lastInv, lastSig = time.Time{}, "" // collect and send now
		case <-powerSuspend:
			// Sleep, hibernate or Fast Startup "shutdown": services are frozen, not
			// stopped, so say goodbye now; the next poll after resume marks us online.
			lg.Printf("system is suspending")
			bye()
			continue
		case <-time.After(wait):
		}
		body := map[string]any{"agent_version": Version, "svc_version": Version, "state": "idle"}
		if time.Since(lastInv) > inventoryCheck {
			lastInv = time.Now()
			if d, err := CollectDetails(ctx); err == nil {
				if sig := inventorySignature(d); sig != lastSig || time.Since(lastSent) > inventoryEvery {
					body["inventory"] = d
					lastSig, lastSent = sig, time.Now()
				}
			} else {
				lg.Printf("inventory: %v", err)
			}
		}
		var out struct {
			Halt       bool   `json:"halt"`
			HaltReason string `json:"halt_reason"`
		}
		err := c.PostJSON("/v1/agent/heartbeat", tok, body, &out)
		if err == nil {
			fixClock(c, lg) // Skew() comes from this response's Date header
		}
		if err != nil {
			fails++
			wait = min(interval*time.Duration(1<<min(fails, 3)), 5*time.Minute)
			var ae *APIError
			if errors.As(err, &ae) && ae.Status == 401 {
				lg.Printf("heartbeat rejected: host token unknown to server — re-enroll this machine")
			} else {
				lg.Printf("heartbeat failed (%d in a row): %v — retry in %s", fails, err, wait)
			}
			st.LastError = err.Error()
			_ = st.Save()
			continue
		}
		if fails > 0 {
			lg.Printf("heartbeat ok again after %d failures", fails)
		}
		if out.Halt != st.Halt {
			if out.Halt {
				lg.Printf("KILL SWITCH active: %s — no new jobs will start", out.HaltReason)
			} else {
				lg.Printf("kill switch cleared")
			}
		}
		fails, wait = 0, interval
		st.LastHeartbeat, st.LastError, st.Halt, st.HaltReason, st.AgentVersion = time.Now().UTC(), "", out.Halt, out.HaltReason, Version
		_ = st.Save()
	}
}

// inventorySignature identifies the parts of the inventory worth reporting at once:
// everything except values that drift every minute (clock, uptime, free memory) and
// free disk space rounded to whole GB.
func inventorySignature(raw []byte) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	delete(m, "collected_at")
	delete(m, "uptime_s")
	if rdp, ok := m["rdp"].(map[string]any); ok {
		delete(rdp, "checked_at")
	}
	if pre, ok := m["prestage"].(map[string]any); ok {
		delete(pre, "updated_at")
	}
	if pol, ok := m["policy"].(map[string]any); ok {
		if il, ok := pol["install_lock"].(map[string]any); ok {
			delete(il, "checked_at")
		}
	}
	dropPluginVolatile(m)
	if mem, ok := m["memory"].(map[string]any); ok {
		delete(mem, "free")
	}
	if disks, ok := m["disks"].([]any); ok {
		for _, d := range disks {
			if dm, ok := d.(map[string]any); ok {
				if f, ok := dm["free"].(float64); ok {
					dm["free"] = int64(f) >> 30
				}
			}
		}
	}
	if st, ok := m["staging"].(map[string]any); ok {
		if f, ok := st["free_bytes"].(float64); ok {
			st["free_bytes"] = int64(f) >> 30
		}
	}
	b, _ := json.Marshal(m) // map keys are sorted, so equal content gives equal bytes
	return string(b)
}

// inventoryKick asks the heartbeat loop to collect and send inventory now (set by Run).
var inventoryKick chan<- struct{}

func requestInventory() {
	if inventoryKick == nil {
		return
	}
	select {
	case inventoryKick <- struct{}{}:
	default:
	}
}

// pkgLog is the agent log for background work started outside Run's loop (set by Run).
var pkgLog interface{ Printf(string, ...any) }
