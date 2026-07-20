package app

// In-editor merge-conflict resolution: the <<<<<<< / ======= / >>>>>>>
// blocks a failed merge leaves in the working file are highlighted (ours
// green, theirs blue) and resolved per block from what's on screen — accept
// ours, theirs, or both — as ordinary undoable edits. The git panel's o/t
// keys stay for whole-file resolution.

import (
	"bytes"
	"fmt"

	"github.com/GurYN/cove-editor/internal/editor"
)

// conflictBlock is one marker block, absolute byte offsets. mid/end fall
// back to the scan-window end when the block is cut off by the window.
type conflictBlock struct {
	start    int // "<<<<<<<" line start
	oursLo   int // first byte after the <<< line
	base     int // "|||||||" line start (diff3 style), -1 if absent
	mid      int // "=======" line start
	theirsLo int // first byte after the === line
	theirsHi int // ">>>>>>>" line start
	end      int // first byte after the >>> line
}

func (b conflictBlock) ours(src []byte) []byte {
	hi := b.mid
	if b.base >= 0 {
		hi = b.base
	}
	return src[b.oursLo:hi]
}
func (b conflictBlock) theirs(src []byte) []byte { return src[b.theirsLo:b.theirsHi] }

// conflictBlocks parses marker blocks intersecting [lo, hi). lo is rewound
// to a line start; a block truncated by the window closes at the window end
// (only its on-screen part matters for highlighting).
func conflictBlocks(src []byte, lo, hi int) []conflictBlock {
	lo = bytes.LastIndexByte(src[:min(lo, len(src))], '\n') + 1
	hi = min(hi, len(src))
	var bs []conflictBlock
	var cur *conflictBlock
	for off := lo; off < hi; {
		next := hi
		if nl := bytes.IndexByte(src[off:hi], '\n'); nl >= 0 {
			next = off + nl + 1
		}
		line := src[off:next]
		switch {
		case bytes.HasPrefix(line, []byte("<<<<<<<")):
			cur = &conflictBlock{start: off, oursLo: next, base: -1, mid: -1}
		case cur != nil && cur.mid < 0 && bytes.HasPrefix(line, []byte("|||||||")):
			cur.base = off
		case cur != nil && cur.mid < 0 && bytes.HasPrefix(line, []byte("=======")):
			cur.mid, cur.theirsLo = off, next
		case cur != nil && cur.mid >= 0 && bytes.HasPrefix(line, []byte(">>>>>>>")):
			cur.theirsHi, cur.end = off, next
			bs = append(bs, *cur)
			cur = nil
		}
		off = next
	}
	if cur != nil { // window cut the block: close it at the edge
		if cur.mid < 0 {
			cur.mid, cur.theirsLo = hi, hi
		}
		cur.theirsHi, cur.end = hi, hi
		bs = append(bs, *cur)
	}
	return bs
}

// backscan covers a block whose <<< line scrolled above the viewport.
// ponytail: a single conflict block larger than 16KB highlights wrong;
// grow the constant if that ever bites.
const conflictBackscan = 16 << 10

// conflictSyntax overlays conflict-block styling on the language syntax
// (which may be nil for plain-text files). Later spans win in the renderer,
// so the overlay repaints the block regardless of language colors.
type conflictSyntax struct{ inner editor.Syntax }

func (c conflictSyntax) Edit(startOff, oldEndOff, newEndOff int, start, oldEnd, newEnd [2]int) {
	if c.inner != nil {
		c.inner.Edit(startOff, oldEndOff, newEndOff, start, oldEnd, newEnd)
	}
}

func (c conflictSyntax) Expand(src []byte, lo, hi int) (int, int, bool) {
	if c.inner != nil {
		return c.inner.Expand(src, lo, hi)
	}
	return 0, 0, false
}

func (c conflictSyntax) Spans(src []byte, startOff, endOff int) []editor.HLSpan {
	var spans []editor.HLSpan
	if c.inner != nil {
		spans = c.inner.Spans(src, startOff, endOff)
	}
	for _, b := range conflictBlocks(src, max(0, startOff-conflictBackscan), endOff) {
		if b.end <= startOff {
			continue
		}
		spans = append(spans,
			editor.HLSpan{Start: b.start, End: b.mid, Class: editor.ClassMergeOurs},
			editor.HLSpan{Start: b.mid, End: b.end, Class: editor.ClassMergeTheirs})
	}
	return spans
}

// mergeAccept resolves the conflict block under (or next after) the cursor:
// which is "ours", "theirs", or "both". The replacement is one undoable edit.
func (m *Model) mergeAccept(which string) {
	d := m.doc()
	if d == nil || d.virtual {
		return
	}
	src := d.ed.Buf.Bytes()
	bs := conflictBlocks(src, 0, len(src))
	if len(bs) == 0 {
		m.lastMsg = "no conflict markers in this file"
		return
	}
	line, _ := d.ed.Cursor()
	cur := d.ed.Buf.Offset(line, 0)
	b := bs[0]
	for _, cand := range bs { // block containing the cursor, else the next one
		if cand.end > cur {
			b = cand
			break
		}
	}
	var repl []byte
	switch which {
	case "ours":
		repl = b.ours(src)
	case "theirs":
		repl = b.theirs(src)
	default:
		repl = append(append([]byte{}, b.ours(src)...), b.theirs(src)...)
	}
	d.ed.ApplyEdits([]editor.Edit{{Off: b.start, Old: append([]byte{}, src[b.start:b.end]...), New: repl}})
	ln, _ := d.ed.Buf.Pos(b.start)
	d.ed.Go(ln, 0)
	if left := len(bs) - 1; left > 0 {
		m.lastMsg = fmt.Sprintf("kept %s — %d conflict(s) left", which, left)
	} else {
		m.lastMsg = "kept " + which + " — all resolved: save, then stage"
	}
}

// mergeNext jumps to the next conflict block below the cursor, wrapping.
func (m *Model) mergeNext() {
	d := m.doc()
	if d == nil || d.virtual {
		return
	}
	src := d.ed.Buf.Bytes()
	bs := conflictBlocks(src, 0, len(src))
	if len(bs) == 0 {
		m.lastMsg = "no conflict markers in this file"
		return
	}
	line, _ := d.ed.Cursor()
	cur := d.ed.Buf.Offset(line, 0)
	b := bs[0] // wrap default
	for _, cand := range bs {
		if cand.start > cur {
			b = cand
			break
		}
	}
	ln, _ := d.ed.Buf.Pos(b.start)
	d.ed.Go(ln, 0)
	m.lastMsg = fmt.Sprintf("%d conflict(s) — ^P Merge: Accept Ours / Theirs / Both", len(bs))
}
