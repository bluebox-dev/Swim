//go:build !windows

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"swim/pkg/manifest"
)

type reports struct {
	mu   sync.Mutex
	last map[string]map[string]any
}

func newHarness(t *testing.T) (*execCtx, ed25519.PrivateKey, *reports) {
	t.Setenv("SWIM_AGENT_DIR", t.TempDir())
	rep := &reports{last: map[string]map[string]any{}}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/agent/commands/"), "/result")
		rep.mu.Lock()
		if body["status"] != "running" {
			rep.last[id] = body
		}
		rep.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(srv.Certificate().Raw)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	ec := &execCtx{c: NewClient(srv.URL, hex.EncodeToString(sum[:])), tok: "t",
		st: &State{HostID: "h_1", SmbiosUUID: "uuid-1"}, lg: log.New(io.Discard, "", 0), restart: make(chan struct{}, 1)}
	return ec, priv, rep
}

func signed(priv ed25519.PrivateKey, kid string, mod func(*manifest.Command)) wireCommand {
	now := time.Now().UTC()
	c := manifest.Command{Schema: 1, Kind: "command", CommandID: "cmd_1", Type: "ping", Args: json.RawMessage(`{}`),
		IssuedAt: now, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour)}
	c.Host.HostID, c.Host.SmbiosUUID = "h_1", "uuid-1"
	if mod != nil {
		mod(&c)
	}
	b, _ := json.Marshal(c)
	return wireCommand{ID: c.CommandID, Envelope: manifest.Sign(priv, kid, b)}
}

func run(t *testing.T, ec *execCtx, keys manifest.Keyset, rep *reports, wc wireCommand) map[string]any {
	t.Helper()
	_ = handleCommand(context.Background(), ec, &keys, loadSeen(), wc)
	rep.mu.Lock()
	defer rep.mu.Unlock()
	r := rep.last[wc.ID]
	delete(rep.last, wc.ID)
	return r
}

func TestCommandSecurity(t *testing.T) {
	ec, priv, rep := newHarness(t)
	keys := manifest.Keyset{"k1": priv.Public().(ed25519.PublicKey)}
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)

	if r := run(t, ec, keys, rep, signed(priv, "k1", nil)); r["status"] != "done" || r["output"] != "pong" {
		t.Fatalf("valid ping should run: %v", r)
	}
	if r := run(t, ec, keys, rep, signed(priv, "k1", nil)); r != nil {
		t.Fatalf("replayed command must not run again: %v", r)
	}
	cases := map[string]wireCommand{
		"forged signature":   signed(otherPriv, "k1", func(c *manifest.Command) { c.CommandID = "cmd_2" }),
		"unknown key":        signed(otherPriv, "k9", func(c *manifest.Command) { c.CommandID = "cmd_3" }),
		"other host":         signed(priv, "k1", func(c *manifest.Command) { c.CommandID = "cmd_4"; c.Host.HostID = "h_2" }),
		"other machine UUID": signed(priv, "k1", func(c *manifest.Command) { c.CommandID = "cmd_5"; c.Host.SmbiosUUID = "uuid-2" }),
		"expired":            signed(priv, "k1", func(c *manifest.Command) { c.CommandID = "cmd_6"; c.NotAfter = time.Now().Add(-time.Minute) }),
		"not allowlisted":    signed(priv, "k1", func(c *manifest.Command) { c.CommandID = "cmd_7"; c.Type = "run_shell" }),
		"script hash wrong": signed(priv, "k1", func(c *manifest.Command) {
			c.CommandID, c.Type = "cmd_8", "run_script"
			c.Args, _ = json.Marshal(manifest.ScriptArgs{Body: "echo pwned", SHA256: strings.Repeat("0", 64), TimeoutS: 10})
		}),
	}
	for name, wc := range cases {
		r := run(t, ec, keys, rep, wc)
		if r == nil || r["status"] != "failed" {
			t.Errorf("%s: must be refused, got %v", name, r)
		}
	}
	// id in the envelope must match the id the server delivered it under
	wc := signed(priv, "k1", func(c *manifest.Command) { c.CommandID = "cmd_9" })
	wc.ID = "cmd_10"
	if r := run(t, ec, keys, rep, wc); r == nil || r["status"] != "failed" {
		t.Errorf("id mismatch must be refused, got %v", r)
	}
	// approved script with the right hash runs and returns its output
	body := "echo hello-from-script"
	sum := sha256.Sum256([]byte(body))
	ok := signed(priv, "k1", func(c *manifest.Command) {
		c.CommandID, c.Type = "cmd_11", "run_script"
		c.Args, _ = json.Marshal(manifest.ScriptArgs{Body: body, SHA256: hex.EncodeToString(sum[:]), TimeoutS: 10})
	})
	if r := run(t, ec, keys, rep, ok); r["status"] != "done" || !strings.Contains(r["output"].(string), "hello-from-script") {
		t.Fatalf("approved script should run: %v", r)
	}
}
