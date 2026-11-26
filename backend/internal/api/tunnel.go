package api

// Remote Desktop through the agent (reverse tunnel).
//
// A machine behind a router (NAT) cannot be reached from the server, but its agent always
// reaches the server. For such a machine a remote session gets a tunnel:
//
//	guacd ──TCP──► 127.0.0.1:<port> (this backend) ◄──upgraded HTTPS── agent ──► 127.0.0.1:3389 on the machine
//
// The agent opens connections out to GET /v1/agent/tunnel/{id} (host token + a random id
// that only the signed remote_session command carries). Each RDP connection guacd makes
// to the tunnel's port is joined to one waiting agent connection. The port listens on
// the server's loopback only (docker publishes 127.0.0.1:13390-13399) and closes when the
// session window ends; a session that is already open keeps running.

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	tunnelPortFirst = 13390
	tunnelPortLast  = 13399
	minTunnelAgent  = "0.5.24"
)

type tunnel struct {
	id, hostID string
	port       int
	ln         net.Listener
	agents     chan net.Conn // agent connections waiting for an RDP connection
	until      time.Time
}

var (
	tunnels   sync.Map // id → *tunnel
	tunnelsMu sync.Mutex
)

// openTunnel reserves a loopback port for one machine until the session window ends.
func (s *Server) openTunnel(hostID string, ttl time.Duration) (*tunnel, error) {
	tunnelsMu.Lock()
	defer tunnelsMu.Unlock()
	// one tunnel per machine: a new request replaces the previous one
	tunnels.Range(func(k, v any) bool {
		if t := v.(*tunnel); t.hostID == hostID {
			t.close()
		}
		return true
	})
	for p := tunnelPortFirst; p <= tunnelPortLast; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("", itoa(p)))
		if err != nil {
			continue // in use by another session
		}
		t := &tunnel{id: randHex(16), hostID: hostID, port: p, ln: ln, agents: make(chan net.Conn, 8), until: time.Now().Add(ttl)}
		tunnels.Store(t.id, t)
		go t.serve(s)
		time.AfterFunc(ttl+5*time.Second, t.close)
		return t, nil
	}
	return nil, errors.New("all remote tunnels are in use — try again in a few minutes")
}

func (t *tunnel) close() {
	if _, ok := tunnels.LoadAndDelete(t.id); !ok {
		return
	}
	_ = t.ln.Close()
	for {
		select {
		case c := <-t.agents:
			_ = c.Close()
		default:
			return
		}
	}
}

// serve joins every connection guacd opens to a waiting agent connection.
func (t *tunnel) serve(s *Server) {
	for {
		g, err := t.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer g.Close()
			select {
			case a := <-t.agents:
				defer a.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(a, g); done <- struct{}{} }()
				go func() { _, _ = io.Copy(g, a); done <- struct{}{} }()
				<-done
			case <-time.After(20 * time.Second):
				s.log.Warn("remote tunnel: the agent did not connect in time", "host", t.hostID)
			}
		}()
	}
}

type hijackedConn struct {
	net.Conn
	r *bufio.Reader
}

func (h *hijackedConn) Read(p []byte) (int, error) { return h.r.Read(p) }

// agentTunnel: GET /v1/agent/tunnel/{id} with "Upgrade: swim-tunnel" — one waiting connection of the agent.
func (s *Server) agentTunnel(w http.ResponseWriter, r *http.Request) {
	hostID, _, ok := s.hostAuth(w, r)
	if !ok {
		return
	}
	v, found := tunnels.Load(r.PathValue("id"))
	if !found {
		writeErr(w, http.StatusNotFound, "SWIM-API-404", "no such tunnel (the remote session window is over)")
		return
	}
	t := v.(*tunnel)
	if t.hostID != hostID {
		writeErr(w, http.StatusForbidden, "SWIM-API-403", "this tunnel belongs to another machine")
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok || r.Header.Get("Upgrade") != "swim-tunnel" {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", "upgrade to swim-tunnel required")
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: swim-tunnel\r\n\r\n"); err != nil || rw.Flush() != nil {
		conn.Close()
		return
	}
	c := &hijackedConn{Conn: conn, r: rw.Reader}
	select {
	case t.agents <- c:
	default:
		conn.Close() // more waiting connections than anyone needs
	}
}
