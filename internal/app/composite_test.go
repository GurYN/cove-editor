package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/GurYN/cove-editor/internal/editor"
)

// The palette must float: base content stays visible left and right of
// the box, every composed line keeps the full terminal width, and one
// item occupies exactly one line.
func TestOverlayFloatsOverBase(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	m := setup(t) // sidebar closed, width 100
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	frame := m.View()

	lines := strings.Split(frame, "\n")
	boxRows := 0
	for _, l := range lines {
		if w := lipgloss.Width(l); w > 100 {
			t.Fatalf("composed line width %d exceeds terminal: %q", w, ansi.Strip(l))
		}
		plain := ansi.Strip(l)
		if strings.ContainsAny(plain, "╭│╰") {
			boxRows++
		}
	}
	if boxRows < 3 {
		t.Fatalf("no bordered box found (%d rows)", boxRows)
	}
	// One item per line: a label and its keybinding share a row.
	found := false
	for _, l := range lines {
		plain := ansi.Strip(l)
		if strings.Contains(plain, "File: Save") && !strings.Contains(plain, "Save All") {
			found = true
			if !strings.Contains(plain, "ctrl+s") {
				t.Fatalf("label and key not on the same row: %q", plain)
			}
		}
	}
	if !found {
		t.Fatal("palette items missing")
	}
	// Base content must survive beside the centered box: some border row
	// still shows editor text left of the box.
	survived := false
	for _, l := range lines {
		plain := ansi.Strip(l)
		if i := strings.IndexRune(plain, '│'); i > 0 {
			if strings.TrimSpace(plain[:i]) != "" {
				survived = true
			}
		}
	}
	if !survived {
		t.Fatal("base content fully blanked beside the overlay")
	}
}

// A diagnostic under the cursor shows as a severity-colored toast card
// bottom-right, not as plain text in the status bar.
func TestDiagnosticToast(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	m := setup(t).(Model)
	d := m.doc()
	d.ed.Diags = []editor.DiagSpan{{Start: 0, End: 5, Severity: 1, Message: "declared and not used: frobnicate"}}
	// Cursor at offset 0 -> inside the diagnostic.
	frame := m.View()
	lines := strings.Split(frame, "\n")
	status := lines[len(lines)-1]
	if strings.Contains(ansi.Strip(status), "frobnicate") {
		t.Fatal("diagnostic message still in the status bar")
	}
	foundMsg, foundBadge := false, false
	for _, l := range lines {
		plain := ansi.Strip(l)
		if strings.Contains(plain, "frobnicate") {
			foundMsg = true
			if !strings.Contains(plain, "│") {
				t.Fatalf("message outside a bordered card: %q", plain)
			}
		}
		if strings.Contains(plain, "● error") {
			foundBadge = true
		}
	}
	if !foundMsg || !foundBadge {
		t.Fatalf("toast missing (msg=%v badge=%v)", foundMsg, foundBadge)
	}
	// Cursor off the diagnostic -> toast gone.
	d.ed.Go(3, 0)
	if strings.Contains(ansi.Strip(m.View()), "frobnicate") {
		t.Fatal("toast still visible after cursor left the diagnostic")
	}
}

// While the palette is open, the footer shows the highlighted action's ID
// so users can rebind it in config.toml.
func TestPaletteShowsActionIDInFooter(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	for _, r := range "save" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	lines := strings.Split(m.View(), "\n")
	footer := ansi.Strip(lines[len(lines)-1])
	if !strings.Contains(footer, "file.save") {
		t.Fatalf("footer missing action id: %q", footer)
	}
	if w := lipgloss.Width(strings.TrimRight(footer, " ")); w < 10 {
		t.Fatalf("footer bar collapsed: %q", footer)
	}
	// Down arrow moves the highlight -> the id follows.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	lines = strings.Split(m.View(), "\n")
	footer = ansi.Strip(lines[len(lines)-1])
	if strings.Contains(footer, "file.save") {
		t.Fatalf("footer still shows id after palette closed: %q", footer)
	}
}

// The toast sits flush against the right edge, directly above the footer.
func TestToastFlushRight(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	m := setup(t).(Model)
	m.doc().ed.Diags = []editor.DiagSpan{{Start: 0, End: 5, Severity: 2, Message: "short"}}
	lines := strings.SplitSeq(m.View(), "\n")
	for l := range lines {
		plain := ansi.Strip(l)
		if i := strings.LastIndexAny(plain, "╮╯"); i >= 0 {
			if w := lipgloss.Width(plain); w != 100 {
				t.Fatalf("toast border ends at col %d, want flush at 100: %q", w, plain)
			}
			return
		}
	}
	t.Fatal("no toast border found")
}

// Each severity gets its own footer count: errors, warnings, AND infos.
func TestFooterCountsAllSeverities(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	m := setup(t).(Model)
	m.doc().ed.Diags = []editor.DiagSpan{
		{Start: 10, End: 12, Severity: 1, Message: "e"},
		{Start: 20, End: 22, Severity: 2, Message: "w1"},
		{Start: 30, End: 32, Severity: 3, Message: "i1"},
		{Start: 40, End: 42, Severity: 4, Message: "hint"},
	}
	lines := strings.Split(m.View(), "\n")
	footer := ansi.Strip(lines[len(lines)-1])
	for _, want := range []string{"1●", "1▲", "2○"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q: %q", want, footer)
		}
	}
}

// F8 opens the Problems list; Enter jumps to the diagnostic's line.
func TestProblemsListNavigates(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	m := setup(t).(Model)
	m.doc().ed.Diags = []editor.DiagSpan{
		{Start: m.doc().ed.Buf.Offset(2, 0), End: m.doc().ed.Buf.Offset(2, 3), Severity: 2, Message: "warn here"},
		{Start: m.doc().ed.Buf.Offset(5, 1), End: m.doc().ed.Buf.Offset(5, 4), Severity: 1, Message: "boom"},
	}
	wantLine, wantCol := m.doc().ed.Buf.Pos(m.doc().ed.Diags[1].Start)
	m2, _ := m.update(tea.KeyMsg{Type: tea.KeyF8})
	frame := m2.View()
	for _, want := range []string{"Problems:", "● boom", "▲ warn here", ":6", ":3"} {
		if !strings.Contains(ansi.Strip(frame), want) {
			t.Fatalf("problems list missing %q", want)
		}
	}
	// Errors sort first: Enter on the initial selection jumps to "boom".
	m2, _ = m2.update(tea.KeyMsg{Type: tea.KeyEnter})
	if line, col := m2.doc().ed.Cursor(); line != wantLine || col != wantCol {
		t.Fatalf("jumped to %d:%d, want %d:%d", line, col, wantLine, wantCol)
	}
	if m2.ovKind != overlayNone {
		t.Fatal("problems list still open after jump")
	}
}

// Dragging the sidebar/editor divider resizes both panes; width clamps at 12.
func TestDividerDragResizesSidebar(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	var m tea.Model = New("/tmp/sample.go", []byte(sampleSrc))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	x := m.(Model).side.Width // divider column
	m, _ = m.Update(tea.MouseMsg{X: x, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m, _ = m.Update(tea.MouseMsg{X: 42, Y: 5, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	appm := m.(Model)
	if appm.side.Width != 42 {
		t.Fatalf("sidebar width %d after drag, want 42", appm.side.Width)
	}
	if w := appm.doc().ed.Width; w != 100-43 {
		t.Fatalf("editor width %d, want %d", w, 100-43)
	}
	m, _ = m.Update(tea.MouseMsg{X: 3, Y: 5, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if appm = m.(Model); appm.side.Width != 12 {
		t.Fatalf("min clamp failed: %d", appm.side.Width)
	}
}

// Hovering the divider flips the resize-pointer state on and off.
func TestDividerHoverPointer(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	var m tea.Model = New("/tmp/sample.go", []byte(sampleSrc))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	x := m.(Model).side.Width
	m, _ = m.Update(tea.MouseMsg{X: x, Y: 5, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if m.(Model).hoverShape != "ew-resize" {
		t.Fatal("hover over divider not detected")
	}
	m, _ = m.Update(tea.MouseMsg{X: x + 10, Y: 5, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if m.(Model).hoverShape != "default" {
		t.Fatal("pointer still resize-shaped after leaving the divider")
	}
}
