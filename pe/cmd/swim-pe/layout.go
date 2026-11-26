package main

// Disk layout parsing (no Windows API here, so it is tested on any platform).

import (
	"encoding/binary"
	"errors"
	"fmt"
)

type part struct {
	Number         int
	Offset, Length uint64
	Type, ID       string
	Active         bool // MBR: the partition BIOS boots from
}

func guidString(b []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(b[0:4]), binary.LittleEndian.Uint16(b[4:6]),
		binary.LittleEndian.Uint16(b[6:8]), b[8:10], b[10:16])
}

// parseLayout reads a DRIVE_LAYOUT_INFORMATION_EX buffer. GPT partitions are identified by
// their partition GUID. MBR partitions have none, so they are named
// "mbr-<disk signature>-<byte offset>", which is stable in Windows and in WinPE alike.
func parseLayout(buf []byte) (ps []part, mbr bool, err error) {
	if len(buf) < 48 {
		return nil, false, errors.New("short layout")
	}
	style := binary.LittleEndian.Uint32(buf[0:4])
	if style != 0 && style != 1 { // 0 = MBR, 1 = GPT
		return nil, false, errors.New("disk is neither MBR nor GPT")
	}
	mbr = style == 0
	sig := binary.LittleEndian.Uint32(buf[8:12]) // MBR disk signature
	count := int(binary.LittleEndian.Uint32(buf[4:8]))
	for i := 0; i < count && 48+(i+1)*144 <= len(buf); i++ {
		e := buf[48+i*144 : 48+(i+1)*144]
		num := int(binary.LittleEndian.Uint32(e[24:28]))
		if num == 0 {
			continue
		}
		p := part{Number: num, Offset: binary.LittleEndian.Uint64(e[8:16]), Length: binary.LittleEndian.Uint64(e[16:24])}
		if mbr {
			typ := e[32]
			if typ == 0 || typ == 0x05 || typ == 0x0f { // unused entry or extended container
				continue
			}
			p.Type, p.ID, p.Active = fmt.Sprintf("mbr-%02x", typ), fmt.Sprintf("mbr-%08x-%d", sig, p.Offset), e[33] != 0
		} else {
			p.Type, p.ID = guidString(e[32:48]), guidString(e[48:64])
		}
		ps = append(ps, p)
	}
	return ps, mbr, nil
}

const typeESPGUID = "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"

// partByID finds a partition by its identity (GPT GUID or "mbr-…" name), ignoring case and braces.
func partByID(ps []part, id string) *part {
	id = trimID(id)
	for i := range ps {
		if ps[i].ID == id {
			return &ps[i]
		}
	}
	return nil
}

func trimID(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '{' || r == '}' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}
