package editor

// Code folding. A fold is [header, end] in line numbers, inclusive: the
// header stays visible (with a ▸ gutter marker), lines header+1..end hide.
// Folds are kept sorted and DISJOINT — folding a range swallows any fold
// inside it, and unfolding reveals the whole body.
// ponytail: no nested-fold memory (VSCode keeps inner folds across an outer
// unfold); add a tree if anyone misses it.
//
// Every helper early-outs or is O(#folds), so the perf gates (which never
// fold) pay one len check per call.

// Folder is the optional fold-range provider a Syntax implementation may
// offer; plain-text buffers just don't fold.
type Folder interface {
	// Folds returns fold regions (startLine, endLine) for the outermost
	// multi-line named nodes starting within [startOff, endOff), sorted.
	Folds(src []byte, startOff, endOff int) [][2]int
}

func (m *Model) folder() (Folder, bool) {
	f, ok := m.Syntax.(Folder)
	return f, ok
}

// foldMarks caches the viewport's foldable regions (header line → range) so
// the gutter chevrons don't cost a tree walk on every frame. Held by
// pointer: View runs on a copy of the Model, and a value field would lose
// every cache write.
type foldMarks struct {
	top, height, lineCount, gen int
	marks                       map[int][2]int
}

// foldMarkers returns the fold regions whose header is one of the visible
// lines, keyed by header line. Recomputed only when the viewport, the
// buffer's line count, or the fold set changes — same-line typing hits the
// cache.
func (m *Model) foldMarkers(vis []int) map[int][2]int {
	fr, ok := m.folder()
	if !ok || len(vis) == 0 || m.marks == nil {
		return nil
	}
	lc := m.Buf.LineCount()
	c := m.marks
	if c.marks != nil && c.top == vis[0] && c.height == len(vis) && c.lineCount == lc && c.gen == m.foldsGen {
		return c.marks
	}
	start := m.Buf.Offset(vis[0], 0)
	end := m.Buf.Len()
	if last := vis[len(vis)-1]; last+1 < lc {
		end = m.Buf.Offset(last+1, 0)
	}
	mk := map[int][2]int{}
	for _, f := range fr.Folds(m.Buf.Bytes(), start, end) {
		if _, dup := mk[f[0]]; !dup { // Folds is parents-first: outermost wins
			mk[f[0]] = f
		}
	}
	*c = foldMarks{top: vis[0], height: len(vis), lineCount: lc, gen: m.foldsGen, marks: mk}
	return mk
}

// foldHiding returns the index of the fold hiding line, -1 if visible.
func (m *Model) foldHiding(line int) int {
	for i, f := range m.folds {
		if f[0] < line && line <= f[1] {
			return i
		}
	}
	return -1
}

// FoldedAt returns whether line is a fold header (used for the gutter mark).
func (m *Model) FoldedAt(line int) bool { return m.foldedAt(line) >= 0 }

func (m *Model) foldedAt(line int) int {
	for i, f := range m.folds {
		if f[0] == line {
			return i
		}
	}
	return -1
}

// seekVisible returns the nearest visible line starting at l moving in dir,
// or -1 when it runs off the buffer.
func (m *Model) seekVisible(l, dir int) int {
	for {
		if l < 0 || l >= m.Buf.LineCount() {
			return -1
		}
		i := m.foldHiding(l)
		if i < 0 {
			return l
		}
		if dir > 0 {
			l = m.folds[i][1] + 1
		} else {
			l = m.folds[i][0] // the header is visible
		}
	}
}

// stepVisible moves delta visible lines from line (negative = up), stopping
// at the buffer edges.
func (m *Model) stepVisible(line, delta int) int {
	dir := 1
	if delta < 0 {
		dir, delta = -1, -delta
	}
	if v := m.seekVisible(line, -dir); v >= 0 { // never start from a hidden line
		line = v
	}
	for ; delta > 0; delta-- {
		n := m.seekVisible(line+dir, dir)
		if n < 0 {
			break
		}
		line = n
	}
	return line
}

// visibleRows counts visible lines in [a, b] inclusive. Folds are disjoint,
// so each subtracts its own hidden span exactly once.
func (m *Model) visibleRows(a, b int) int {
	if b < a {
		return 0
	}
	n := b - a + 1
	for _, f := range m.folds {
		lo, hi := max(f[0]+1, a), min(f[1], b)
		if hi >= lo {
			n -= hi - lo + 1
		}
	}
	return n
}

// unfoldAt removes the fold hiding line, if any (cursor guard: whatever
// lands the cursor in a hidden region unfolds it).
func (m *Model) unfoldAt(line int) {
	if i := m.foldHiding(line); i >= 0 {
		m.removeFold(i)
	}
}

func (m *Model) removeFold(i int) {
	m.folds = append(m.folds[:i], m.folds[i+1:]...)
	m.foldsGen++
}

// fixTop snaps the viewport top to a visible line (the hiding fold's header).
func (m *Model) fixTop() {
	if i := m.foldHiding(m.top); i >= 0 {
		m.top = m.folds[i][0]
	}
}

// addFold inserts f sorted, swallowing folds contained in it.
func (m *Model) addFold(f [2]int) {
	out := m.folds[:0]
	for _, g := range m.folds {
		if g[0] >= f[0] && g[1] <= f[1] {
			continue
		}
		out = append(out, g)
	}
	m.folds = out
	at := len(m.folds)
	for i, g := range m.folds {
		if g[0] > f[0] {
			at = i
			break
		}
	}
	m.folds = append(m.folds[:at], append([][2]int{f}, m.folds[at:]...)...)
	m.foldsGen++
}

// ToggleFold folds the region anchored at the primary cursor's line — the
// outermost node starting there, else the enclosing multi-line node — or
// unfolds if the line is already a fold header. The cursor moves to the
// header so it can never sit in a hidden line.
func (m *Model) ToggleFold() {
	line, _ := m.Buf.Pos(m.cursors[m.primary].Head)
	if i := m.foldedAt(line); i >= 0 {
		m.removeFold(i)
		return
	}
	f, ok := m.foldRangeAt(line)
	if !ok {
		return
	}
	m.Go(f[0], 0)
	m.addFold(f)
	m.fixTop()
	m.scrollToCursor()
}

// foldStartingAt returns the fold region whose header is exactly line.
func (m *Model) foldStartingAt(line int) ([2]int, bool) {
	fr, ok := m.folder()
	if !ok {
		return [2]int{}, false
	}
	start := m.Buf.Offset(line, 0)
	for _, f := range fr.Folds(m.Buf.Bytes(), start, start+m.Buf.LineLen(line)) {
		if f[0] == line {
			return f, true
		}
	}
	return [2]int{}, false
}

// foldRangeAt computes the fold anchored at line: a region starting on the
// line wins; otherwise climb to the enclosing multi-line syntax node.
func (m *Model) foldRangeAt(line int) ([2]int, bool) {
	if m.Syntax == nil {
		return [2]int{}, false
	}
	if f, ok := m.foldStartingAt(line); ok {
		return f, true
	}
	src := m.Buf.Bytes()
	// Enclosing node: expand from the cursor until the range spans lines.
	lo := m.cursors[m.primary].Head
	hi := lo
	for {
		nlo, nhi, ok := m.Syntax.Expand(src, lo, hi)
		if !ok {
			return [2]int{}, false
		}
		sl, _ := m.Buf.Pos(nlo)
		el, ec := m.Buf.Pos(nhi)
		if el > sl && ec == 0 {
			el-- // node ends exactly on a newline: last content line folds
		}
		if el > sl {
			if sl == 0 && el >= m.Buf.LineCount()-1 {
				return [2]int{}, false // never fold the whole buffer
			}
			return [2]int{sl, el}, true
		}
		lo, hi = nlo, nhi
	}
}

// FoldAll folds every outermost region in the file.
func (m *Model) FoldAll() {
	fr, ok := m.folder()
	if !ok {
		return
	}
	all := fr.Folds(m.Buf.Bytes(), 0, m.Buf.Len())
	m.folds = m.folds[:0]
	m.foldsGen++
	lastEnd := -1
	for _, f := range all {
		if f[0] > lastEnd { // outermost only: input is sorted, parents first
			m.folds = append(m.folds, f)
			lastEnd = f[1]
		}
	}
	// The cursor may now sit in a hidden body: move it to the fold header.
	line, _ := m.Buf.Pos(m.cursors[m.primary].Head)
	if i := m.foldHiding(line); i >= 0 {
		m.Go(m.folds[i][0], 0)
	}
	m.fixTop()
	m.scrollToCursor()
}

// UnfoldAll clears every fold.
func (m *Model) UnfoldAll() {
	m.folds = nil
	m.foldsGen++
}

// adjustFolds remaps folds through one edit: [start, oldEnd] became
// [start, newEnd] (line numbers). Edits inside a fold stretch it; edits
// crossing a fold boundary drop it.
func (m *Model) adjustFolds(start, oldEnd, newEnd int) {
	delta := newEnd - oldEnd
	m.foldsGen++
	out := m.folds[:0]
	for _, f := range m.folds {
		switch {
		case oldEnd < f[0]: // fully above the header
			out = append(out, [2]int{f[0] + delta, f[1] + delta})
		case start > f[1]: // fully below
			out = append(out, f)
		case start >= f[0] && oldEnd <= f[1]: // within the folded range
			if f[1]+delta > f[0] {
				out = append(out, [2]int{f[0], f[1] + delta})
			}
		}
		// crossing a boundary: dropped
	}
	m.folds = out
}
