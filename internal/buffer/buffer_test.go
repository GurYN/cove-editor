package buffer

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// naive is the reference implementation: a plain byte slice.
type naive struct{ data []byte }

func (n *naive) insert(off int, text []byte) {
	n.data = append(n.data[:off], append(append([]byte{}, text...), n.data[off:]...)...)
}
func (n *naive) delete(start, end int) {
	n.data = append(n.data[:start], n.data[end:]...)
}
func (n *naive) lines() []string {
	return strings.Split(string(n.data), "\n")
}

func check(t *testing.T, b *Buffer, ref *naive) {
	t.Helper()
	if got, want := string(b.Bytes()), string(ref.data); got != want {
		t.Fatalf("content mismatch:\ngot  %q\nwant %q", got, want)
	}
	lines := ref.lines()
	if b.LineCount() != len(lines) {
		t.Fatalf("LineCount = %d, want %d", b.LineCount(), len(lines))
	}
	for i, want := range lines {
		if got := string(b.Line(i)); got != want {
			t.Fatalf("Line(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestEditsAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	b := New([]byte("hello\nworld\n"))
	ref := &naive{data: []byte("hello\nworld\n")}
	check(t, b, ref)

	pieces := []string{"x", "\n", "ab\ncd", "\n\n", "long line without newline", ""}
	for range 2000 {
		if rng.Intn(2) == 0 || b.Len() == 0 {
			off := rng.Intn(b.Len() + 1)
			text := []byte(pieces[rng.Intn(len(pieces))])
			b.Insert(off, text)
			ref.insert(off, text)
		} else {
			start := rng.Intn(b.Len())
			end := start + rng.Intn(b.Len()-start) + 1
			b.Delete(start, end)
			ref.delete(start, end)
		}
		check(t, b, ref)
	}
}

func TestPosOffsetRoundTrip(t *testing.T) {
	b := New([]byte("ab\n\ncdef\n"))
	for off := 0; off <= b.Len(); off++ {
		line, col := b.Pos(off)
		if got := b.Offset(line, col); got != off {
			t.Errorf("off %d -> (%d,%d) -> %d", off, line, col, got)
		}
	}
	if b.LineCount() != 4 {
		t.Errorf("LineCount = %d, want 4", b.LineCount())
	}
}

func bigFile(lines int) []byte {
	var sb bytes.Buffer
	for i := range lines {
		fmt.Fprintf(&sb, "line %06d: the quick brown fox jumps over the lazy dog\n", i)
	}
	return sb.Bytes()
}

func BenchmarkInsert50k(b *testing.B) {
	buf := New(bigFile(50_000))
	mid := buf.Len() / 2
	for b.Loop() {
		buf.Insert(mid, []byte("x"))
	}
}

func BenchmarkInsertNewline200k(b *testing.B) {
	buf := New(bigFile(200_000))
	mid := buf.Len() / 2
	for b.Loop() {
		buf.Insert(mid, []byte("\n"))
	}
}
