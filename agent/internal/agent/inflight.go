package agent

// In-flight commands: a small file per running command in <DataDir>\inflight, written
// after "running" is reported and removed after the result is sent. If the agent (or the
// machine) restarts in between, the next start reports the command as interrupted
// instead of leaving it "running" on the server forever. Reimage is skipped: its job
// reports its own outcome (reportFinishedJob / reportMissedHandoff).

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"swim/pkg/manifest"
)

type inflightRec struct {
	ID      string    `json:"id"`
	Type    string    `json:"type"`
	Started time.Time `json:"started"`
}

func inflightDir() string { return filepath.Join(DataDir(), "inflight") }

func markInflight(cmd manifest.Command) {
	if cmd.Type == "reimage" || !safeID(cmd.CommandID) {
		return
	}
	_ = os.MkdirAll(inflightDir(), 0o700)
	b, _ := json.Marshal(inflightRec{ID: cmd.CommandID, Type: cmd.Type, Started: time.Now().UTC()})
	_ = os.WriteFile(filepath.Join(inflightDir(), cmd.CommandID+".json"), b, 0o600)
}

func clearInflight(id string) {
	if safeID(id) {
		_ = os.Remove(filepath.Join(inflightDir(), id+".json"))
	}
}

// safeID keeps command ids usable as file names (they are server-generated "cmd_…").
func safeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// reportInterrupted runs once at agent start.
func reportInterrupted(c *Client, tok string, lg interface{ Printf(string, ...any) }) {
	files, _ := filepath.Glob(filepath.Join(inflightDir(), "*.json"))
	for _, f := range files {
		var rec inflightRec
		b, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(b, &rec) != nil || !safeID(rec.ID) || rec.Type == "reimage" {
			_ = os.Remove(f)
			continue
		}
		body := map[string]any{"status": "failed", "output": "", "exit_code": nil, "data": nil,
			"error": "interrupted: agent restarted while the command was running (started " + rec.Started.Format(time.RFC3339) + ")"}
		err = c.PostJSON("/v1/agent/commands/"+rec.ID+"/result", tok, body, nil)
		var ae *APIError
		if err != nil && !(errors.As(err, &ae) && ae.Status < 500) {
			lg.Printf("command %s: could not report interruption yet: %v", rec.ID, err)
			continue // keep the file, try again at the next start
		}
		lg.Printf("command %s (%s): reported interrupted", rec.ID, rec.Type)
		_ = os.Remove(f)
	}
}
