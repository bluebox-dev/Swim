package api

// The server's side of the plugin contract (swim/backend/plugin): the Host a plugin works
// through, its routes and web files, and the two hooks the core calls. With no plugin linked
// in, everything here is a loop over an empty list.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"swim/backend/internal/audit"
	"swim/backend/internal/ids"
	"swim/backend/plugin"
)

type pluginHost struct {
	s    *Server
	name string
}

func pluginDataRoot() string {
	if d := os.Getenv("SWIM_PLUGIN_DATA"); d != "" {
		return d
	}
	return "/plugin-data"
}

func (h pluginHost) DB() *pgxpool.Pool                         { return h.s.pool }
func (h pluginHost) Log() *slog.Logger                         { return h.s.log.With("plugin", h.name) }
func (h pluginHost) DataDir() string                           { return filepath.Join(pluginDataRoot(), h.name) }
func (h pluginHost) Admin(f http.HandlerFunc) http.HandlerFunc { return h.s.requireAdmin(f) }
func (h pluginHost) AdminName(r *http.Request) string          { return adminFrom(r).Name }
func (h pluginHost) Nudge(hostID string)                       { h.s.pres.nudge(hostID) }
func (h pluginHost) Online(hostID string) bool                 { return h.s.pres.isOnline(hostID) }
func (h pluginHost) Touch(hostID string)                       { h.s.pres.touch(hostID) }
func (h pluginHost) NewID(prefix string) string                { return ids.New(prefix) }
func (h pluginHost) VersionLess(a, b string) bool              { return versionLess(a, b) }
func (h pluginHost) JSON(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, v)
}
func (h pluginHost) Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decode(w, r, v)
}
func (h pluginHost) DBError(w http.ResponseWriter, err error) { h.s.dbErr(w, err) }
func (h pluginHost) Error(w http.ResponseWriter, status int, message string) {
	writeErr(w, status, "SWIM-API-"+itoa(status), message)
}

func (h pluginHost) AgentAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, _, ok := h.s.hostAuth(w, r)
	return id, ok
}

func (h pluginHost) Publish(event, hostID string, data map[string]any) {
	h.s.events.publish(Event{Type: event, HostID: hostID, Data: data})
}

func (h pluginHost) Do(r *http.Request, action, objectType, objectID string, data map[string]any, fn func(pgx.Tx) error) error {
	return h.s.inTx(r.Context(), func(tx pgx.Tx) (audit.Entry, error) {
		if err := fn(tx); err != nil {
			return audit.Entry{}, err
		}
		return h.s.entry(r, action, objectType, objectID, data), nil
	})
}

func (h pluginHost) DoAs(ctx context.Context, actor, action, objectType, objectID string, data map[string]any, fn func(pgx.Tx) error) error {
	return h.s.inTx(ctx, func(tx pgx.Tx) (audit.Entry, error) {
		if err := fn(tx); err != nil {
			return audit.Entry{}, err
		}
		return audit.Entry{Actor: actor, Action: action, ObjectType: objectType, ObjectID: objectID, Result: "ok", Data: data}, nil
	})
}

func (h pluginHost) PlanCommand(ctx context.Context, hostID, typ string, args any, actor string) (*plugin.Command, error) {
	if _, ok := plugin.CommandOf(typ); !ok {
		return nil, errors.New("not a plugin command type: " + typ)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	p, ce := h.s.planCommand(ctx, hostID, typ, raw, "", actor)
	if ce != nil {
		return nil, errors.New(ce.msg)
	}
	return &plugin.Command{ID: p.ID, Queue: func(ctx context.Context, tx pgx.Tx) error {
		return insertCommand(ctx, tx, p, "", actor, nil)
	}}, nil
}

// pluginRoutes registers every linked plugin's handlers and web files, and the list the
// console reads to know what to load.
func (s *Server) pluginRoutes(m *http.ServeMux) {
	type item struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Version     string `json:"version"`
		Description string `json:"description"`
		JS          string `json:"js,omitempty"`
		CSS         string `json:"css,omitempty"`
	}
	list := []item{}
	for _, p := range plugin.All() {
		it := item{Name: p.Name, Title: p.Title, Version: p.Version, Description: p.Description}
		if p.Web != nil {
			base := "/plugins/" + p.Name + "/"
			if _, err := fs.Stat(p.Web, "plugin.js"); err == nil {
				it.JS = base + "plugin.js?v=" + p.Version
			}
			if _, err := fs.Stat(p.Web, "plugin.css"); err == nil {
				it.CSS = base + "plugin.css?v=" + p.Version
			}
			files := http.StripPrefix(base, http.FileServerFS(p.Web))
			m.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/") { // no directory listings
					http.NotFound(w, r)
					return
				}
				w.Header().Set("X-Content-Type-Options", "nosniff")
				w.Header().Set("Cache-Control", "public, max-age=3600")
				files.ServeHTTP(w, r)
			})
		}
		if p.Routes != nil {
			p.Routes(m, pluginHost{s, p.Name})
		}
		list = append(list, it)
	}
	// names and versions only: the console needs it before sign-in to load the plugin scripts
	m.HandleFunc("GET /v1/plugins", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": list})
	})
}

// pluginHeartbeat and pluginHourly are the two places the core calls into plugins. A plugin
// that panics is logged and skipped: it must not break check-ins or the hourly jobs.
func (s *Server) pluginHeartbeat(ctx context.Context, hostID, agentVer string) {
	for _, p := range plugin.All() {
		if p.Heartbeat != nil {
			s.pluginCall(p.Name, func() { p.Heartbeat(ctx, pluginHost{s, p.Name}, hostID, agentVer) })
		}
	}
}

func (s *Server) pluginHourly(ctx context.Context) {
	for _, p := range plugin.All() {
		if p.Hourly != nil {
			s.pluginCall(p.Name, func() { p.Hourly(ctx, pluginHost{s, p.Name}) })
		}
	}
}

func (s *Server) pluginCall(name string, f func()) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("plugin panicked", "plugin", name, "panic", r)
		}
	}()
	f()
}
