package app

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Copy and cut must stay undo-transparent: the OSC 52 side effect happens at
// the action layer, and undo after paste/cut restores the buffer.
func TestUndoAfterCopyPasteCut(t *testing.T) {
	var m tea.Model = New("/tmp/sample.go", []byte("hello world\n"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	am := m.(Model)
	am.doc().ed.SelectAll()
	m = am
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}) // copy all
	am = m.(Model)
	am.doc().ed.Go(0, 0)
	m = am
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlV}) // paste -> duplicated
	am = m.(Model)
	if got := am.doc().ed.Buf.Bytes(); !bytes.Equal(got, []byte("hello world\nhello world\n")) {
		t.Fatalf("paste wrong: %q", got)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlZ}) // undo paste
	am = m.(Model)
	if got := am.doc().ed.Buf.Bytes(); !bytes.Equal(got, []byte("hello world\n")) {
		t.Fatalf("undo after paste wrong: %q", got)
	}
	am.doc().ed.SelectAll()
	m = am
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX}) // cut
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlZ}) // undo cut
	am = m.(Model)
	if got := am.doc().ed.Buf.Bytes(); !bytes.Equal(got, []byte("hello world\n")) {
		t.Fatalf("undo after cut wrong: %q", got)
	}
}

// Ctrl+V in the find bar (and every other single-line input) types the
// shared clipboard, newlines flattened.
func TestPasteIntoFindBar(t *testing.T) {
	var m tea.Model = New("/tmp/sample.go", []byte("hello world\nhello moon\n"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	am := m.(Model)
	am.doc().ed.Go(0, 0)
	for range "hello" {
		am.doc().ed.MoveH(+1, true) // select "hello"
	}
	m = am
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}) // copy selection
	am = m.(Model)
	am.doc().ed.Go(1, 0) // collapse selection so find opens empty
	m = am
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF}) // open find
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlV}) // paste into find
	am = m.(Model)
	if am.query != "hello" {
		t.Fatalf("find query = %q, want %q", am.query, "hello")
	}
}
