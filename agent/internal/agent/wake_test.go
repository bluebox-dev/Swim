package agent

import (
	"bytes"
	"net"
	"testing"
)

func TestMagicPacket(t *testing.T) {
	mac, _ := net.ParseMAC("52:54:00:12:34:56")
	p := magicPacket(mac)
	if len(p) != 102 || !bytes.Equal(p[:6], bytes.Repeat([]byte{0xFF}, 6)) || !bytes.Equal(p[6:12], mac) || !bytes.Equal(p[96:], mac) {
		t.Fatalf("bad packet % x", p)
	}
	if _, err := wake([]string{"not-a-mac"}); err == nil {
		t.Fatal("invalid MAC accepted")
	}
}
