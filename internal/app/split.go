package app

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Split panes: one vertical split over the editor area. The two panes share
// the tab list; m.active is always the focused pane's doc, m.other the
// unfocused pane's. Splitting mirrors the active doc until another tab is
// picked. ponytail: exactly two panes, vertical only; N-way needs a tree.

// splitAvail is the editor area width (right of the sidebar and its border).
func (m *Model) splitAvail() int { return m.width - m.editorX() }

// splitLW is the left pane's content width; 0 stored width means half.
func (m *Model) splitLW() int {
	avail := m.splitAvail()
	lw := m.splitW
	if lw == 0 {
		lw = (avail - 1) / 2
	}
	return clampInt(lw, 10, max(10, avail-11))
}

// splitX is the divider's absolute column.
func (m *Model) splitX() int { return m.editorX() + m.splitLW() }

// paneDocIdx returns the doc index shown in the left (false) or right pane.
func (m *Model) paneDocIdx(right bool) int {
	if right == m.splitRight {
		return m.active
	}
	return m.other
}

// paneXAbs is the focused pane's left edge — the x origin for editor mouse
// events and cursor-anchored overlays (completion, hover).
func (m *Model) paneXAbs() int {
	if m.split && m.splitRight {
		return m.splitX() + 1
	}
	return m.editorX()
}

// flashMsg clears the focus flash; the int is the generation that set it.
type flashMsg int

// cycleFocus moves focus to the next (+1) or previous (-1) visible panel,
// in visual order: sidebar slot, left editor, right editor, terminal. The
// returned cmd clears the accent frame flashed around the new panel.
func (m *Model) cycleFocus(dir int) tea.Cmd {
	type stop struct {
		p     pane
		right bool
	}
	var stops []stop
	if m.sidebarOpen {
		if m.git.view {
			stops = append(stops, stop{p: paneGit})
		} else {
			stops = append(stops, stop{p: paneSidebar})
		}
	}
	stops = append(stops, stop{p: paneEditor})
	if m.split {
		stops = append(stops, stop{p: paneEditor, right: true})
	}
	if m.termOpen && m.activeTerm() != nil {
		stops = append(stops, stop{p: paneTerminal})
	}
	cur := 0
	for i, s := range stops {
		if s.p == m.focus && (s.p != paneEditor || s.right == m.splitRight) {
			cur = i
		}
	}
	next := stops[(cur+len(stops)+dir)%len(stops)]
	if next.p == paneEditor {
		m.focusPane(next.right)
	} else {
		m.focus = next.p
	}
	m.flashOn = true
	m.flashGen++
	gen := m.flashGen
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return flashMsg(gen) })
}

// flashRect is the focused panel's rectangle in middle-block coordinates
// (row 0 = first row under the tab bar).
func (m Model) flashRect() (x, y, w, h int) {
	switch m.focus {
	case paneSidebar, paneGit:
		return 0, 0, m.side.Width, m.height - 2
	case paneTerminal:
		return m.editorX(), m.contentRows(), m.width - m.editorX(), m.panelRows()
	}
	x, w = m.editorX(), m.width-m.editorX()
	if m.split {
		if m.splitRight {
			x, w = m.splitX()+1, m.splitAvail()-m.splitLW()-1
		} else {
			w = m.splitLW()
		}
	}
	return x, 0, w, m.contentRows()
}

// flashFrame overlays an accent frame on the focused panel's outer ring —
// four composite splices: top edge, bottom edge, left and right columns.
func (m Model) flashFrame(middle string) string {
	x, y, w, h := m.flashRect()
	if w < 2 || h < 2 {
		return middle
	}
	hor := func(l, r string) string { return flashStyle.Render(l + strings.Repeat("─", w-2) + r) }
	side := strings.TrimSuffix(strings.Repeat(flashStyle.Render("│")+"\n", h-2), "\n")
	middle = m.composite(middle, hor("┌", "┐"), y, x)
	middle = m.composite(middle, hor("└", "┘"), y+h-1, x)
	if h > 2 {
		middle = m.composite(middle, side, y+1, x)
		middle = m.composite(middle, side, y+1, x+w-1)
	}
	return middle
}

// focusPane focuses the left or right pane, swapping active/other so the
// m.active invariant holds.
func (m *Model) focusPane(right bool) {
	if m.split && right != m.splitRight {
		m.active, m.other = m.other, m.active
		m.splitRight = right
	}
	m.focus = paneEditor
}

// splitOpen turns the split on, mirroring the active doc in the new pane.
func (m *Model) splitOpen() {
	if m.split || m.doc() == nil {
		return
	}
	m.split = true
	m.splitRight = false
	m.other = m.active
	m.splitW = 0
	m.layout()
}

// splitView renders both panes side by side with a border column between.
func (m Model) splitView() string {
	lw := m.splitLW()
	rw := m.splitAvail() - lw - 1
	rows := max(1, m.contentRows())
	border := strings.TrimSuffix(strings.Repeat(borderStyle.Render("│")+"\n", rows), "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.paneView(m.paneDocIdx(false), lw), border, m.paneView(m.paneDocIdx(true), rw))
}

// paneView renders doc i at width w on a copy, so a mirrored split can show
// the same doc at two widths. Lines are padded to w to keep the divider
// column straight (JoinHorizontal only pads to the block's own max width).
func (m Model) paneView(i, w int) string {
	e := m.docs[i].ed
	e.Width = w
	lines := strings.Split(e.View(), "\n")
	for j, l := range lines {
		if pad := w - lipgloss.Width(l); pad > 0 {
			lines[j] = l + strings.Repeat(" ", pad)
		}
	}
	return strings.Join(lines, "\n")
}
