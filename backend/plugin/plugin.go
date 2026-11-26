// Package plugin is the contract between the SWIM server and optional plugins.
//
// A plugin is a Go package outside the core (plugins/<name>/server) that calls Register in
// its init function. It is part of the server only when it is linked in: the generated file
// backend/cmd/server/plugins_gen.go imports the enabled plugins, and nothing else in the core
// refers to them. Without that import the plugin's code, routes, tables and web files simply
// do not exist in the running server.
//
// A plugin gets everything it may use through Host. It never imports the server's internal
// packages, so the core can change freely as long as this file keeps its promises.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"swim/pkg/manifest"
)

// Plugin describes one plugin. Only Name is required.
type Plugin struct {
	Name        string // lower-case letters, digits and dashes; also the URL and folder name
	Title       string
	Version     string
	Description string

	// Migrations holds *.sql files applied at start-up, once each, in name order. File names
	// must begin with "<name>_" (exam_0001_init.sql) so they cannot collide with the core's.
	Migrations fs.FS
	// Web holds the files served under /plugins/<name>/. The console loads "plugin.js" (and
	// "plugin.css" when present) from it.
	Web fs.FS
	// Commands are the signed command types this plugin may send to machines.
	Commands []CommandType
	// Tools are read-only ready-made scripts (run_script with {"tool": "<id>"}), without parameters.
	Tools map[string]func() (title, script string)
	// Routes registers the plugin's HTTP handlers. Use paths under /v1/<name>/ and
	// /v1/agent/<name>/ only.
	Routes func(mux *http.ServeMux, h Host)
	// Heartbeat runs each time a machine checks in; keep it cheap and throttle inside.
	Heartbeat func(ctx context.Context, h Host, hostID, agentVersion string)
	// Hourly runs at start-up and then every hour (retention, clean-up).
	Hourly func(ctx context.Context, h Host)
}

// CommandType is one kind of command a plugin sends to its half inside the agent.
type CommandType struct {
	Type     string // "set_exam"
	Risk     string // "safe", "change" or "destructive", as in manifest.CommandTypes
	MinAgent string // first agent version that knows the command; older agents are skipped
	// Check validates the arguments before the server signs them. It returns the arguments to
	// send and what to write in the activity log (never secrets).
	Check func(raw json.RawMessage) (args any, logged map[string]any, err error)
}

// Command is a signed command that is ready to be queued.
type Command struct {
	ID    string
	Queue func(ctx context.Context, tx pgx.Tx) error
}

// Host is what the server offers a plugin.
type Host interface {
	DB() *pgxpool.Pool
	Log() *slog.Logger
	// DataDir is a directory for this plugin's own files. It is not served by the web server.
	DataDir() string

	// Admin wraps a handler so only a signed-in administrator reaches it.
	Admin(http.HandlerFunc) http.HandlerFunc
	AdminName(*http.Request) string
	// AgentAuth identifies the machine that sent a request (its host token); on failure it has
	// already written the response.
	AgentAuth(http.ResponseWriter, *http.Request) (hostID string, ok bool)

	// Do runs fn in a transaction and writes one activity-log entry in the same transaction,
	// in the name of the administrator who sent r. fn may add to data.
	Do(r *http.Request, action, objectType, objectID string, data map[string]any, fn func(tx pgx.Tx) error) error
	// DoAs is Do for work nobody asked for (heartbeat, hourly); actor is "system:<name>".
	DoAs(ctx context.Context, actor, action, objectType, objectID string, data map[string]any, fn func(tx pgx.Tx) error) error

	// PlanCommand validates and signs a command for one machine without writing anything.
	// The error says why the machine cannot take it now (unknown, removed, agent too old).
	PlanCommand(ctx context.Context, hostID, commandType string, args any, actor string) (*Command, error)
	// Nudge wakes the machine's waiting connection so it fetches its commands now.
	Nudge(hostID string)
	Online(hostID string) bool
	Touch(hostID string)
	// Publish sends a live event to open consoles ("<name>.something").
	Publish(event, hostID string, data map[string]any)

	JSON(w http.ResponseWriter, status int, v any)
	Error(w http.ResponseWriter, status int, message string)
	Decode(w http.ResponseWriter, r *http.Request, v any) bool
	DBError(w http.ResponseWriter, err error)
	NewID(prefix string) string
	VersionLess(a, b string) bool
}

var (
	mu       sync.Mutex
	plugins  = map[string]Plugin{}
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	commands = map[string]CommandType{}
)

// Register adds a plugin. Call it from the plugin package's init function.
func Register(p Plugin) {
	mu.Lock()
	defer mu.Unlock()
	if !nameRe.MatchString(p.Name) {
		panic(fmt.Sprintf("plugin: invalid name %q", p.Name))
	}
	if _, dup := plugins[p.Name]; dup {
		panic("plugin: " + p.Name + " registered twice")
	}
	for _, c := range p.Commands {
		if _, taken := manifest.CommandTypes[c.Type]; taken || c.Type == "" || c.Check == nil {
			panic(fmt.Sprintf("plugin %s: command type %q is taken or incomplete", p.Name, c.Type))
		}
		manifest.CommandTypes[c.Type] = c.Risk
		commands[c.Type] = c
	}
	plugins[p.Name] = p
}

// All returns the registered plugins in name order.
func All() []Plugin {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Plugin, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CommandOf returns the plugin command type, if typ belongs to a plugin.
func CommandOf(typ string) (CommandType, bool) {
	mu.Lock()
	defer mu.Unlock()
	c, ok := commands[typ]
	return c, ok
}

// Tool returns a plugin's ready-made tool by id.
func Tool(id string) (func() (string, string), bool) {
	mu.Lock()
	defer mu.Unlock()
	for _, p := range plugins {
		if f, ok := p.Tools[id]; ok {
			return f, true
		}
	}
	return nil, false
}
