// Package agent is the SWIM client agent (v0.2: enroll via MSI/registry or
// reimage ticket, heartbeat, Windows service, clone guard).
//
// Not in this version yet (spec 03/04): swim-svc supervisor + self-update,
// mTLS client cert, command channel, preflight, staging, deploy.
package agent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const Version = "0.5.40"

// State is persisted to <DataDir>/state.json. Secrets are sealed with
// Protect (DPAPI on Windows) and stored base64-encoded.
type State struct {
	Server        string    `json:"server"`
	Pin           string    `json:"cert_sha256"`
	HostID        string    `json:"host_id"`
	SmbiosUUID    string    `json:"smbios_uuid"` // machine this state belongs to (clone guard)
	SealedToken   string    `json:"host_token_sealed"`
	SealedX25519  string    `json:"x25519_sealed"`
	HeartbeatS    int       `json:"heartbeat_s"`
	EnrolledAt    time.Time `json:"enrolled_at"`
	Reenrolled    bool      `json:"reenrolled"`
	LastHeartbeat time.Time `json:"last_heartbeat,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	Halt          bool      `json:"halt"`
	HaltReason    string    `json:"halt_reason,omitempty"`
	AgentVersion  string    `json:"agent_version"`
}

var ErrNotEnrolled = errors.New("not enrolled — run: swim-agent enroll --server URL --token TOKEN --pin SHA256")

func StatePath() string { return filepath.Join(DataDir(), "state.json") }

func LoadState() (*State, error) {
	b, err := os.ReadFile(StatePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.HostID == "" {
		return nil, ErrNotEnrolled
	}
	return &s, nil
}

// Save writes atomically (tmp + rename) so a crash never leaves half a file.
func (s *State) Save() error {
	if err := os.MkdirAll(DataDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := StatePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, StatePath())
}

func seal(b []byte) (string, error) {
	out, err := Protect(b)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

func unseal(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return Unprotect(b)
}

// Token returns the host token in clear text (in memory only).
func (s *State) Token() (string, error) {
	b, err := unseal(s.SealedToken)
	return string(b), err
}
