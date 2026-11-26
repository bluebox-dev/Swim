package api

import (
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// A connection made to the tunnel's port (guacd) must be joined to a waiting agent
// connection, in both directions.
func TestTunnelJoinsServerAndAgent(t *testing.T) {
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	tun, err := s.openTunnel("h_test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.close()
	agentSide, serverSide := net.Pipe()
	tun.agents <- serverSide

	g, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(tun.port)), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	go func() { _, _ = g.Write([]byte("rdp-hello")) }()
	buf := make([]byte, 9)
	_ = agentSide.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(agentSide, buf); err != nil || string(buf) != "rdp-hello" {
		t.Fatalf("server → agent: %q %v", buf, err)
	}
	go func() { _, _ = agentSide.Write([]byte("rdp-reply")) }()
	_ = g.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(g, buf); err != nil || string(buf) != "rdp-reply" {
		t.Fatalf("agent → server: %q %v", buf, err)
	}

	// a second request for the same machine replaces the first tunnel
	tun2, err := s.openTunnel("h_test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer tun2.close()
	if _, ok := tunnels.Load(tun.id); ok {
		t.Fatal("the old tunnel of the machine is still registered")
	}
}
