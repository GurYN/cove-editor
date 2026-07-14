// Package buffer provides Cove's text buffer: a rope of bytes plus a
// line-start index, giving O(lg n) edits and O(1) line addressing.
// The rope implementation is swappable; nothing outside this package
// touches it directly.
package buffer

import (
	"bytes"
	"sort"

	"github.com/zyedidia/generic/rope"
)

// Buffer is a mutable text buffer addressed by byte offset or (line, col).
// Lines and columns are 0-based; col is a byte offset within the line.
type Buffer struct {
	rope *rope.Node[byte]
	// lineStarts[i] is the byte offset of the first byte of line i.
	// lineStarts[0] is always 0. A '\n' at offset p starts a line at p+1,
	// so a buffer ending in '\n' has a final empty line, like every editor.
	// ponytail: flat []int index, O(lines) memmove per edit — swap for a
	// tree if profiling ever shows it (50µs on 200k lines today).
	lineStarts []int
}

// New creates a buffer from data. The slice is copied; callers keep ownership.
func New(data []byte) *Buffer {
	owned := make([]byte, len(data))
	copy(owned, data)
	b := &Buffer{rope: rope.New(owned)}
	b.lineStarts = append(b.lineStarts, 0)
	for i, c := range owned {
		if c == '\n' {
			b.lineStarts = append(b.lineStarts, i+1)
		}
	}
	return b
}

// Len returns the buffer size in bytes.
func (b *Buffer) Len() int { return b.rope.Len() }

// LineCount returns the number of lines. Never less than 1.
func (b *Buffer) LineCount() int { return len(b.lineStarts) }

// Line returns the content of line i without its trailing newline.
// The returned slice is only valid until the next edit.
func (b *Buffer) Line(i int) []byte {
	start := b.lineStarts[i]
	end := b.Len()
	if i+1 < len(b.lineStarts) {
		end = b.lineStarts[i+1] - 1 // drop the '\n'
	}
	if start >= end {
		return nil
	}
	return b.rope.Slice(start, end)
}

// LineLen returns the length of line i in bytes, excluding the newline.
func (b *Buffer) LineLen(i int) int {
	start := b.lineStarts[i]
	if i+1 < len(b.lineStarts) {
		return b.lineStarts[i+1] - 1 - start
	}
	return b.Len() - start
}

// Slice returns the bytes in [start, end). Only valid until the next edit.
func (b *Buffer) Slice(start, end int) []byte {
	if start >= end {
		return nil
	}
	return b.rope.Slice(start, end)
}

// Bytes returns the full buffer contents as a copy.
func (b *Buffer) Bytes() []byte {
	v := b.rope.Value()
	out := make([]byte, len(v))
	copy(out, v)
	return out
}

// Offset converts (line, col) to a byte offset, clamping col to line length.
func (b *Buffer) Offset(line, col int) int {
	if n := b.LineLen(line); col > n {
		col = n
	}
	return b.lineStarts[line] + col
}

// Pos converts a byte offset to (line, col).
func (b *Buffer) Pos(off int) (line, col int) {
	line = sort.Search(len(b.lineStarts), func(i int) bool {
		return b.lineStarts[i] > off
	}) - 1
	return line, off - b.lineStarts[line]
}

// Insert inserts text at byte offset off.
func (b *Buffer) Insert(off int, text []byte) {
	if len(text) == 0 {
		return
	}
	owned := make([]byte, len(text))
	copy(owned, text)
	b.rope.Insert(off, owned)

	// Line starts strictly after off shift right by len(text).
	idx := sort.Search(len(b.lineStarts), func(i int) bool {
		return b.lineStarts[i] > off
	})
	for i := idx; i < len(b.lineStarts); i++ {
		b.lineStarts[i] += len(text)
	}
	// Each '\n' in text opens a new line start at off+r+1.
	var newStarts []int
	for r := 0; ; {
		j := bytes.IndexByte(owned[r:], '\n')
		if j < 0 {
			break
		}
		r += j + 1
		newStarts = append(newStarts, off+r)
	}
	if len(newStarts) > 0 {
		b.lineStarts = append(b.lineStarts[:idx], append(newStarts, b.lineStarts[idx:]...)...)
	}
}

// Delete removes the bytes in [start, end).
func (b *Buffer) Delete(start, end int) {
	if start >= end {
		return
	}
	b.rope.Remove(start, end)

	// Line starts in (start, end] lose their newline: drop them.
	// Starts after end shift left.
	lo := sort.Search(len(b.lineStarts), func(i int) bool {
		return b.lineStarts[i] > start
	})
	hi := sort.Search(len(b.lineStarts), func(i int) bool {
		return b.lineStarts[i] > end
	})
	n := end - start
	b.lineStarts = append(b.lineStarts[:lo], b.lineStarts[hi:]...)
	for i := lo; i < len(b.lineStarts); i++ {
		b.lineStarts[i] -= n
	}
}
