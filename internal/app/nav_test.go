package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/lsp"
)

func TestJumpListBackForward(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	os.WriteFile(a, []byte("package a\nvar A = 1\n"), 0o644)
	os.WriteFile(b, []byte("package b\nvar B = 2\n"), 0o644)

	var tm tea.Model = New(dir, nil)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m := tm.(Model)
	m.openFile(a)
	m.docs[0].ed.Go(1, 4)

	// a go-to-definition style jump into b records where we left a
	m.jumpTo(lsp.Location{URI: lsp.PathToURI(b), Range: lsp.Range{Start: lsp.Position{Line: 1}}})
	if !strings.HasSuffix(m.doc().path, "b.go") {
		t.Fatalf("jump landed on %s", m.doc().path)
	}

	m.navBack()
	if !strings.HasSuffix(m.doc().path, "a.go") {
		t.Fatalf("back landed on %s", m.doc().path)
	}
	if line, col := m.doc().ed.Cursor(); line != 1 || col != 4 {
		t.Fatalf("back cursor = %d:%d, want 1:4", line, col)
	}

	m.navForward()
	if !strings.HasSuffix(m.doc().path, "b.go") {
		t.Fatalf("forward landed on %s", m.doc().path)
	}

	// back at the oldest entry: another back is a no-op, not a crash
	m.navBack()
	m.navBack()
	if !strings.HasSuffix(m.doc().path, "a.go") {
		t.Fatalf("double back landed on %s", m.doc().path)
	}
}
