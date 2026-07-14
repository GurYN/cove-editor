package app

import (
	"strings"

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
