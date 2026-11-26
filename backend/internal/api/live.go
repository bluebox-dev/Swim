package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ───────── event hub (admin UI live updates) ─────────

// Event is pushed to every connected admin browser over SSE.
type Event struct {
	Type   string `json:"type"` // host.presence | host.updated | command.updated | batch.updated
	HostID string `json:"host_id,omitempty"`
	Data   any    `json:"data,omitempty"`
}

type hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func newHub() *hub { return &hub{subs: map[chan Event]struct{}{}} }

func (h *hub) subscribe() chan Event {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// publish never blocks: a browser that cannot keep up misses events and
// re-reads state on its next render anyway.
func (h *hub) publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// ───────── presence: online = the agent holds its command long-poll open ─────────

// The agent opens a new long-poll at most every longPollMaxWait (20 s). A host
// whose last poll *started* more than offlineAfter ago is gone, even if its old
// TCP connection is still dangling (power cut, cable pulled, Wi-Fi lost).
const offlineAfter = 30 * time.Second

type presenceEntry struct {
	conns     int
	lastSeen  time.Time // last sign of life (poll start/end, heartbeat, result)
	lastBegin time.Time // last long-poll start
	online    bool
}

type presence struct {
	mu    sync.Mutex
	hosts map[string]*presenceEntry
	wake  map[string]chan struct{} // nudges a waiting long-poll when a command is queued
}

func newPresence() *presence {
	return &presence{hosts: map[string]*presenceEntry{}, wake: map[string]chan struct{}{}}
}

func (p *presence) entry(id string) *presenceEntry {
	e := p.hosts[id]
	if e == nil {
		e = &presenceEntry{}
		p.hosts[id] = e
	}
	return e
}

// begin returns true when the host just came online.
func (p *presence) begin(id string) (cameOnline bool, wake chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(id)
	e.conns++
	e.lastSeen, e.lastBegin = time.Now(), time.Now()
	cameOnline = !e.online
	e.online = true
	w := p.wake[id]
	if w == nil {
		w = make(chan struct{}, 1)
		p.wake[id] = w
	}
	return cameOnline, w
}

func (p *presence) end(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(id)
	if e.conns > 0 {
		e.conns--
	}
}

func (p *presence) touch(id string) (cameOnline bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(id)
	e.lastSeen = time.Now()
	cameOnline = !e.online
	e.online = true
	return cameOnline
}

// goodbye marks a host offline at once (agent is stopping: shutdown/reboot/service stop).
func (p *presence) goodbye(id string) (wentOffline bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(id)
	wentOffline = e.online
	e.online, e.conns = false, 0
	return wentOffline
}

func (p *presence) isOnline(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.hosts[id]
	return e != nil && e.online
}

func (p *presence) nudge(id string) {
	p.mu.Lock()
	w := p.wake[id]
	p.mu.Unlock()
	if w != nil {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// sweep returns hosts that just went offline: no new poll and no other sign of
// life for offlineAfter. A still-open poll older than that is a dead connection.
func (p *presence) sweep() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var gone []string
	for id, e := range p.hosts {
		quiet := time.Since(e.lastSeen) > offlineAfter && (e.conns == 0 || time.Since(e.lastBegin) > offlineAfter)
		if e.online && quiet {
			e.online = false
			gone = append(gone, id)
		}
	}
	return gone
}

// Run starts background work (presence sweeper). Call once.
func (s *Server) Run(ctx context.Context) {
	go func() { // activity log retention
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			s.pruneAudit(ctx)
			s.pluginHourly(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		n := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n++; n%12 == 0 { // every minute
					s.sweepRemovals(ctx)
					s.sweepStaleCommands(ctx)
				}
				for _, id := range s.pres.sweep() {
					s.events.publish(Event{Type: "host.presence", HostID: id, Data: map[string]any{"online": false}})
				}
			}
		}
	}()
}

func (s *Server) markOnline(id string, cameOnline bool) {
	if cameOnline {
		s.events.publish(Event{Type: "host.presence", HostID: id, Data: map[string]any{"online": true}})
	}
}

// ───────── SSE endpoint ─────────

// eventStream streams live events to an admin browser. The UI reads it with
// fetch() (EventSource cannot send the Authorization header).
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "SWIM-API-500", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: do not buffer
	w.WriteHeader(http.StatusOK)
	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)
	fmt.Fprintf(w, "event: hello\ndata: {}\n\n")
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}
