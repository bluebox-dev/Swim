package wol

import "testing"

func TestPacket(t *testing.T) {
	mac, err := ParseMAC("02-11-EC-E8-B1-16")
	if err != nil {
		t.Fatal(err)
	}
	p := Packet(mac)
	if len(p) != 102 || p[0] != 0xff || p[5] != 0xff || p[6] != 0x02 || p[101] != 0x16 {
		t.Fatalf("bad packet % x", p[:12])
	}
	for _, s := range []string{"02:11:ec:e8:b1:16", "0211ece8b116"} {
		if _, err := ParseMAC(s); err != nil {
			t.Fatal(s, err)
		}
	}
	if _, err := ParseMAC("zz"); err == nil {
		t.Fatal("bad MAC accepted")
	}
}
