package editor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/buffer"
)

func newEd(text string) Model {
	m := New(buffer.New([]byte(text)))
	m.Width, m.Height = 80, 24
	return m
}

// selectLines makes a selection from (line a, 0) to the end of line b.
func selectLines(m *Model, a, b int) {
	m.Go(a, 0)
	for i := a; i < b; i++ {
		m.MoveV(1, true)
	}
	m.LineEdge(+1, true)
}

func TestIndentOutdentBlock(t *testing.T) {
	m := newEd("aa\nbb\ncc\n")
	selectLines(&m, 0, 1)
	m.IndentLines(+1)
	if got := string(m.Buf.Bytes()); got != "\taa\n\tbb\ncc\n" {
		t.Fatalf("indent: %q", got)
	}
	// Selection survives: a second Tab must indent again, not replace.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := string(m.Buf.Bytes()); got != "\t\taa\n\t\tbb\ncc\n" {
		t.Fatalf("indent via Tab: %q", got)
	}
	m.IndentLines(-1)
	m.IndentLines(-1)
	m.IndentLines(-1) // extra outdent is a no-op at column 0
	if got := string(m.Buf.Bytes()); got != "aa\nbb\ncc\n" {
		t.Fatalf("outdent: %q", got)
	}
	m.UndoStep()
	m.UndoStep()
	m.UndoStep()
	m.UndoStep()
	if got := string(m.Buf.Bytes()); got != "aa\nbb\ncc\n" {
		t.Fatalf("undo: %q", got)
	}
}

func TestOutdentSpaces(t *testing.T) {
	m := newEd("      x\n")
	m.Go(0, 6)
	m.IndentLines(-1)
	if got := string(m.Buf.Bytes()); got != "  x\n" {
		t.Fatalf("outdent 4 spaces: %q", got)
	}
}

func TestToggleComment(t *testing.T) {
	m := newEd("a\n\n\tb\n")
	selectLines(&m, 0, 2)
	m.ToggleComment("//")
	if got := string(m.Buf.Bytes()); got != "// a\n\n\t// b\n" {
		t.Fatalf("comment: %q", got)
	}
	m.ToggleComment("//")
	if got := string(m.Buf.Bytes()); got != "a\n\n\tb\n" {
		t.Fatalf("uncomment: %q", got)
	}
	m.ToggleComment("") // no line-comment language: no-op
	if got := string(m.Buf.Bytes()); got != "a\n\n\tb\n" {
		t.Fatalf("empty prefix: %q", got)
	}
}

func TestDuplicateAndDeleteLine(t *testing.T) {
	m := newEd("aa\nbb\ncc")
	m.Go(1, 1)
	m.DuplicateLine()
	if got := string(m.Buf.Bytes()); got != "aa\nbb\nbb\ncc" {
		t.Fatalf("duplicate: %q", got)
	}
	m.DeleteLine()
	if got := string(m.Buf.Bytes()); got != "aa\nbb\ncc" {
		t.Fatalf("delete: %q", got)
	}
	m.Go(2, 0)
	m.DeleteLine() // last line eats the preceding newline
	if got := string(m.Buf.Bytes()); got != "aa\nbb" {
		t.Fatalf("delete last: %q", got)
	}
	// Deleting the two last lines together must not produce overlapping edits.
	selectLines(&m, 0, 1)
	m.DeleteLine()
	if got := string(m.Buf.Bytes()); got != "" {
		t.Fatalf("delete all: %q", got)
	}
}

func TestMoveLine(t *testing.T) {
	m := newEd("aa\nbb\ncc\n")
	m.Go(1, 1)
	m.MoveLine(-1)
	if got := string(m.Buf.Bytes()); got != "bb\naa\ncc\n" {
		t.Fatalf("move up: %q", got)
	}
	if l, c := m.Cursor(); l != 0 || c != 1 {
		t.Fatalf("cursor followed to %d:%d", l, c)
	}
	m.MoveLine(-1) // at the top: no-op
	m.MoveLine(+1)
	if got := string(m.Buf.Bytes()); got != "aa\nbb\ncc\n" {
		t.Fatalf("move down: %q", got)
	}
	if l, _ := m.Cursor(); l != 1 {
		t.Fatalf("cursor line %d", l)
	}
}

func TestSelectAllOccurrences(t *testing.T) {
	m := newEd("foo bar foo baz foo\n")
	m.Go(0, 1) // inside the first "foo"
	m.SelectAllOccurrences()
	if m.CursorCount() != 3 {
		t.Fatalf("cursors = %d, want 3", m.CursorCount())
	}
	m.InsertText("qux")
	if got := string(m.Buf.Bytes()); got != "qux bar qux baz qux\n" {
		t.Fatalf("multi-edit: %q", got)
	}
}
