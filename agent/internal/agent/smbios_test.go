//go:build windows

package agent

import "testing"

func TestParseSMBIOSUUID(t *testing.T) {
	// header(8) + type 0 (len 4, no strings) + type 1 with UUID bytes
	raw := []byte{0, 3, 0, 0, 0, 0, 0, 0}
	t0 := []byte{0, 4, 0, 0, 0, 0}
	t1 := make([]byte, 0x1b)
	t1[0], t1[1] = 1, 0x1b
	copy(t1[8:24], []byte{0x3c, 0x79, 0x06, 0x98, 0xa3, 0x8e, 0x62, 0x42, 0x8c, 0xbd, 0xcf, 0x39, 0x08, 0xf6, 0xbb, 0x84})
	tbl := append(append(t0, append(t1, 0, 0)...), 127, 4, 0, 0, 0, 0)
	raw[4] = byte(len(tbl))
	got, err := parseSMBIOSUUID(append(raw, tbl...))
	if err != nil || got != "9806793c-8ea3-4262-8cbd-cf3908f6bb84" {
		t.Fatalf("got %q %v", got, err)
	}
}
