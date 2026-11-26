package main

import (
	"encoding/binary"
	"testing"
)

func entry(buf []byte, i int, num uint32, off, length uint64) []byte {
	e := buf[48+i*144 : 48+(i+1)*144]
	binary.LittleEndian.PutUint64(e[8:16], off)
	binary.LittleEndian.PutUint64(e[16:24], length)
	binary.LittleEndian.PutUint32(e[24:28], num)
	return e
}

// A typical legacy BIOS disk: System Reserved (active), Windows, recovery, SWIM staging.
func TestParseLayoutMBR(t *testing.T) {
	buf := make([]byte, 48+144*8)
	binary.LittleEndian.PutUint32(buf[0:4], 0) // MBR
	binary.LittleEndian.PutUint32(buf[4:8], 5)
	binary.LittleEndian.PutUint32(buf[8:12], 0x9A3BC0DE) // disk signature
	e := entry(buf, 0, 1, 1<<20, 579<<20)
	e[32], e[33] = 0x07, 1 // NTFS, active
	entry(buf, 1, 2, 580<<20, 400<<30)[32] = 0x07
	entry(buf, 2, 3, 401<<30, 20<<30)[32] = 0x07
	entry(buf, 3, 4, 465<<30, 500<<20)[32] = 0x27
	entry(buf, 4, 0, 0, 0) // unused slot

	ps, mbr, err := parseLayout(buf)
	if err != nil || !mbr || len(ps) != 4 {
		t.Fatalf("parse: %v mbr=%v parts=%d", err, mbr, len(ps))
	}
	if ps[0].ID != "mbr-9a3bc0de-1048576" || !ps[0].Active || ps[0].Type != "mbr-07" {
		t.Fatalf("system partition: %+v", ps[0])
	}
	if ps[1].Active || ps[1].ID != "mbr-9a3bc0de-608174080" {
		t.Fatalf("windows partition: %+v", ps[1])
	}
	if p := partByID(ps, "MBR-9A3BC0DE-608174080"); p == nil || p.Number != 2 {
		t.Fatalf("lookup by id (case-insensitive): %+v", p)
	}
}

func TestParseLayoutGPT(t *testing.T) {
	buf := make([]byte, 48+144*4)
	binary.LittleEndian.PutUint32(buf[0:4], 1) // GPT
	binary.LittleEndian.PutUint32(buf[4:8], 1)
	e := entry(buf, 0, 1, 1<<20, 100<<20)
	copy(e[32:48], []byte{0x28, 0x73, 0x2a, 0xc1, 0x1f, 0xf8, 0xd2, 0x11, 0xba, 0x4b, 0x00, 0xa0, 0xc9, 0x3e, 0xc9, 0x3b}) // ESP type
	e[48] = 0x01
	ps, mbr, err := parseLayout(buf)
	if err != nil || mbr || len(ps) != 1 || ps[0].Type != typeESPGUID {
		t.Fatalf("gpt: %v mbr=%v %+v", err, mbr, ps)
	}
}

func TestParseLayoutRejectsRaw(t *testing.T) {
	buf := make([]byte, 48+144)
	binary.LittleEndian.PutUint32(buf[0:4], 2) // RAW
	if _, _, err := parseLayout(buf); err == nil {
		t.Fatal("a raw disk must be refused")
	}
}
