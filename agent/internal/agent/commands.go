package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"swim/pkg/manifest"
)

// ErrRestart asks the service wrapper to exit so SCM's recovery action restarts us.
var ErrRestart = errors.New("restart requested by server")

type execCtx struct {
	c       *Client
	tok     string
	st      *State
	lg      *log.Logger
	restart chan<- struct{}
	invNow  chan<- struct{} // ask the heartbeat loop to send full inventory now
	staged  string          // verified new agent binary, swapped in after reporting
}

// ───────── replay protection: each command id runs at most once ─────────

type seenStore struct {
	mu  sync.Mutex
	IDs []string `json:"ids"`
}

func seenPath() string { return filepath.Join(DataDir(), "commands-seen.json") }

func loadSeen() *seenStore {
	s := &seenStore{}
	if b, err := os.ReadFile(seenPath()); err == nil {
		_ = json.Unmarshal(b, s)
	}
	return s
}

// claim records id before execution (at-most-once even across a crash).
func (s *seenStore) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.IDs {
		if x == id {
			return false
		}
	}
	s.IDs = append(s.IDs, id)
	if len(s.IDs) > 500 {
		s.IDs = s.IDs[len(s.IDs)-500:]
	}
	b, _ := json.Marshal(s)
	_ = os.WriteFile(seenPath(), b, 0o600)
	return true
}

// ───────── keyset (server's command-signing keys) ─────────

func fetchKeyset(c *Client) (manifest.Keyset, error) {
	var out struct {
		Keys []struct{ Kid, Alg, Pub string } `json:"keys"`
	}
	if err := c.GetJSON("/v1/agent/keyset", "", &out); err != nil {
		return nil, err
	}
	ks := manifest.Keyset{}
	for _, k := range out.Keys {
		if b, err := base64.RawURLEncoding.DecodeString(k.Pub); err == nil && len(b) == ed25519.PublicKeySize && k.Alg == manifest.AlgEd25519 {
			ks[k.Kid] = ed25519.PublicKey(b)
		}
	}
	if len(ks) == 0 {
		return nil, errors.New("server keyset is empty")
	}
	return ks, nil
}

// ───────── loop ─────────

type wireCommand struct {
	ID       string            `json:"id"`
	Envelope manifest.Envelope `json:"envelope"`
}

func commandLoop(ctx context.Context, ec *execCtx) {
	seen := loadSeen()
	var keys manifest.Keyset
	fails := 0
	for ctx.Err() == nil {
		if keys == nil {
			k, err := fetchKeyset(ec.c)
			if err != nil {
				ec.lg.Printf("commands: cannot load server keyset: %v", err)
				sleepCtx(ctx, time.Minute)
				continue
			}
			keys = k
		}
		var items []wireCommand
		err := ec.c.GetJSON("/v1/agent/commands?wait=20", ec.tok, &items)
		if err != nil {
			fails++
			wait := min(time.Duration(fails)*15*time.Second, 5*time.Minute)
			var ae *APIError
			if errors.As(err, &ae) && ae.Status == 401 {
				wait = 5 * time.Minute
			}
			if fails == 1 || fails%10 == 0 {
				ec.lg.Printf("commands: poll failed (%d): %v", fails, err)
			}
			sleepCtx(ctx, wait)
			continue
		}
		fails = 0
		for _, it := range items {
			if errors.Is(handleCommand(ctx, ec, &keys, seen, it), manifest.ErrUnknownKey) {
				keys = nil // rotated key: refetch, the server will not resend this one
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (ec *execCtx) report(id, status, output string, exit *int, data any, errText string) {
	body := map[string]any{"status": status, "output": output, "exit_code": exit, "data": data, "error": errText}
	for attempt := 0; attempt < 5; attempt++ {
		err := ec.c.PostJSON("/v1/agent/commands/"+id+"/result", ec.tok, body, nil)
		var ae *APIError
		if err == nil || (errors.As(err, &ae) && ae.Status < 500) {
			return
		}
		time.Sleep(time.Duration(attempt+1) * 3 * time.Second)
	}
}

// handleCommand verifies a command end to end before running it:
// Ed25519 signature → kind/id/host/UUID binding → validity window (server clock) →
// allowlist → at-most-once. Any failure is reported and logged, nothing runs.
func handleCommand(ctx context.Context, ec *execCtx, keys *manifest.Keyset, seen *seenStore, it wireCommand) error {
	payload, err := keys.Verify(it.Envelope)
	if err != nil {
		ec.lg.Printf("SECURITY: rejected command %s: %v", it.ID, err)
		ec.report(it.ID, "failed", "", nil, nil, "agent rejected command: "+err.Error())
		return err
	}
	var cmd manifest.Command
	if err := json.Unmarshal(payload, &cmd); err != nil {
		ec.report(it.ID, "failed", "", nil, nil, "agent rejected command: bad payload")
		return err
	}
	now := ec.c.Now()
	reject := ""
	switch {
	case cmd.Schema != 1 || cmd.Kind != "command":
		reject = "not a command payload"
	case cmd.CommandID != it.ID:
		reject = "command id mismatch"
	case cmd.Host.HostID != ec.st.HostID:
		reject = "command is for another host"
	case cmd.Host.SmbiosUUID != "" && ec.st.SmbiosUUID != "" && !strings.EqualFold(cmd.Host.SmbiosUUID, ec.st.SmbiosUUID):
		reject = "command is for another machine (UUID)"
	case now.Before(cmd.NotBefore) || now.After(cmd.NotAfter):
		reject = fmt.Sprintf("outside validity window (server time %s)", now.UTC().Format(time.RFC3339))
	case manifest.CommandTypes[cmd.Type] == "":
		reject = "command type not allowed: " + cmd.Type
	}
	if reject != "" {
		ec.lg.Printf("SECURITY: rejected command %s (%s): %s", it.ID, cmd.Type, reject)
		ec.report(it.ID, "failed", "", nil, nil, "agent rejected command: "+reject)
		return nil
	}
	if !seen.claim(cmd.CommandID) {
		ec.lg.Printf("SECURITY: replayed command %s ignored", cmd.CommandID)
		return nil
	}
	ec.lg.Printf("command %s: %s by %s", cmd.CommandID, cmd.Type, cmd.Actor)
	ec.report(cmd.CommandID, "running", "", nil, nil, "")
	markInflight(cmd)

	out, data, exit, err := execute(ctx, ec, cmd)
	status, errText := "done", ""
	if err != nil {
		status, errText = "failed", err.Error()
	}
	ec.lg.Printf("command %s: %s", cmd.CommandID, status)
	ec.report(cmd.CommandID, status, out, exit, data, errText)
	clearInflight(cmd.CommandID)

	switch cmd.Type {
	case "run_script", "remote_session", "release_staging", "reboot", "set_policy", "sign_out_stale", "prestage_image":
		// these can change what the host page shows (Remote Desktop, accounts, disks…):
		// report the new state now instead of at the next 2-minute check
		requestInventory()
	}
	if err == nil {
		switch cmd.Type {
		case "restart_agent":
			select {
			case ec.restart <- struct{}{}:
			default:
			}
		case "update_agent":
			ec.lg.Printf("switching to the new agent binary")
			if err := osApplyUpdate(ec.staged); err != nil {
				ec.lg.Printf("update launch failed: %v", err)
			}
		case "refresh_inventory", "delete_staged_image":
			select {
			case ec.invNow <- struct{}{}:
			default:
			}
		case "uninstall_agent":
			ec.lg.Printf("uninstalling SWIM agent from this machine")
			if err := osUninstall(); err != nil {
				ec.lg.Printf("uninstall launch failed: %v", err)
			}
		}
	}
	return nil
}

func execute(ctx context.Context, ec *execCtx, cmd manifest.Command) (string, any, *int, error) {
	if h := pluginCommand(cmd.Type); h != nil { // a command type of a linked plugin
		out, data, err := h(ctx, cmd.Args)
		requestInventory()
		return out, data, nil, err
	}
	switch cmd.Type {
	case "ping":
		host, _ := os.Hostname()
		return "pong", map[string]any{"agent_version": Version, "hostname": host,
			"local_time": time.Now().Format(time.RFC3339), "clock_skew_s": int(ec.c.Skew().Seconds())}, nil, nil
	case "collect_logs":
		var b strings.Builder
		b.WriteString(sysInfo(ctx))
		b.WriteString("\n==== SWIM agent log (last 64 KB) ====\n")
		b.WriteString(tailFile(filepath.Join(DataDir(), "logs", "agent.log"), 64<<10))
		return b.String(), nil, nil, nil
	case "refresh_inventory":
		d, err := CollectDetails(ctx)
		if err != nil {
			return "", nil, nil, err
		}
		var sum struct {
			Hostname, Vendor, Model string
			OS                      struct{ Caption, Build string } `json:"os"`
		}
		_ = json.Unmarshal(d, &sum)
		return fmt.Sprintf("%s · %s %s · %s build %s", sum.Hostname, sum.Vendor, sum.Model, sum.OS.Caption, sum.OS.Build), d, nil, nil
	case "update_agent":
		var a manifest.UpdateArgs
		if err := json.Unmarshal(cmd.Args, &a); err != nil || a.File == "" || len(a.SHA256) != 64 || strings.ContainsAny(a.File, `/\`) {
			return "", nil, nil, errors.New("invalid update arguments")
		}
		exe, err := os.Executable()
		if err != nil {
			return "", nil, nil, err
		}
		dst := filepath.Join(filepath.Dir(exe), "swim-agent.new"+filepath.Ext(exe))
		sum, n, err := ec.c.Download("/dl/agent/"+a.File, dst)
		if err != nil {
			os.Remove(dst)
			return "", nil, nil, fmt.Errorf("download: %w", err)
		}
		if !strings.EqualFold(sum, a.SHA256) {
			os.Remove(dst)
			return "", nil, nil, fmt.Errorf("SHA-256 mismatch (got %s) — update refused", sum[:16])
		}
		if out, err := command(ctx, dst, "version").Output(); err != nil || !strings.Contains(string(out), a.Version) {
			os.Remove(dst)
			return "", nil, nil, fmt.Errorf("new binary does not report version %s", a.Version)
		}
		ec.staged = dst
		return fmt.Sprintf("downloaded %s (%d bytes), SHA-256 verified, reports version %s — switching from %s now (auto-rollback if it does not start)",
			a.File, n, a.Version, Version), nil, nil, nil
	case "restart_agent":
		return "agent will restart in a few seconds", nil, nil, nil
	case "reboot":
		var a manifest.RebootArgs
		if err := json.Unmarshal(cmd.Args, &a); err != nil || a.DelayS < 60 {
			return "", nil, nil, errors.New("invalid reboot arguments")
		}
		out, err := osReboot(a.DelayS, a.Message)
		return out, nil, nil, err
	case "run_script":
		var a manifest.ScriptArgs
		if err := json.Unmarshal(cmd.Args, &a); err != nil {
			return "", nil, nil, errors.New("invalid script arguments")
		}
		sum := sha256.Sum256([]byte(a.Body))
		if hex.EncodeToString(sum[:]) != strings.ToLower(a.SHA256) {
			return "", nil, nil, errors.New("script body does not match its approved SHA-256")
		}
		timeout := time.Duration(min(max(a.TimeoutS, 10), 3600)) * time.Second
		out, code, err := runScript(ctx, cmd.CommandID, a.Body, timeout)
		if err == nil && code != 0 {
			err = fmt.Errorf("script exited with code %d", code)
		}
		return out, nil, &code, err
	case "reimage":
		out, err := startReimage(ctx, ec, cmd)
		return out, nil, nil, err
	case "delete_staged_image":
		var a manifest.DeleteStagedImageArgs
		if err := json.Unmarshal(cmd.Args, &a); err != nil {
			return "", nil, nil, errors.New("invalid arguments")
		}
		out, err := deleteStagedImage(a.Version)
		return out, nil, nil, err
	case "set_policy":
		var a manifest.SetPolicyArgs
		if err := json.Unmarshal(cmd.Args, &a); err != nil {
			return "", nil, nil, errors.New("invalid arguments")
		}
		out, data, err := setPolicy(ctx, a)
		return out, data, nil, err
	case "sign_out_stale":
		out, data, err := signOutStale(ctx)
		return out, data, nil, err
	case "prestage_image":
		var a manifest.PrestageArgs
		if err := json.Unmarshal(cmd.Args, &a); err != nil {
			return "", nil, nil, errors.New("invalid arguments")
		}
		out, err := prestageImage(ec, a)
		return out, nil, nil, err
	case "rehandoff":
		var a manifest.RehandoffArgs
		if err := json.Unmarshal(cmd.Args, &a); err != nil || a.JobID == "" {
			return "", nil, nil, errors.New("invalid arguments")
		}
		out, err := rehandoff(ctx, ec, a.JobID)
		return out, nil, nil, err
	case "release_staging":
		var ra manifest.ReleaseStagingArgs
		_ = json.Unmarshal(cmd.Args, &ra)
		if ra.Force {
			clearAbortedJob()
		}
		out, err := releaseStaging(ctx)
		if err == nil {
			select {
			case ec.invNow <- struct{}{}:
			default:
			}
		}
		return out, nil, nil, err
	case "remote_session":
		var a manifest.RemoteSessionArgs
		if err := json.Unmarshal(cmd.Args, &a); err != nil {
			return "", nil, nil, errors.New("invalid arguments")
		}
		out, data, err := remoteSession(ctx, ec.st, a, ec.lg)
		if err == nil && a.Tunnel != "" {
			ttl := time.Duration(min(max(a.TTLS, 120), 3600)) * time.Second
			runTunnel(ec.c, ec.tok, a.Tunnel, "127.0.0.1:3389", time.Now().Add(ttl), ec.lg)
			out += "; reachable through the agent's tunnel (the machine is behind a router)"
		}
		return out, data, nil, err
	case "wake":
		var a manifest.WakeArgs
		if err := json.Unmarshal(cmd.Args, &a); err != nil {
			return "", nil, nil, errors.New("invalid arguments")
		}
		out, err := wake(a.MACs)
		return out, nil, nil, err
	case "uninstall_agent":
		return "uninstall scheduled — the agent, its service and its data will be removed", nil, nil, nil
	}
	return "", nil, nil, errors.New("unsupported command")
}

func deref(b *bool) any {
	if b == nil {
		return "unknown"
	}
	return *b
}

func tailFile(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return "(no log: " + err.Error() + ")"
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
	}
	b, _ := io.ReadAll(f)
	return string(b)
}
