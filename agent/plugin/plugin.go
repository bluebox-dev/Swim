// Package plugin is the contract between the SWIM agent and optional plugins: everything a
// plugin's agent half may use, and nothing else. A plugin lives outside the core
// (plugins/<name>/agent), registers from its init function, and is in the agent binary only
// when agent/cmd/swim-agent/plugins_gen.go imports it.
package plugin

import (
	"context"
	"os/exec"
	"path/filepath"
	"time"

	"swim/agent/internal/agent"
)

type (
	// Plugin is the agent half of a plugin; see the field comments in the agent package.
	Plugin = agent.Plugin
	// Env is what a plugin's Service gets: a way to post to the server, and the agent's log.
	Env = agent.PluginEnv
)

// Register adds a plugin. Call it from the plugin package's init function.
func Register(p Plugin) { agent.RegisterPlugin(p) }

// Version is the agent's version.
func Version() string { return agent.Version }

// DataDir is a folder for this plugin's files under the agent's own (SYSTEM and
// Administrators only). It is removed with the agent.
func DataDir(name string) string { return filepath.Join(agent.DataDir(), name) }

// PowerShell runs a script as SYSTEM. Never build a script from text a user typed without
// checking it first.
func PowerShell(ctx context.Context, timeout time.Duration, script string) (string, error) {
	return agent.PowerShell(ctx, timeout, script)
}

// ConsoleUser reports who is signed in at the machine's own screen and in which session.
func ConsoleUser(ctx context.Context) (user string, session int, ok bool) {
	return agent.ConsoleUser(ctx)
}

// Exec builds a command the way the agent runs its own helpers.
func Exec(ctx context.Context, name string, args ...string) *exec.Cmd {
	return agent.Exec(ctx, name, args...)
}

// StatusOf returns the HTTP status of an error from Env.Post; 0 when the server was not reached.
func StatusOf(err error) int { return agent.StatusOf(err) }

// RequestInventory makes the agent report its inventory now.
func RequestInventory() { agent.RequestInventory() }
