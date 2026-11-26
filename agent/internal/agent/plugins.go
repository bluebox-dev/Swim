package agent

// Optional plugins inside the agent. A plugin is a package outside the core
// (plugins/<name>/agent) that registers itself from init through swim/agent/plugin. It is
// part of the agent only when the generated file agent/cmd/swim-agent/plugins_gen.go imports
// it; otherwise none of its code is in the binary.

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"swim/pkg/manifest"
)

// Plugin is the agent half of a plugin.
type Plugin struct {
	Name string
	// Commands handles the plugin's signed command types. They pass the same checks as every
	// command (signature, machine, validity window, at-most-once) before a handler runs.
	Commands map[string]func(ctx context.Context, args json.RawMessage) (output string, data any, err error)
	// Inventory is reported to the server as inventory.plugins.<name>.
	Inventory func(ctx context.Context) any
	// Volatile lists top-level keys of that value that change all the time (a timestamp, a
	// counter). They are still sent, but a change in them alone does not cause a new report.
	Volatile []string
	// Service runs beside the agent's own loops for as long as the agent runs. It must recover
	// from its own failures: a plugin cannot be allowed to stop the agent.
	Service func(ctx context.Context, env PluginEnv)
	// Subcommands are extra "swim-agent <name>" modes, for helpers the plugin starts itself.
	Subcommands map[string]func(args []string) int
}

// PluginEnv is what a plugin's Service gets.
type PluginEnv struct {
	// Post sends JSON to the server as this machine. Use paths under /v1/agent/<name>/.
	Post func(path string, in, out any) error
	Log  *log.Logger
}

var (
	pluginMu sync.Mutex
	plugins  []Plugin
)

// RegisterPlugin adds a plugin; called from its init function.
func RegisterPlugin(p Plugin) {
	pluginMu.Lock()
	defer pluginMu.Unlock()
	for t := range p.Commands {
		if _, taken := manifest.CommandTypes[t]; taken {
			panic("agent plugin " + p.Name + ": command type " + t + " is taken")
		}
		manifest.CommandTypes[t] = "change" // the agent only needs to know the type is allowed
	}
	plugins = append(plugins, p)
}

func pluginCommand(typ string) func(context.Context, json.RawMessage) (string, any, error) {
	pluginMu.Lock()
	defer pluginMu.Unlock()
	for _, p := range plugins {
		if h, ok := p.Commands[typ]; ok {
			return h
		}
	}
	return nil
}

// PluginSubcommand returns the handler of an extra "swim-agent <name>" mode, or nil.
func PluginSubcommand(name string) func([]string) int {
	pluginMu.Lock()
	defer pluginMu.Unlock()
	for _, p := range plugins {
		if h, ok := p.Subcommands[name]; ok {
			return h
		}
	}
	return nil
}

// pluginInventory collects what each plugin reports; nil when no plugin reports anything.
func pluginInventory(ctx context.Context) map[string]any {
	pluginMu.Lock()
	list := append([]Plugin(nil), plugins...)
	pluginMu.Unlock()
	var out map[string]any
	for _, p := range list {
		if p.Inventory == nil {
			continue
		}
		func() {
			defer func() { _ = recover() }()
			if v := p.Inventory(ctx); v != nil {
				if out == nil {
					out = map[string]any{}
				}
				out[p.Name] = v
			}
		}()
	}
	return out
}

// dropPluginVolatile removes the keys plugins marked as volatile from a decoded inventory,
// for the "did anything change" comparison only.
func dropPluginVolatile(m map[string]any) {
	pl, ok := m["plugins"].(map[string]any)
	if !ok {
		return
	}
	pluginMu.Lock()
	defer pluginMu.Unlock()
	for _, p := range plugins {
		if v, ok := pl[p.Name].(map[string]any); ok {
			for _, k := range p.Volatile {
				delete(v, k)
			}
		}
	}
}

// startPluginServices starts each plugin's Service in its own goroutine.
func startPluginServices(ctx context.Context, c *Client, tok string, lg *log.Logger) {
	pluginMu.Lock()
	list := append([]Plugin(nil), plugins...)
	pluginMu.Unlock()
	for _, p := range list {
		if p.Service == nil {
			continue
		}
		p := p
		lg.Printf("plugin %s: service started", p.Name)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					lg.Printf("plugin %s: service stopped after a panic: %v", p.Name, r)
				}
			}()
			p.Service(ctx, PluginEnv{Log: lg, Post: func(path string, in, out any) error { return c.PostJSON(path, tok, in, out) }})
		}()
	}
}

// StatusOf returns the HTTP status of an error from PluginEnv.Post (0 when the server was
// not reached at all).
func StatusOf(err error) int {
	if ae, ok := err.(*APIError); ok {
		return ae.Status
	}
	return 0
}

// RequestInventory asks the agent to report its inventory now instead of at the next round.
func RequestInventory() { requestInventory() }

// Exec builds a command the way the agent runs its own helpers.
var Exec = command
