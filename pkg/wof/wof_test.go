package wof

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// bitWriter mirrors the decoder: bits go MSB-first into 16-bit little-endian words;
// raw bytes (long match lengths) are appended at the current byte position.
type bitWriter struct {
	out  []byte
	acc  uint32
	n    uint
	hole int // index of the word being filled
}

func (w *bitWriter) put(v uint32, n uint) {
	for i := int(n) - 1; i >= 0; i-- {
		if w.n == 0 {
			w.hole = len(w.out)
			w.out = append(w.out, 0, 0)
		}
		w.acc = w.acc<<1 | (v>>uint(i))&1
		w.n++
		if w.n == 16 {
			binary.LittleEndian.PutUint16(w.out[w.hole:], uint16(w.acc))
			w.acc, w.n = 0, 0
		}
	}
}

func (w *bitWriter) flush() []byte {
	if w.n > 0 {
		binary.LittleEndian.PutUint16(w.out[w.hole:], uint16(w.acc<<(16-w.n)))
		w.n = 0
	}
	return append(w.out, 0, 0, 0, 0)
}

// all 512 symbols get 9-bit codes, so the canonical code of a symbol is the symbol itself
func encodeFlat(lits []byte, matches map[int][2]int) []byte {
	tbl := bytes.Repeat([]byte{0x99}, 256)
	var w bitWriter
	for i := 0; i < len(lits); {
		if m, ok := matches[i]; ok {
			off, length := m[0], m[1]
			lg := 0
			for 1<<(lg+1) <= off {
				lg++
			}
			l := length - 3
			if l > 15 {
				l = 15
			}
			w.put(uint32(256+lg<<4+l), 9)
			w.put(uint32(off-1<<lg), uint(lg))
			if l == 15 {
				w.flushTo(byte(length - 3 - 15))
			}
			i += length
			continue
		}
		w.put(uint32(lits[i]), 9)
		i++
	}
	return append(tbl, w.flush()...)
}

// flushTo appends a raw byte right after the bits written so far (decoder reads it
// after the 16-bit words it has already loaded).
func (w *bitWriter) flushTo(b byte) { w.out = append(w.out, b) }

func TestXpressLiteralsAndMatch(t *testing.T) {
	want := []byte("abcabcabcabcXYZ")
	// "abc" literals, then a match of 9 at offset 3, then "XYZ"
	comp := encodeFlat(want, map[int][2]int{3: {3, 9}})
	got := make([]byte, len(want))
	if err := xpressHuff(comp, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDecompressRawAndTable(t *testing.T) {
	// two 4K chunks: first stored raw (compressed size == chunk size), second compressed
	raw := bytes.Repeat([]byte{7}, 4096)
	tail := []byte("hello hello hello")
	c2 := encodeFlat(tail, map[int][2]int{6: {6, 11}})
	stream := make([]byte, 4)
	binary.LittleEndian.PutUint32(stream, uint32(len(raw))) // end of chunk 0
	stream = append(stream, raw...)
	stream = append(stream, c2...)
	got, err := Decompress(stream, int64(len(raw)+len(tail)), XPRESS4K)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, append(append([]byte{}, raw...), tail...)) {
		t.Fatalf("mismatch: %q", got[4096:])
	}
}

func TestParseReparse(t *testing.T) {
	b := []byte{0x17, 0, 0, 0x80, 0x10, 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0}
	if a, err := ParseReparse(b); err != nil || a != XPRESS8K {
		t.Fatalf("got %d %v", a, err)
	}
}
