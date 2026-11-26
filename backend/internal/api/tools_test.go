package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPsqQuotes(t *testing.T) {
	for in, want := range map[string]string{
		"plain":         "'plain'",
		"it's":          "'it''s'",
		"a\u2019; calc": "'a\u2019\u2019; calc'", // typographic quote also ends a PowerShell string
		"\u2018x\u201B": "'\u2018\u2018x\u201B\u201B'",
	} {
		if got := psq(in); got != want {
			t.Errorf("psq(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestEveryToolBuilds(t *testing.T) {
	good := map[string]map[string]any{
		"shutdown": {"t": "300", "msg": "IT จะปิดเครื่องนี้"}, "msg": {"sec": "60", "text": "hello"},
		"useradd": {"user": "student1", "full": "Student One", "pass": "Temp-Pass-123", "admin": "on"},
		"userpw":  {"user": "student1", "pass": "Temp-Pass-123"}, "userstate": {"user": "student1", "on": "1"},
		"uninstall": {"app": "7-Zip"}, "kill": {"p": "chrome.exe"}, "service": {"svc": "Spooler", "act": "Restart", "st": ""},
		"rename": {"nn": "lab-01", "rb": "on"}, "time": {"tz": "UTC", "sync": "on"}, "ping": {"to": "8.8.8.8", "port": "443"},
		"hiber": {"on": "off"},
	}
	for id := range tools {
		ts, msg := buildTool(id, good[id])
		if ts == nil {
			t.Errorf("%s: %s", id, msg)
			continue
		}
		if ts.Name == "" || ts.Body == "" || ts.Timeout < 10 {
			t.Errorf("%s: empty name/body/timeout", id)
		}
	}
}

func TestToolSecretsAreHidden(t *testing.T) {
	ts, msg := buildTool("useradd", map[string]any{"user": "u1", "pass": "Sup3r-Secret!", "admin": true})
	if ts == nil {
		t.Fatal(msg)
	}
	if !strings.Contains(ts.Body, "Sup3r-Secret!") || strings.Contains(ts.Shown, "Sup3r-Secret!") || ts.Params["pass"] != "********" {
		t.Fatalf("password handling wrong: shown=%q params=%v", ts.Shown, ts.Params)
	}
	if !strings.Contains(ts.Body, "Add-LocalGroupMember -Group Administrators -Member 'u1'") {
		t.Fatal("admin flag ignored")
	}
}

func TestToolsRefuseBadInput(t *testing.T) {
	bad := []struct {
		id string
		p  map[string]any
	}{
		{"nope", nil},
		{"shutdown", map[string]any{"t": "5; calc.exe", "msg": "x"}},
		{"service", map[string]any{"svc": "Spooler", "act": "Remove-Item C:\\ -Recurse;", "st": ""}},
		{"service", map[string]any{"svc": "a'; calc; '", "act": "Stop", "st": ""}},
		{"rename", map[string]any{"nn": "x'; calc"}},
		{"useradd", map[string]any{"user": "a b", "pass": "12345678"}},
		{"useradd", map[string]any{"user": "ok", "pass": "short"}},
		{"ping", map[string]any{"to": "8.8.8.8; calc", "port": ""}},
		{"ping", map[string]any{"to": "8.8.8.8", "port": "99999"}},
		{"kill", map[string]any{"p": "x'; calc"}},
		{"msg", map[string]any{"sec": "60", "text": ""}},
		{"time", map[string]any{"tz": "Mars"}},
	}
	for _, c := range bad {
		if ts, _ := buildTool(c.id, c.p); ts != nil {
			t.Errorf("%s %v: accepted, body=%q", c.id, c.p, ts.Body)
		}
	}
	// free text is allowed anywhere, but can never leave its quotes
	ts, _ := buildTool("msg", map[string]any{"sec": "60", "text": "it's \u2019 fine"})
	if ts == nil || !strings.Contains(ts.Body, "'it''s \u2019\u2019 fine'") {
		t.Fatalf("quoting: %+v", ts)
	}
}

func TestScriptRequestRefusesScriptText(t *testing.T) {
	if _, _, ce := scriptRequest(json.RawMessage(`{"name":"x","body":"calc.exe"}`)); ce == nil || ce.status != 403 {
		t.Fatalf("script text accepted: %+v", ce)
	}
	if _, _, ce := scriptRequest(json.RawMessage(`{"tool":"dns","body":"calc.exe"}`)); ce == nil {
		t.Fatal("script text accepted next to a tool")
	}
	out, ts, ce := scriptRequest(json.RawMessage(`{"tool":"dns"}`))
	if ce != nil || ts == nil || !strings.Contains(string(out), "ipconfig.exe /flushdns") {
		t.Fatalf("tool not built: %s %+v", out, ce)
	}
	if out, ts, ce := scriptRequest(json.RawMessage(`{"script_id":"scr_1"}`)); ce != nil || ts != nil || string(out) != `{"script_id":"scr_1"}` {
		t.Fatalf("saved script: %s", out)
	}
	if _, _, ce := scriptRequest(json.RawMessage(`{}`)); ce == nil {
		t.Fatal("empty request accepted")
	}
}

// TestToolsMatchFormerBrowserScripts: testdata/tools_fixtures.json was produced by the
// script generators that used to live in the web page (frontend/app.js, before they moved
// here). Every tool must build exactly the same name, script and timeout.
func TestToolsMatchFormerBrowserScripts(t *testing.T) {
	b, err := os.ReadFile("testdata/tools_fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Tool    string         `json:"tool"`
		Params  map[string]any `json:"params"`
		Name    string         `json:"name"`
		Body    string         `json:"body"`
		Timeout int            `json:"timeout_s"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		seen[c.Tool] = true
		ts, msg := buildTool(c.Tool, c.Params)
		if ts == nil {
			t.Errorf("%s %v: refused: %s", c.Tool, c.Params, msg)
			continue
		}
		if ts.Name != c.Name || ts.Body != c.Body || ts.Timeout != c.Timeout {
			t.Errorf("%s %v differs\n got name %q timeout %d\nwant name %q timeout %d\n got body:\n%s\nwant body:\n%s", c.Tool, c.Params, ts.Name, ts.Timeout, c.Name, c.Timeout, ts.Body, c.Body)
		}
	}
	addedLater := map[string]bool{"uefi-check": true} // tools written on the server, never generated in the browser
	for id := range tools {
		if !seen[id] && !addedLater[id] {
			t.Errorf("tool %s has no fixture", id)
		}
	}
}
