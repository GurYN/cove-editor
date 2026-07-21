package app

// The jump list: every "big" navigation (go-to-definition, symbol pick,
// problems/search-hit jump, go-to-line) records where it left from, so
// alt+left walks back through the trail and alt+right forward again —
// go-to-definition without go-back is a one-way door.

// jumpLoc is one remembered position.
type jumpLoc struct {
	path      string
	line, col int
}

const maxJumps = 100

// curLoc captures the active doc's position; ok=false for no doc or a
// virtual tab (a git diff view can't be re-opened by path).
func (m *Model) curLoc() (jumpLoc, bool) {
	d := m.doc()
	if d == nil || d.virtual {
		return jumpLoc{}, false
	}
	line, col := d.ed.Cursor()
	return jumpLoc{path: d.path, line: line, col: col}, true
}

// pushJump records the current position before a navigation lands
// somewhere else, truncating any forward trail.
func (m *Model) pushJump() {
	cur, ok := m.curLoc()
	if !ok {
		return
	}
	m.jumps = append(m.jumps[:min(m.jumpIdx, len(m.jumps))], cur)
	if len(m.jumps) > maxJumps {
		m.jumps = m.jumps[len(m.jumps)-maxJumps:]
	}
	m.jumpIdx = len(m.jumps)
}

func (m *Model) navBack() {
	if m.jumpIdx == 0 {
		m.lastMsg = "start of jump list"
		return
	}
	if m.jumpIdx == len(m.jumps) { // leaving the tip: remember it for forward
		if cur, ok := m.curLoc(); ok {
			m.jumps = append(m.jumps, cur)
		}
	}
	m.jumpIdx--
	m.gotoJump(m.jumps[m.jumpIdx])
}

func (m *Model) navForward() {
	if m.jumpIdx >= len(m.jumps)-1 {
		m.lastMsg = "end of jump list"
		return
	}
	m.jumpIdx++
	m.gotoJump(m.jumps[m.jumpIdx])
}

func (m *Model) gotoJump(j jumpLoc) {
	m.openFile(j.path)
	if d := m.doc(); d != nil && same(d.path, j.path) {
		d.ed.Go(j.line, j.col)
		d.ed.Center()
	}
	m.layout()
}
