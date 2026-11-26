package agent

// wake relays Wake-on-LAN for machines on this agent's network segment (the server can
// only broadcast on its own subnet). Magic packet = 6×0xFF + 16×MAC, UDP port 9, sent to
// 255.255.255.255 and to the directed broadcast of every IPv4 interface that is up.

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

func magicPacket(mac net.HardwareAddr) []byte {
	p := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		p = append(p, 0xFF)
	}
	for i := 0; i < 16; i++ {
		p = append(p, mac...)
	}
	return p
}

// broadcastTargets: limited broadcast plus each up, non-loopback IPv4 subnet's broadcast.
func broadcastTargets() []string {
	t := []string{"255.255.255.255:9"}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || n.IP.IsLinkLocalUnicast() {
				continue
			}
			ip, mask := n.IP.To4(), n.Mask
			if len(mask) == 16 {
				mask = mask[12:]
			}
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^mask[i]
			}
			t = append(t, b.String()+":9")
		}
	}
	return t
}

func wake(macs []string) (string, error) {
	if len(macs) == 0 || len(macs) > 256 {
		return "", errors.New("1–256 MAC addresses required")
	}
	var parsed []net.HardwareAddr
	for _, m := range macs {
		hw, err := net.ParseMAC(strings.TrimSpace(m))
		if err != nil || len(hw) != 6 {
			return "", fmt.Errorf("invalid MAC address %q", m)
		}
		parsed = append(parsed, hw)
	}
	targets := broadcastTargets()
	sent := 0
	var lastErr error
	for _, dst := range targets {
		addr, err := net.ResolveUDPAddr("udp4", dst)
		if err != nil {
			lastErr = err
			continue
		}
		conn, err := net.DialUDP("udp4", nil, addr)
		if err != nil {
			lastErr = err
			continue
		}
		for _, hw := range parsed {
			if _, err := conn.Write(magicPacket(hw)); err == nil {
				sent++
			} else {
				lastErr = err
			}
		}
		conn.Close()
	}
	if sent == 0 {
		return "", fmt.Errorf("no magic packet could be sent: %v", lastErr)
	}
	return fmt.Sprintf("magic packets sent for %d machine(s) to %s", len(parsed), strings.Join(targets, ", ")), nil
}
