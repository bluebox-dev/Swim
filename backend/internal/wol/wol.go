// Package wol sends Wake-on-LAN magic packets. The backend runs on a container bridge,
// where a broadcast never reaches the LAN, so a tiny "waker" process runs on the host
// network and takes requests over a Unix socket shared with the backend (nothing is
// exposed on the network).
package wol

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"
)

// ParseMAC accepts 02-11-EC-E8-B1-16, 02:11:ec:e8:b1:16 or 0211ece8b116.
func ParseMAC(s string) ([]byte, error) {
	h := strings.NewReplacer("-", "", ":", "", ".", "").Replace(strings.TrimSpace(s))
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 6 {
		return nil, fmt.Errorf("bad MAC %q", s)
	}
	return b, nil
}

// Packet is 6×0xFF followed by the MAC 16 times.
func Packet(mac []byte) []byte {
	p := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		p = append(p, 0xff)
	}
	for i := 0; i < 16; i++ {
		p = append(p, mac...)
	}
	return p
}

// broadcasts lists 255.255.255.255 plus the directed broadcast of every up IPv4 interface.
func broadcasts() []string {
	out := []string{"255.255.255.255"}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagBroadcast == 0 ||
			strings.HasPrefix(ifc.Name, "docker") || strings.HasPrefix(ifc.Name, "br-") || strings.HasPrefix(ifc.Name, "veth") || strings.HasPrefix(ifc.Name, "virbr") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ip, mask := n.IP.To4(), n.Mask
			b := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				b[i] = ip[i] | ^mask[i]
			}
			out = append(out, b.String())
		}
	}
	return out
}

// Send sends the magic packet for each MAC to every broadcast address, UDP ports 9 and 7, three times.
func Send(macs []string) ([]string, error) {
	targets := broadcasts()
	sent := 0
	for _, m := range macs {
		mac, err := ParseMAC(m)
		if err != nil {
			return targets, err
		}
		pkt := Packet(mac)
		for round := 0; round < 3; round++ {
			for _, t := range targets {
				for _, port := range []int{9, 7} {
					c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(t), Port: port})
					if err != nil {
						continue
					}
					if _, err := c.Write(pkt); err == nil {
						sent++
					}
					c.Close()
				}
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	if sent == 0 {
		return targets, errors.New("no packet could be sent")
	}
	return targets, nil
}

type request struct {
	MACs []string `json:"macs"`
}
type reply struct {
	Targets []string `json:"targets"`
	Error   string   `json:"error,omitempty"`
}

// Serve runs the host-network waker on a Unix socket (one JSON request per connection).
func Serve(sock string, log *slog.Logger) error {
	_ = os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	_ = os.Chmod(sock, 0o660)
	log.Info("waker listening", "socket", sock, "broadcasts", broadcasts())
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			var in request
			if err := json.NewDecoder(bufio.NewReader(c)).Decode(&in); err != nil {
				_ = json.NewEncoder(c).Encode(reply{Error: "bad request"})
				return
			}
			t, err := Send(in.MACs)
			rep := reply{Targets: t}
			if err != nil {
				rep.Error = err.Error()
			}
			log.Info("wake", "macs", in.MACs, "targets", t, "err", rep.Error)
			_ = json.NewEncoder(c).Encode(rep)
		}(c)
	}
}

// Ask is the backend side: hand the MACs to the waker over its socket.
func Ask(sock string, macs []string) ([]string, error) {
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("waker service not reachable: %w", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if err := json.NewEncoder(c).Encode(request{MACs: macs}); err != nil {
		return nil, err
	}
	var rep reply
	if err := json.NewDecoder(c).Decode(&rep); err != nil {
		return nil, err
	}
	if rep.Error != "" {
		return rep.Targets, errors.New(rep.Error)
	}
	return rep.Targets, nil
}
