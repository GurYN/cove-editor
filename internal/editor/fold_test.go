package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/buffer"
)

// fakeFolds implements Syntax + Folder with a fixed fold list.
type fakeFolds struct{ folds [][2]int }

func (fakeFolds) Edit(int, int, int, [2]int, [2]int, [2]int) {}
func (fakeFolds) Spans([]byte, int, int) []HLSpan            { return nil }
func (fakeFolds) Expand([]byte, int, int) (int, int, bool)   { return 0, 0, false }
func (f fakeFolds) Folds([]byte, int, int) [][2]int          { return f.folds }

func foldModel(t *testing.T, lines int, folds [][2]int) Model {
	t.Helper()
	var sb strings.Builder
	for i := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("line")
		sb.WriteByte(byte('0' + i%10))
	}
	m := New(buffer.New([]byte(sb.String())))
	m.Width, m.Height = 40, 6
	m.Syntax = fakeFolds{folds: folds}
	return m
}

func TestFoldHidesBodyAndMovesOverIt(t *testing.T) {
	m := foldModel(t, 10, [][2]int{{1, 4}})
	m.Go(1, 0)
	m.ToggleFold()

	view := m.View()
	if !strings.Contains(view, "line5") || strings.Contains(view, "line2") {
		t.Fatalf("fold [1,4]: body must hide, line5 must show:\n%s", view)
	}
	if !strings.Contains(view, "▸") {
		t.Fatal("fold header missing the gutter marker")
	}

	m.MoveV(+1, false) // down from the header skips the body
	if l, _ := m.Cursor(); l != 5 {
		t.Fatalf("moveV over fold: got line %d, want 5", l)
	}
	if _, y := m.CursorScreen(); y != 2 {
		t.Fatalf("CursorScreen: got y %d, want 2 (rows: 0,1,5)", y)
	}

	m.Go(1, 0)
	m.ToggleFold() // header again: unfold
	if len(m.folds) != 0 {
		t.Fatal("toggle on the header must unfold")
	}
}

func TestFoldMouseChevron(t *testing.T) {
	m := foldModel(t, 10, [][2]int{{1, 4}})

	if !strings.Contains(m.View(), "▾") {
		t.Fatal("foldable line must show a ▾ chevron")
	}

	// Click the chevron cell (between its two pad columns) on row 1: fold.
	chevronX := m.gutterW() - 2
	m.handleMouse(tea.MouseMsg{X: chevronX, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(m.folds) != 1 || m.folds[0] != [2]int{1, 4} {
		t.Fatalf("chevron click must fold: got %v", m.folds)
	}
	view := m.View()
	if strings.Contains(view, "line3") || !strings.Contains(view, "▸") {
		t.Fatalf("after mouse fold: body visible or ▸ missing:\n%s", view)
	}

	// Click again: unfold (row 1 is still the header row).
	m.handleMouse(tea.MouseMsg{X: chevronX, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(m.folds) != 0 {
		t.Fatal("second chevron click must unfold")
	}

	// A gutter click left of the chevron cell keeps its cursor semantics.
	m.handleMouse(tea.MouseMsg{X: 0, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(m.folds) != 0 {
		t.Fatal("non-chevron gutter click must not fold")
	}
	if l, _ := m.Cursor(); l != 2 {
		t.Fatalf("gutter click should still place the cursor: line %d, want 2", l)
	}
}

func TestFoldMouseParksCursorOnHeader(t *testing.T) {
	m := foldModel(t, 10, [][2]int{{1, 4}})
	m.Go(3, 0) // cursor inside the region about to fold
	m.handleMouse(tea.MouseMsg{X: m.gutterW() - 2, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(m.folds) != 1 {
		t.Fatalf("fold expected, got %v", m.folds)
	}
	if l, _ := m.Cursor(); l != 1 {
		t.Fatalf("cursor must move to the header: line %d, want 1", l)
	}
}

func TestFoldAdjustsOnEdits(t *testing.T) {
	m := foldModel(t, 10, nil)
	m.folds = [][2]int{{4, 7}}

	m.Go(0, 0)
	m.InsertNewline() // one line added above: fold shifts down
	if m.folds[0] != [2]int{5, 8} {
		t.Fatalf("edit above: got %v, want [5 8]", m.folds[0])
	}

	m.Go(3, 0) // still above the header: no unfold
	if len(m.folds) != 1 {
		t.Fatal("cursor above the fold must not unfold it")
	}
	m.Go(6, 0) // inside the hidden body: jump unfolds
	if len(m.folds) != 0 {
		t.Fatal("cursor landing in a hidden line must unfold")
	}
}

func TestFoldAllKeepsOutermost(t *testing.T) {
	m := foldModel(t, 10, [][2]int{{1, 8}, {2, 4}}) // nested input, parents first
	m.FoldAll()
	if len(m.folds) != 1 || m.folds[0] != [2]int{1, 8} {
		t.Fatalf("FoldAll: got %v, want [[1 8]]", m.folds)
	}
	m.UnfoldAll()
	if len(m.folds) != 0 {
		t.Fatal("UnfoldAll left folds behind")
	}
}

func TestFoldDropsWhenEditCrossesBoundary(t *testing.T) {
	m := foldModel(t, 10, nil)
	m.folds = [][2]int{{2, 5}}
	// Delete lines 4..7 — crosses the fold's lower boundary.
	lo := m.Buf.Offset(4, 0)
	hi := m.Buf.Offset(7, 0)
	m.ApplyEdits([]Edit{{Off: lo, Old: append([]byte(nil), m.Buf.Slice(lo, hi)...)}})
	if len(m.folds) != 0 {
		t.Fatalf("boundary-crossing edit must drop the fold, got %v", m.folds)
	}
}

func TestFoldTailIndicator(t *testing.T) {
	// Brace fold: closing "}" joins the marker → "…}".
	src := "func a() {\n\tx()\n}\nafter"
	m := New(buffer.New([]byte(src)))
	m.Width, m.Height = 40, 6
	m.Syntax = fakeFolds{folds: [][2]int{{0, 2}}}
	m.ToggleFold()
	if view := m.View(); !strings.Contains(view, "…}") {
		t.Fatalf("folded brace header must show …} :\n%s", view)
	}

	// Delimiter-less fold (Python-style): bare "…", no code from the end line.
	src = "def a():\n\tx()\n\ty()\nafter"
	m = New(buffer.New([]byte(src)))
	m.Width, m.Height = 40, 6
	m.Syntax = fakeFolds{folds: [][2]int{{0, 2}}}
	m.ToggleFold()
	view := m.View()
	if !strings.Contains(view, "…") || strings.Contains(view, "…y") {
		t.Fatalf("folded python header must show bare … :\n%s", view)
	}
}
