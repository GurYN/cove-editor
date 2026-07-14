package app

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Ctrl+\ splits the editor: a divider column appears mid-pane and the same
// doc mirrors on both sides; a second file opens into the focused pane.
func TestSplitPane(t *testing.T) {
	m := setup(t) // sidebar closed, width 100, /tmp/sample.go active
	if err := os.WriteFile("/tmp/other.go", []byte("package other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlBackslash})
	appm := m.(Model)
	if !appm.split {
		t.Fatal("ctrl+\\ did not split")
	}
	lines := strings.Split(m.View(), "\n")
	row := []rune(ansi.Strip(lines[1]))
	if row[49] != '│' {
		t.Fatalf("no divider at col 49: %q", string(row))
	}
	if c := strings.Count(string(row), "package sample"); c != 2 {
		t.Fatalf("mirrored split shows the doc %d times, want 2", c)
	}

	// F6 focuses the right pane; opening a file lands there, left keeps sample.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF6})
	appm = m.(Model)
	if !appm.splitRight {
		t.Fatal("f6 did not focus the right pane")
	}
	appm.openFile("/tmp/other.go")
	appm.layout()
	if got := appm.doc().path; !strings.HasSuffix(got, "other.go") {
		t.Fatalf("open landed on %s", got)
	}
	frame := ansi.Strip(strings.Split(appm.View(), "\n")[1])
	l, r := frame[:49], frame[49:]
	if !strings.Contains(l, "package sample") || !strings.Contains(r, "package other") {
		t.Fatalf("panes wrong: left=%q right=%q", l, r)
	}

	// A click in the left pane focuses it back.
	m2, _ := appm.update(tea.MouseMsg{X: 8, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m2.splitRight || !strings.HasSuffix(m2.doc().path, "sample.go") {
		t.Fatalf("left-pane click focused %s (right=%v)", m2.doc().path, m2.splitRight)
	}

	// Closing the right pane's tab keeps the split valid (mirror of what's left).
	m2, _ = m2.update(tea.KeyMsg{Type: tea.KeyF6})
	m2.forceClose()
	if !m2.split || m2.active != 0 || m2.other != 0 {
		t.Fatalf("after close: split=%v active=%d other=%d", m2.split, m2.active, m2.other)
	}
}

// Dragging the split divider resizes the panes, clamped at 10 cells.
func TestSplitDividerDrag(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlBackslash})
	mm := m.(Model)
	x := mm.splitX()
	m, _ = m.Update(tea.MouseMsg{X: x, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m, _ = m.Update(tea.MouseMsg{X: 30, Y: 5, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	appm := m.(Model)
	if appm.splitLW() != 30 {
		t.Fatalf("left width %d after drag, want 30", appm.splitLW())
	}
	if w := appm.doc().ed.Width; w != 30 { // focused (left) doc hit-tests at pane width
		t.Fatalf("focused editor width %d, want 30", w)
	}
	m, _ = m.Update(tea.MouseMsg{X: 2, Y: 5, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	mm = m.(Model)
	if lw := mm.splitLW(); lw != 10 {
		t.Fatalf("min clamp failed: %d", lw)
	}
}
