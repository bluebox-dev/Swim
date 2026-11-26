// Package wof decodes Windows Overlay Filter (CompactOS / "system compression")
// files: a reparse point (tag 0x80000017) plus a "WofCompressedData" named stream.
//
// wimlib in NTFS-3G mode captures such files exactly as they are on disk. Windows
// reads them fine through wof.sys, but WinPE does not attach WOF to a freshly
// applied volume, so programs from the new image (DISM, bcdboot …) cannot run
// there (ERROR_CANT_ACCESS_FILE). The builder therefore stores them uncompressed.
package wof

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const ReparseTag = 0x80000017

// Algorithms (FILE_PROVIDER_EXTERNAL_INFO_V1.Algorithm).
const (
	XPRESS4K  = 0
	LZX       = 1
	XPRESS8K  = 2
	XPRESS16K = 3
)

// ParseReparse reads the ntfs-3g "system.ntfs_reparse_data" value of a WOF file
// (8-byte reparse header + WOF_EXTERNAL_INFO + FILE_PROVIDER_EXTERNAL_INFO_V1)
// and returns the compression algorithm.
func ParseReparse(b []byte) (int, error) {
	if len(b) < 24 || binary.LittleEndian.Uint32(b) != ReparseTag {
		return 0, errors.New("not a WOF reparse point")
	}
	if prov := binary.LittleEndian.Uint32(b[12:]); prov != 2 { // WOF_PROVIDER_FILE
		return 0, fmt.Errorf("WOF provider %d is not the file provider", prov)
	}
	return int(binary.LittleEndian.Uint32(b[20:])), nil
}

func chunkSize(algo int) (int, error) {
	switch algo {
	case XPRESS4K:
		return 4096, nil
	case XPRESS8K:
		return 8192, nil
	case XPRESS16K:
		return 16384, nil
	}
	return 0, fmt.Errorf("WOF algorithm %d not supported (only XPRESS)", algo)
}

// Decompress turns a WofCompressedData stream back into the file contents.
// Layout: a table of chunk end offsets (numChunks-1 entries, 4 bytes, or 8 bytes
// when size > 4 GiB) followed by the chunks; a chunk whose stored size equals its
// uncompressed size is stored raw.
func Decompress(stream []byte, size int64, algo int) ([]byte, error) {
	cs, err := chunkSize(algo)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return []byte{}, nil
	}
	n := int((size + int64(cs) - 1) / int64(cs))
	ent := 4
	if size > 0xFFFFFFFF {
		ent = 8
	}
	tbl := (n - 1) * ent
	if len(stream) < tbl {
		return nil, errors.New("WOF stream shorter than its chunk table")
	}
	data := stream[tbl:]
	starts := make([]int, n+1)
	for i := 1; i < n; i++ {
		var v uint64
		if ent == 4 {
			v = uint64(binary.LittleEndian.Uint32(stream[(i-1)*4:]))
		} else {
			v = binary.LittleEndian.Uint64(stream[(i-1)*8:])
		}
		starts[i] = int(v)
	}
	starts[n] = len(data)
	out := make([]byte, size)
	for i := 0; i < n; i++ {
		a, b := starts[i], starts[i+1]
		if a > b || b > len(data) {
			return nil, fmt.Errorf("chunk %d: bad offsets %d..%d", i, a, b)
		}
		dst := out[i*cs:]
		if len(dst) > cs {
			dst = dst[:cs]
		}
		if b-a == len(dst) {
			copy(dst, data[a:b])
			continue
		}
		if err := xpressHuff(data[a:b], dst); err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, err)
		}
	}
	return out, nil
}

// ───────── XPRESS Huffman (MS-XCA 2.2.4), one table per chunk (chunks are ≤ 64 KiB) ─────────

const (
	numSyms  = 512
	maxLen   = 15
	fastBits = 10
)

type bits struct {
	in       []byte
	pos      int
	buf      uint32
	left     uint
	overflow bool
}

// ensure keeps at least n (≤16) bits in the MSB-aligned buffer, 16 bits at a time
func (s *bits) ensure(n uint) {
	if s.left >= n {
		return
	}
	if len(s.in)-s.pos < 2 {
		s.left = 32 // past the end: zeros (valid streams never consume them)
		s.overflow = true
		return
	}
	s.buf |= uint32(binary.LittleEndian.Uint16(s.in[s.pos:])) << (16 - s.left)
	s.pos += 2
	s.left += 16
}

func (s *bits) peek(n uint) uint32 { return s.buf >> (32 - n) }
func (s *bits) drop(n uint)        { s.buf <<= n; s.left -= n }
func (s *bits) pop(n uint) uint32 {
	if n == 0 {
		return 0
	}
	v := s.peek(n)
	s.drop(n)
	return v
}
func (s *bits) byte() uint32 {
	if s.pos >= len(s.in) {
		return 0
	}
	s.pos++
	return uint32(s.in[s.pos-1])
}
func (s *bits) u16() uint32 {
	if len(s.in)-s.pos < 2 {
		return 0
	}
	s.pos += 2
	return uint32(binary.LittleEndian.Uint16(s.in[s.pos-2:]))
}

type huff struct {
	fast   [1 << fastBits]uint16 // sym<<4 | len, 0 = not in fast table
	first  [maxLen + 2]int
	count  [maxLen + 1]int
	offset [maxLen + 1]int
	sorted [numSyms]uint16
}

func (h *huff) build(lens *[numSyms]uint8) error {
	for _, l := range lens {
		h.count[l]++
	}
	h.count[0] = 0
	code, idx := 0, 0
	for l := 1; l <= maxLen; l++ {
		h.first[l], h.offset[l] = code, idx
		idx += h.count[l]
		code = (code + h.count[l]) << 1
	}
	if idx == 0 {
		return errors.New("empty Huffman table")
	}
	var next [maxLen + 1]int
	copy(next[:], h.offset[:])
	for sym, l := range lens {
		if l != 0 {
			h.sorted[next[l]] = uint16(sym)
			next[l]++
		}
	}
	// fast table for codes ≤ fastBits long
	for l := 1; l <= fastBits; l++ {
		for i := 0; i < h.count[l]; i++ {
			c := h.first[l] + i
			sym := h.sorted[h.offset[l]+i]
			lo, hi := c<<(fastBits-l), (c+1)<<(fastBits-l)
			if hi > len(h.fast) {
				return errors.New("over-subscribed Huffman code")
			}
			for j := lo; j < hi; j++ {
				h.fast[j] = sym<<4 | uint16(l)
			}
		}
	}
	return nil
}

func (h *huff) decode(s *bits) (int, error) {
	s.ensure(maxLen)
	if e := h.fast[s.peek(fastBits)]; e != 0 {
		s.drop(uint(e & 15))
		return int(e >> 4), nil
	}
	for l := fastBits + 1; l <= maxLen; l++ {
		c := int(s.peek(uint(l)))
		if d := c - h.first[l]; d >= 0 && d < h.count[l] {
			s.drop(uint(l))
			return int(h.sorted[h.offset[l]+d]), nil
		}
	}
	return 0, errors.New("invalid Huffman code")
}

func xpressHuff(in, out []byte) error {
	if len(in) < 256 {
		return errors.New("compressed chunk shorter than its Huffman table")
	}
	var lens [numSyms]uint8
	for i := 0; i < 256; i++ {
		lens[2*i], lens[2*i+1] = in[i]&15, in[i]>>4
	}
	var h huff
	if err := h.build(&lens); err != nil {
		return err
	}
	s := bits{in: in, pos: 256}
	o := 0
	for o < len(out) {
		sym, err := h.decode(&s)
		if err != nil {
			return err
		}
		if sym < 256 {
			out[o] = byte(sym)
			o++
			continue
		}
		length := uint32(sym & 15)
		lg := uint(sym>>4) & 15
		s.ensure(16)
		off := int(uint32(1)<<lg | s.pop(lg))
		if length == 15 {
			length += s.byte()
			if length == 15+255 {
				length = s.u16()
			}
		}
		length += 3
		if off > o || int(length) > len(out)-o {
			return fmt.Errorf("bad match (offset %d length %d at %d)", off, length, o)
		}
		for i := 0; i < int(length); i++ { // byte-wise: overlapping copies are allowed
			out[o] = out[o-off]
			o++
		}
	}
	if s.overflow {
		return errors.New("chunk ran past its end")
	}
	return nil
}

// PEChecksum returns the stored and the computed PE image checksum of b.
// ok is false when b is not a PE image or stores no checksum (then nothing can be checked).
func PEChecksum(b []byte) (stored, computed uint32, ok bool) {
	if len(b) < 64 || b[0] != 'M' || b[1] != 'Z' {
		return 0, 0, false
	}
	pe := int(binary.LittleEndian.Uint32(b[60:]))
	off := pe + 24 + 64
	if pe < 0 || off+4 > len(b) || string(b[pe:pe+4]) != "PE\x00\x00" {
		return 0, 0, false
	}
	stored = binary.LittleEndian.Uint32(b[off:])
	if stored == 0 {
		return 0, 0, false
	}
	var sum uint64
	for i := 0; i+1 < len(b); i += 2 {
		if i == off || i == off+2 {
			continue
		}
		sum += uint64(binary.LittleEndian.Uint16(b[i:]))
		sum = (sum & 0xffff) + (sum >> 16)
	}
	if len(b)%2 == 1 {
		sum += uint64(b[len(b)-1])
		sum = (sum & 0xffff) + (sum >> 16)
	}
	sum = (sum & 0xffff) + (sum >> 16)
	return stored, uint32(sum) + uint32(len(b)), true
}
