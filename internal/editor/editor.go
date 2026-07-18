// Package editor implements Cove's editor pane: a virtualized viewport over
// a buffer.Buffer with multi-cursor editing, undo/redo, and search. It
// renders only visible lines and never hands the whole file to Bubbletea's
// render loop — the PRD §5.2 boundary.
package editor

import (
	"bytes"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/buffer"
)

// Cursor is an anchor+head pair of byte offsets. Anchor == Head means no
// selection; Head is where the caret blinks.
type Cursor struct {
	Anchor, Head int
	wantCol      int // sticky byte-column for vertical movement
}

func (c Cursor) sel() (int, int) {
	if c.Anchor <= c.Head {
		return c.Anchor, c.Head
	}
	return c.Head, c.Anchor
}

func (c Cursor) hasSel() bool { return c.Anchor != c.Head }

// Syntax is what the editor needs from a highlighter; wired by the app so
// this package stays free of the CGo dependency.
type Syntax interface {
	// Edit reports a buffer mutation. Pre-points describe the buffer before
	// the change, newEnd after; the implementation reparses lazily.
	Edit(startOff, oldEndOff, newEndOff int, start, oldEnd, newEnd [2]int)
	// Spans returns highlight spans intersecting [startOff, endOff),
	// sorted by Start.
	Spans(src []byte, startOff, endOff int) []HLSpan
	// Expand grows [lo, hi) to the smallest enclosing syntax node strictly
	// larger than the current range. ok is false at the root.
	Expand(src []byte, lo, hi int) (int, int, bool)
}

// HLSpan is a highlighted byte range with a style class.
type HLSpan struct {
	Start, End int
	Class      int // one of the Class* constants in style.go
}

// DiagSpan is a diagnostic mapped to byte offsets, ready to render.
type DiagSpan struct {
	Start, End int
	Severity   int // 1 error, 2 warning, 3+ info
	Message    string
}

type Model struct {
	Buf      *buffer.Buffer
	Width    int
	Height   int
	Syntax   Syntax
	Dirty    bool
	ReadOnly bool       // rejects every mutation; navigation/selection still work
	Rev      int        // bumped on every buffer mutation; drives LSP sync
	Diags    []DiagSpan // set by the app; offsets clamped at render time
	Signs    []byte     // git gutter sign per line ('a'/'m'/'d', 0 none); set by the app

	cursors []Cursor // sorted by sel start, non-overlapping, len >= 1
	primary int      // index into cursors; scroll follows this one
	top     int      // first visible line
	xoff    int      // horizontal scroll, in screen cells
	hist    history
	search  searchState
	clip    []byte // internal clipboard; OSC 52 integration is Phase 2

	lastClickAt  time.Time
	lastClickPos int
	dragging     bool
}

func New(buf *buffer.Buffer) Model {
	return Model{Buf: buf, cursors: []Cursor{{}}}
}

func (m Model) Init() tea.Cmd { return nil }

// Cursor returns the primary cursor's (line, col).
func (m Model) Cursor() (int, int) { return m.Buf.Pos(m.cursors[m.primary].Head) }

// CursorCount returns the number of active cursors.
func (m Model) CursorCount() int { return len(m.cursors) }

// Go places a single cursor at (line, col) and scrolls it into view.
func (m *Model) Go(line, col int) {
	line = clamp(line, 0, m.Buf.LineCount()-1)
	off := m.Buf.Offset(line, col)
	m.cursors = []Cursor{{Anchor: off, Head: off, wantCol: col}}
	m.primary = 0
	m.scrollToCursor()
}

// Center scrolls so the primary cursor's line sits mid-viewport — used
// after jumps (problems, go-to-definition) so the target has context.
func (m *Model) Center() {
	if m.Height <= 0 {
		return
	}
	line, _ := m.Buf.Pos(m.cursors[m.primary].Head)
	m.top = clamp(line-m.Height/2, 0, max(0, m.Buf.LineCount()-m.Height))
}

// Selection returns the primary cursor's selected text, nil if none.
func (m Model) Selection() []byte {
	lo, hi := m.cursors[m.primary].sel()
	return m.Buf.Slice(lo, hi)
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.handleKey(msg)
	case tea.MouseMsg:
		m.handleMouse(msg)
	}
	return m, nil
}

// handleKey covers typing only (runes, space, enter, tab, deletion) — every
// other key is a registry action so rebinding stays the single source of truth.
func (m *Model) handleKey(k tea.KeyMsg) {
	switch k.Type {
	case tea.KeyRunes:
		if k.Alt {
			return // alt-chords are not text
		}
		m.InsertText(string(k.Runes))
	case tea.KeySpace:
		m.InsertText(" ")
	case tea.KeyEnter:
		m.InsertText("\n")
	case tea.KeyTab:
		if m.selSpansLines() {
			m.IndentLines(+1)
		} else {
			m.InsertText("\t")
		}
	case tea.KeyBackspace:
		m.deleteAtCursors(-1)
	case tea.KeyDelete:
		m.deleteAtCursors(+1)
	default:
		return
	}
	m.scrollToCursor()
}

// ---- exported operations (the registry calls these) ----

func (m *Model) MoveH(dir int, extend bool)    { m.moveH(dir, extend); m.scrollToCursor() }
func (m *Model) MoveV(delta int, extend bool)  { m.moveV(delta, extend); m.scrollToCursor() }
func (m *Model) MoveWord(dir int, extend bool) { m.moveWord(dir, extend); m.scrollToCursor() }
func (m *Model) Page(dir int)                  { m.moveV(dir*max(1, m.Height-1), false); m.scrollToCursor() }

// LineEdge moves every cursor to the start (dir<0) or end (dir>0) of its line.
func (m *Model) LineEdge(dir int, extend bool) {
	m.eachCursor(func(c *Cursor) {
		line, _ := m.Buf.Pos(c.Head)
		if dir < 0 {
			c.Head = m.Buf.Offset(line, 0)
		} else {
			c.Head = m.Buf.Offset(line, m.Buf.LineLen(line))
		}
		_, c.wantCol = m.Buf.Pos(c.Head)
		if !extend {
			c.Anchor = c.Head
		}
	})
	m.scrollToCursor()
}

func (m *Model) SelectAll() {
	m.cursors = []Cursor{{Anchor: 0, Head: m.Buf.Len()}}
	m.primary = 0
}

// Collapse reduces to a single cursor and clears the search — Esc.
func (m *Model) Collapse() {
	m.cursors = []Cursor{m.cursors[m.primary]}
	m.cursors[0].Anchor = m.cursors[0].Head
	m.primary = 0
	m.search.clear()
}

func (m *Model) SelectNext()       { m.selectNextOccurrence(); m.scrollToCursor() }
func (m *Model) ExpandSelection()  { m.expandSelection() }
func (m *Model) AddCursor(dir int) { m.addCursorVert(dir); m.scrollToCursor() }
func (m *Model) Copy()             { m.copySelection(false) }
func (m *Model) Cut()              { m.copySelection(true) }
func (m *Model) Paste()            { m.paste() }
func (m *Model) DeleteRune(dir int) {
	m.deleteAtCursors(dir)
	m.scrollToCursor()
}

// ---- movement ----

func (m *Model) eachCursor(f func(*Cursor)) {
	for i := range m.cursors {
		f(&m.cursors[i])
	}
	m.normalize()
}

func (m *Model) moveH(dir int, extend bool) {
	m.eachCursor(func(c *Cursor) {
		if c.hasSel() && !extend {
			lo, hi := c.sel()
			if dir < 0 {
				c.Head = lo
			} else {
				c.Head = hi
			}
		} else if dir < 0 {
			c.Head = m.prevRune(c.Head)
		} else {
			c.Head = m.nextRune(c.Head)
		}
		if !extend {
			c.Anchor = c.Head
		}
		_, c.wantCol = m.Buf.Pos(c.Head)
	})
}

func (m *Model) moveWord(dir int, extend bool) {
	m.eachCursor(func(c *Cursor) {
		c.Head = m.wordBoundary(c.Head, dir)
		if !extend {
			c.Anchor = c.Head
		}
		_, c.wantCol = m.Buf.Pos(c.Head)
	})
}

func (m *Model) moveV(delta int, extend bool) {
	m.eachCursor(func(c *Cursor) {
		line, _ := m.Buf.Pos(c.Head)
		line = clamp(line+delta, 0, m.Buf.LineCount()-1)
		c.Head = m.snapRune(m.Buf.Offset(line, c.wantCol))
		if !extend {
			c.Anchor = c.Head
		}
	})
}

// snapRune steps off back to the start of the rune it falls inside — wantCol
// is a byte column from a different line and can land mid-sequence.
func (m *Model) snapRune(off int) int {
	for off > 0 && off < m.Buf.Len() && isCont(m.byteAt(off)) {
		off--
	}
	return off
}

// prevRune/nextRune step one rune, crossing line boundaries.
func (m *Model) prevRune(off int) int {
	if off == 0 {
		return 0
	}
	off--
	for off > 0 && isCont(m.byteAt(off)) {
		off--
	}
	return off
}

func (m *Model) nextRune(off int) int {
	n := m.Buf.Len()
	if off >= n {
		return n
	}
	off++
	for off < n && isCont(m.byteAt(off)) {
		off++
	}
	return off
}

func (m *Model) byteAt(off int) byte { return m.Buf.Slice(off, off+1)[0] }

func isCont(b byte) bool { return b&0xC0 == 0x80 }

func isWordByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' ||
		b >= '0' && b <= '9' || b >= 0x80
}

func (m *Model) wordBoundary(off, dir int) int {
	n := m.Buf.Len()
	if dir > 0 {
		for off < n && !isWordByte(m.byteAt(off)) {
			off++
		}
		for off < n && isWordByte(m.byteAt(off)) {
			off++
		}
	} else {
		for off > 0 && !isWordByte(m.byteAt(off-1)) {
			off--
		}
		for off > 0 && isWordByte(m.byteAt(off-1)) {
			off--
		}
	}
	return off
}

// normalize sorts cursors and merges overlapping ones, keeping track of the
// primary.
func (m *Model) normalize() {
	p := m.cursors[m.primary]
	sort.Slice(m.cursors, func(i, j int) bool {
		li, _ := m.cursors[i].sel()
		lj, _ := m.cursors[j].sel()
		return li < lj
	})
	out := m.cursors[:1]
	for _, c := range m.cursors[1:] {
		last := &out[len(out)-1]
		llo, lhi := last.sel()
		clo, chi := c.sel()
		if clo < lhi || (clo == lhi && !last.hasSel() && !c.hasSel()) {
			// overlap: extend last to cover both
			if chi > lhi {
				if last.Head >= last.Anchor {
					last.Head = chi
				} else {
					last.Anchor = chi
				}
			}
			_ = llo
			continue
		}
		out = append(out, c)
	}
	m.cursors = out
	m.primary = 0
	for i, c := range m.cursors {
		if c.Head == p.Head {
			m.primary = i
			break
		}
	}
}

func (m *Model) scrollToCursor() {
	line, _ := m.Buf.Pos(m.cursors[m.primary].Head)
	if line < m.top {
		m.top = line
	}
	if m.Height > 0 && line >= m.top+m.Height {
		m.top = line - m.Height + 1
	}
	m.keepCursorHVisible()
}

// ---- editing ----

// InsertText replaces every cursor's selection with s. Line endings are
// normalized to \n and stray control characters dropped — pastes and fast
// typing arrive as raw chunks and must not put control bytes in the buffer.
func (m *Model) InsertText(s string) {
	s = sanitize(s)
	if s == "" {
		return
	}
	text := []byte(s)
	edits := make([]Edit, len(m.cursors))
	typing := true
	for i, c := range m.cursors {
		lo, hi := c.sel()
		edits[i] = Edit{Off: lo, Old: append([]byte(nil), m.Buf.Slice(lo, hi)...), New: text}
		if c.hasSel() {
			typing = false
		}
	}
	m.apply(Tx{Edits: edits, typing: typing})
}

// deleteAtCursors deletes selections, or one rune before (dir<0) / after
// (dir>0) each cursor.
func (m *Model) deleteAtCursors(dir int) {
	edits := make([]Edit, 0, len(m.cursors))
	for _, c := range m.cursors {
		lo, hi := c.sel()
		if !c.hasSel() {
			if dir < 0 {
				lo = m.prevRune(hi)
			} else {
				hi = m.nextRune(lo)
			}
		}
		if lo == hi {
			continue
		}
		edits = append(edits, Edit{Off: lo, Old: append([]byte(nil), m.Buf.Slice(lo, hi)...)})
	}
	if len(edits) == 0 {
		return
	}
	m.apply(Tx{Edits: edits})
}

// apply runs tx against the buffer, records it, and repositions cursors.
// A tx with pre-set After keeps those cursors (line ops preserve selections);
// otherwise one cursor lands at the end of each edit.
func (m *Model) apply(tx Tx) {
	if m.ReadOnly { // nothing ever enters hist, so undo/redo stay no-ops too
		return
	}
	tx.Before = append([]Cursor(nil), m.cursors...)
	tx.at = time.Now()
	m.applyEdits(tx.Edits)

	if tx.After == nil {
		// Place one cursor at the end of each edit, in post-tx coordinates.
		after := make([]Cursor, len(tx.Edits))
		delta := 0
		for i, e := range tx.Edits {
			end := e.Off + delta + len(e.New)
			_, col := m.Buf.Pos(end)
			after[i] = Cursor{Anchor: end, Head: end, wantCol: col}
			delta += len(e.New) - len(e.Old)
		}
		tx.After = after
		m.primary = len(tx.After) - 1
	} else {
		m.primary = min(m.primary, len(tx.After)-1)
	}
	m.cursors = append([]Cursor(nil), tx.After...)
	m.normalize()
	m.hist.push(tx)
	m.Dirty = true
	m.search.dirty = true
	m.scrollToCursor()
}

// ApplyEdits applies external (LSP) edits as one undoable transaction.
// Edits must be non-overlapping, ascending, in current-buffer offsets.
func (m *Model) ApplyEdits(edits []Edit) {
	if len(edits) == 0 {
		return
	}
	m.apply(Tx{Edits: edits})
}

// DiagUnderCursor returns the diagnostic covering the primary cursor.
func (m *Model) DiagUnderCursor() (DiagSpan, bool) {
	head := m.cursors[m.primary].Head
	for _, d := range m.Diags {
		if head >= d.Start && head <= d.End {
			return d, true
		}
	}
	return DiagSpan{}, false
}

// DiagCounts returns (errors, warnings, infos+hints).
func (m *Model) DiagCounts() (int, int, int) {
	e, w, i := 0, 0, 0
	for _, d := range m.Diags {
		switch {
		case d.Severity <= 1:
			e++
		case d.Severity == 2:
			w++
		default:
			i++
		}
	}
	return e, w, i
}

// applyEdits mutates the buffer (descending order keeps offsets valid) and
// notifies the highlighter with pre/post points.
func (m *Model) applyEdits(edits []Edit) {
	m.Rev++
	for _, e := range slices.Backward(edits) {
		oldEnd := e.Off + len(e.Old)
		var sp, oep [2]int
		if m.Syntax != nil {
			sl, sc := m.Buf.Pos(e.Off)
			ol, oc := m.Buf.Pos(oldEnd)
			sp, oep = [2]int{sl, sc}, [2]int{ol, oc}
		}
		if len(e.Old) > 0 {
			m.Buf.Delete(e.Off, oldEnd)
		}
		if len(e.New) > 0 {
			m.Buf.Insert(e.Off, e.New)
		}
		if m.Syntax != nil {
			newEnd := e.Off + len(e.New)
			nl, nc := m.Buf.Pos(newEnd)
			m.Syntax.Edit(e.Off, oldEnd, newEnd, sp, oep, [2]int{nl, nc})
		}
	}
}

// UndoStep reverts the most recent transaction.
func (m *Model) UndoStep() {
	tx, ok := m.hist.popUndo()
	if !ok {
		return
	}
	m.applyEdits(tx.inverse())
	m.cursors = append([]Cursor(nil), tx.Before...)
	m.primary = len(m.cursors) - 1
	m.normalize()
	m.Dirty = true
	m.search.dirty = true
	m.scrollToCursor()
}

// RedoStep re-applies the most recently undone transaction.
func (m *Model) RedoStep() {
	tx, ok := m.hist.popRedo()
	if !ok {
		return
	}
	m.applyEdits(tx.Edits)
	m.cursors = append([]Cursor(nil), tx.After...)
	m.primary = len(m.cursors) - 1
	m.normalize()
	m.Dirty = true
	m.search.dirty = true
	m.scrollToCursor()
}

// ---- multi-cursor ----

// selectWordAtPrimary selects the word under the primary cursor when it has
// no selection. Reports whether the primary ends up with one.
func (m *Model) selectWordAtPrimary() bool {
	p := &m.cursors[m.primary]
	if p.hasSel() {
		return true
	}
	lo := m.wordBoundary(m.nextRune(p.Head), -1)
	hi := m.wordBoundary(lo, +1)
	if lo == hi {
		return false
	}
	p.Anchor, p.Head = lo, hi
	return true
}

// selectNextOccurrence (Ctrl+D): select word under cursor, or add a cursor
// at the next occurrence of the current selection.
func (m *Model) selectNextOccurrence() {
	p := &m.cursors[m.primary]
	if !p.hasSel() {
		m.selectWordAtPrimary()
		return
	}
	lo, hi := p.sel()
	needle := append([]byte(nil), m.Buf.Slice(lo, hi)...)
	src := m.Buf.Bytes()
	// Search after the last cursor, wrapping around.
	last := 0
	for _, c := range m.cursors {
		if _, h := c.sel(); h > last {
			last = h
		}
	}
	idx := indexFrom(src, needle, last)
	if idx < 0 {
		return
	}
	m.cursors = append(m.cursors, Cursor{Anchor: idx, Head: idx + len(needle)})
	m.primary = len(m.cursors) - 1
	m.normalize()
}

func indexFrom(src, needle []byte, from int) int {
	if from < len(src) {
		if i := bytes.Index(src[from:], needle); i >= 0 {
			return from + i
		}
	}
	return bytes.Index(src[:min(from, len(src))], needle)
}

// expandSelection grows each cursor to the enclosing syntax node
// (structural selection). No-op without a highlighter.
func (m *Model) expandSelection() {
	if m.Syntax == nil {
		return
	}
	src := m.Buf.Bytes()
	m.eachCursor(func(c *Cursor) {
		lo, hi := c.sel()
		if nlo, nhi, ok := m.Syntax.Expand(src, lo, hi); ok {
			c.Anchor, c.Head = nlo, nhi
		}
	})
}

// addCursorVert adds a cursor on the line above/below the outermost cursor.
func (m *Model) addCursorVert(dir int) {
	src := m.cursors[0]
	if dir > 0 {
		src = m.cursors[len(m.cursors)-1]
	}
	line, _ := m.Buf.Pos(src.Head)
	line += dir
	if line < 0 || line >= m.Buf.LineCount() {
		return
	}
	off := m.snapRune(m.Buf.Offset(line, src.wantCol))
	m.cursors = append(m.cursors, Cursor{Anchor: off, Head: off, wantCol: src.wantCol})
	m.primary = len(m.cursors) - 1
	m.normalize()
}

// ---- line operations ----

// selSpansLines reports whether any cursor's selection covers more than one
// line — the Tab-means-indent test.
func (m *Model) selSpansLines() bool {
	for _, c := range m.cursors {
		lo, hi := c.sel()
		la, _ := m.Buf.Pos(lo)
		lb, _ := m.Buf.Pos(hi)
		if lb > la {
			return true
		}
	}
	return false
}

// selectedLines returns the sorted set of lines touched by any cursor.
// A selection ending at column 0 excludes that line (matches VSCode).
func (m *Model) selectedLines() []int {
	seen := map[int]bool{}
	for _, c := range m.cursors {
		lo, hi := c.sel()
		a, _ := m.Buf.Pos(lo)
		b, bc := m.Buf.Pos(hi)
		if b > a && bc == 0 {
			b--
		}
		for l := a; l <= b; l++ {
			seen[l] = true
		}
	}
	lines := make([]int, 0, len(seen))
	for l := range seen {
		lines = append(lines, l)
	}
	sort.Ints(lines)
	return lines
}

// remapCursors maps the current cursors through a set of ascending,
// non-overlapping edits so line ops keep selections instead of collapsing
// them the way typing does. Call before apply; pass the result as Tx.After.
func (m *Model) remapCursors(edits []Edit) []Cursor {
	shift := func(off int) int {
		d := 0
		for _, e := range edits {
			if e.Off > off {
				break
			}
			if off < e.Off+len(e.Old) { // inside an edited range: clamp into it
				return e.Off + d + min(off-e.Off, len(e.New))
			}
			d += len(e.New) - len(e.Old)
		}
		return off + d
	}
	out := make([]Cursor, len(m.cursors))
	for i, c := range m.cursors {
		out[i] = Cursor{Anchor: shift(c.Anchor), Head: shift(c.Head), wantCol: c.wantCol}
	}
	return out
}

// IndentLines shifts every selected line right (dir>0: insert a tab) or
// left (dir<0: strip one leading tab or up to 4 leading spaces).
func (m *Model) IndentLines(dir int) {
	var edits []Edit
	for _, l := range m.selectedLines() {
		off := m.Buf.Offset(l, 0)
		if dir > 0 {
			if m.Buf.LineLen(l) == 0 {
				continue // don't indent blank lines
			}
			edits = append(edits, Edit{Off: off, New: []byte("\t")})
			continue
		}
		head := m.Buf.Slice(off, off+min(4, m.Buf.LineLen(l)))
		n := 0
		if len(head) > 0 && head[0] == '\t' {
			n = 1
		} else {
			for n < len(head) && head[n] == ' ' {
				n++
			}
		}
		if n > 0 {
			edits = append(edits, Edit{Off: off, Old: append([]byte(nil), head[:n]...)})
		}
	}
	if len(edits) == 0 {
		return
	}
	m.apply(Tx{Edits: edits, After: m.remapCursors(edits)})
}

// ToggleComment strips prefix from every selected line if all non-blank
// lines carry it, otherwise adds it after the leading whitespace.
func (m *Model) ToggleComment(prefix string) {
	if prefix == "" {
		return // language without line comments
	}
	p, ins := []byte(prefix), []byte(prefix+" ")
	lines := m.selectedLines()
	any, all := false, true
	for _, l := range lines {
		off := m.Buf.Offset(l, 0)
		body := bytes.TrimLeft(m.Buf.Slice(off, off+m.Buf.LineLen(l)), " \t")
		if len(body) == 0 {
			continue
		}
		any = true
		if !bytes.HasPrefix(body, p) {
			all = false
		}
	}
	if !any {
		return
	}
	var edits []Edit
	for _, l := range lines {
		off := m.Buf.Offset(l, 0)
		text := m.Buf.Slice(off, off+m.Buf.LineLen(l))
		ws := len(text) - len(bytes.TrimLeft(text, " \t"))
		body := text[ws:]
		if len(body) == 0 {
			continue
		}
		if all { // uncomment: prefix plus one following space if present
			n := len(p)
			if len(body) > n && body[n] == ' ' {
				n++
			}
			edits = append(edits, Edit{Off: off + ws, Old: append([]byte(nil), body[:n]...)})
		} else if !bytes.HasPrefix(body, p) {
			edits = append(edits, Edit{Off: off + ws, New: ins})
		}
	}
	if len(edits) == 0 {
		return
	}
	m.apply(Tx{Edits: edits, After: m.remapCursors(edits)})
}

// DuplicateLine copies each cursor's line below itself.
func (m *Model) DuplicateLine() {
	var edits []Edit
	for _, l := range m.selectedLines() {
		start := m.Buf.Offset(l, 0)
		end := start + m.Buf.LineLen(l)
		text := append([]byte("\n"), m.Buf.Slice(start, end)...)
		edits = append(edits, Edit{Off: end, New: text})
	}
	m.apply(Tx{Edits: edits, After: m.remapCursors(edits)})
}

// DeleteLine removes each cursor's whole line.
func (m *Model) DeleteLine() {
	lines := m.selectedLines()
	var edits []Edit
	for i, l := range lines {
		lo := m.Buf.Offset(l, 0)
		hi := lo + m.Buf.LineLen(l)
		if l+1 < m.Buf.LineCount() {
			hi++ // trailing newline
		} else if lo > 0 && (i == 0 || lines[i-1] != l-1) {
			lo-- // last line: eat the preceding newline instead
		}
		edits = append(edits, Edit{Off: lo, Old: append([]byte(nil), m.Buf.Slice(lo, hi)...)})
	}
	m.apply(Tx{Edits: edits})
}

// MoveLine swaps the primary cursor's line with its neighbor above/below.
// ponytail: primary cursor only; multi-cursor move-line when someone misses it.
func (m *Model) MoveLine(dir int) {
	line, col := m.Buf.Pos(m.cursors[m.primary].Head)
	other := line + dir
	if other < 0 || other >= m.Buf.LineCount() {
		return
	}
	a := min(line, other) // moving always swaps lines a and a+1
	aStart := m.Buf.Offset(a, 0)
	bStart := m.Buf.Offset(a+1, 0)
	bEnd := bStart + m.Buf.LineLen(a+1)
	aText := append([]byte(nil), m.Buf.Slice(aStart, bStart-1)...)
	bText := append([]byte(nil), m.Buf.Slice(bStart, bEnd)...)
	swapped := append(append(append([]byte(nil), bText...), '\n'), aText...)
	old := append([]byte(nil), m.Buf.Slice(aStart, bEnd)...)
	var head int
	if dir < 0 {
		head = aStart + min(col, len(bText))
	} else {
		head = aStart + len(bText) + 1 + min(col, len(aText))
	}
	m.apply(Tx{Edits: []Edit{{Off: aStart, Old: old, New: swapped}},
		After: []Cursor{{Anchor: head, Head: head, wantCol: col}}})
}

// SelectAllOccurrences selects every match of the primary selection (or of
// the word under the cursor).
func (m *Model) SelectAllOccurrences() {
	if !m.selectWordAtPrimary() {
		return
	}
	p := &m.cursors[m.primary]
	lo, hi := p.sel()
	needle := append([]byte(nil), m.Buf.Slice(lo, hi)...)
	src := m.Buf.Bytes()
	var cs []Cursor
	for i := 0; ; {
		j := bytes.Index(src[i:], needle)
		if j < 0 {
			break
		}
		start := i + j
		cs = append(cs, Cursor{Anchor: start, Head: start + len(needle)})
		if start == lo {
			m.primary = len(cs) - 1
		}
		i = start + len(needle)
	}
	m.cursors = cs
	m.normalize()
	m.scrollToCursor()
}

// ---- clipboard (internal) ----

func (m *Model) copySelection(cut bool) {
	var parts [][]byte
	for _, c := range m.cursors {
		lo, hi := c.sel()
		if lo < hi {
			parts = append(parts, append([]byte(nil), m.Buf.Slice(lo, hi)...))
		}
	}
	if len(parts) == 0 {
		return
	}
	m.clip = joinBytes(parts, '\n')
	if cut {
		m.deleteAtCursors(0) // selections only; dir irrelevant
	}
}

func (m *Model) paste() {
	if len(m.clip) == 0 {
		return
	}
	m.InsertText(string(m.clip))
	m.hist.seal() // paste is its own undo step; typing after it must not merge
}

// ---- mouse ----

// maxVisibleCellWidth is the widest visible line in screen cells, probed only
// one wheel step past the current window (megabyte lines must stay O(window)).
func (m *Model) maxVisibleCellWidth() int {
	probe := m.xoff + m.textWidth() + 6
	w := 0
	for line := m.top; line < min(m.top+m.Height, m.Buf.LineCount()); line++ {
		start := m.Buf.Offset(line, 0)
		maxB := min(m.Buf.LineLen(line), probe*4+8)
		if cells := lineCellsTo(m.Buf.Slice(start, start+maxB), probe); len(cells) > 0 {
			w = max(w, cells[len(cells)-1].x+1)
		}
	}
	return w
}

func (m *Model) handleMouse(msg tea.MouseMsg) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.top = max(0, m.top-3)
		return
	case tea.MouseButtonWheelDown:
		m.top = min(max(0, m.Buf.LineCount()-m.Height), m.top+3)
		return
	case tea.MouseButtonWheelLeft:
		m.xoff = max(0, m.xoff-6)
		return
	case tea.MouseButtonWheelRight:
		m.xoff = min(max(0, m.maxVisibleCellWidth()-m.textWidth()), m.xoff+6)
		return
	}
	if msg.Button != tea.MouseButtonLeft && msg.Action != tea.MouseActionMotion {
		return
	}
	off := m.hitTest(msg.X, msg.Y)
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Alt {
			m.cursors = append(m.cursors, Cursor{Anchor: off, Head: off})
			m.primary = len(m.cursors) - 1
			m.normalize()
			return
		}
		if time.Since(m.lastClickAt) < 400*time.Millisecond && off == m.lastClickPos {
			// double-click: select word
			lo := m.wordBoundary(m.nextRune(off), -1)
			hi := m.wordBoundary(lo, +1)
			m.cursors = []Cursor{{Anchor: lo, Head: hi}}
			m.primary = 0
			return
		}
		m.lastClickAt, m.lastClickPos = time.Now(), off
		m.cursors = []Cursor{{Anchor: off, Head: off}}
		m.primary = 0
		m.dragging = true
		_, m.cursors[0].wantCol = m.Buf.Pos(off)
	case tea.MouseActionMotion:
		if m.dragging {
			m.cursors[m.primary].Head = off
		}
	case tea.MouseActionRelease:
		m.dragging = false
	}
}

// hitTest maps a screen cell to a byte offset. x includes the gutter.
func (m *Model) hitTest(x, y int) int {
	line := clamp(m.top+y, 0, m.Buf.LineCount()-1)
	want := max(0, x-m.gutterW()) + m.xoff
	start := m.Buf.Offset(line, 0)
	maxB := min(m.Buf.LineLen(line), (want+1)*4+8)
	cells := lineCellsTo(m.Buf.Slice(start, start+maxB), want+1)
	col := m.Buf.LineLen(line)
	for _, c := range cells {
		if c.x >= want {
			col = c.col
			break
		}
	}
	return m.Buf.Offset(line, col)
}

// ---- helpers ----

func joinBytes(parts [][]byte, sep byte) []byte {
	out := parts[0]
	for _, p := range parts[1:] {
		out = append(out, sep)
		out = append(out, p...)
	}
	return out
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

// sanitize normalizes \r\n and \r to \n and drops other control characters
// (keeping \t).
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
