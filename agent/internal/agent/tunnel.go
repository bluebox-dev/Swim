package agent

// Remote Desktop through the agent ("reverse tunnel").
//
// A machine behind a router cannot be reached from the server, but its agent can always
// reach the server. For a remote session the agent therefore opens connections OUT to the
// server (GET /v1/agent/tunnel/{id}, upgraded to a raw byte stream) and, as soon as the
// server sends the first bytes of an RDP connection, joins that stream to Remote Desktop
// on this machine (127.0.0.1:3389). Nothing listens on the machine for the network, and
// the tunnel exists only while the remote session is allowed.

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const tunnelWorkers = 2 // an RDP client may open a second connection while negotiating

type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// dialTunnel opens one upgraded connection to the server over the pinned TLS configuration.
func (c *Client) dialTunnel(id, tok string) (net.Conn, error) {
	u, err := url.Parse(c.Server)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if t, ok := c.hc.Transport.(*http.Transport); ok && t.TLSClientConfig != nil {
		tc = t.TLSClientConfig.Clone()
	}
	tc.NextProtos = []string{"http/1.1"} // an upgrade needs HTTP/1.1
	if tc.ServerName == "" {
		tc.ServerName = u.Hostname()
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", host, tc)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	req := "GET /v1/agent/tunnel/" + url.PathEscape(id) + " HTTP/1.1\r\nHost: " + u.Host +
		"\r\nAuthorization: Bearer " + tok + "\r\nConnection: Upgrade\r\nUpgrade: swim-tunnel\r\nSWIM-Agent-Version: " + Version + "\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("tunnel refused: HTTP %d", resp.StatusCode)
	}
	_ = conn.SetDeadline(time.Time{})
	return &bufConn{Conn: conn, r: br}, nil
}

// runTunnel serves the tunnel until the remote session window closes. A session that is
// already connected keeps running after that; only new connections stop.
func runTunnel(c *Client, tok, id, target string, until time.Time, lg interface{ Printf(string, ...any) }) {
	lg.Printf("remote tunnel %s: open until %s", id, until.Format("15:04:05"))
	for i := 0; i < tunnelWorkers; i++ {
		go func() {
			fails := 0
			for time.Now().Before(until) {
				err := tunnelOnce(c, tok, id, target, until)
				if err == nil {
					fails = 0
					continue
				}
				if strings.Contains(err.Error(), "HTTP 404") {
					return // replaced by a newer session or the window is over
				}
				if fails++; fails > 20 {
					lg.Printf("remote tunnel %s: giving up: %v", id, err)
					return
				}
				time.Sleep(3 * time.Second)
			}
		}()
	}
}

// tunnelOnce waits on one connection for the server to start an RDP connection, then relays it.
func tunnelOnce(c *Client, tok, id, target string, until time.Time) error {
	up, err := c.dialTunnel(id, tok)
	if err != nil {
		return err
	}
	defer up.Close()
	_ = up.SetReadDeadline(until)
	first := make([]byte, 16<<10)
	n, err := up.Read(first)
	if err != nil {
		if time.Now().After(until) || errors.Is(err, io.EOF) {
			return nil // window over, or the server recycled an idle connection
		}
		return err
	}
	_ = up.SetReadDeadline(time.Time{})
	rdp, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return fmt.Errorf("local Remote Desktop: %w", err)
	}
	defer rdp.Close()
	if _, err := rdp.Write(first[:n]); err != nil {
		return err
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(rdp, up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(up, rdp); done <- struct{}{} }()
	<-done
	return nil
}
