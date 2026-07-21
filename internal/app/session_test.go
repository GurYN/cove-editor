package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSessionRoundTrip(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nvar A = 1\nvar B = 2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone.go"), []byte("package gone\n"), 0o644)

	var tm tea.Model = New(dir, nil)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m := tm.(Model)
	m.openFile(filepath.Join(dir, "a.go"))
	m.openFile(filepath.Join(dir, "b.go"))
	m.openFile(filepath.Join(dir, "gone.go"))
	m.active = 0
	m.docs[0].ed.Go(2, 4)
	m.saveSession()

	os.Remove(filepath.Join(dir, "gone.go")) // deleted between sessions

	m2 := New(dir, nil)
	if len(m2.docs) != 2 {
		t.Fatalf("restored %d docs, want 2 (deleted file skipped)", len(m2.docs))
	}
	if !strings.HasSuffix(m2.docs[m2.active].path, "a.go") {
		t.Fatalf("active tab not restored: %s", m2.docs[m2.active].path)
	}
	if line, col := m2.docs[0].ed.Cursor(); line != 2 || col != 4 {
		t.Fatalf("cursor = %d:%d, want 2:4", line, col)
	}
	if m2.focus != paneEditor {
		t.Fatal("focus should land in the editor after restore")
	}
}

func TestSessionSkipsExplicitFileOpen(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	os.WriteFile(a, []byte("package a\n"), 0o644)

	m := New(dir, nil)
	m.openFile(a)
	m.saveSession()

	// `cove a.go` must open exactly that file, not the whole session.
	m2 := New(a, []byte("package a\n"))
	if len(m2.docs) != 1 {
		t.Fatalf("explicit file open restored a session: %d docs", len(m2.docs))
	}
}
