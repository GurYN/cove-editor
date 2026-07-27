package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GurYN/cove-editor/internal/editor"
)

// alt+n / alt+p cycle through diagnostics in buffer order, wrapping.
func TestCycleDiag(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	content := []byte("aaa\nbbb\nccc\nddd\n")
	os.WriteFile(path, content, 0o644)

	m := New(path, content)
	d := m.doc()
	off := func(line int) int { return d.ed.Buf.Offset(line, 0) }
	// Deliberately unsorted: cycling must sort by position.
	d.ed.Diags = []editor.DiagSpan{
		{Start: off(2), End: off(2) + 1, Severity: 1},
		{Start: off(0), End: off(0) + 1, Severity: 2},
	}

	line := func() int { l, _ := d.ed.Cursor(); return l }
	m.cycleDiag(+1) // from 0:0, the diag at line 0 starts AT the cursor → next is line 2
	if line() != 2 {
		t.Fatalf("next: got line %d, want 2", line())
	}
	m.cycleDiag(+1) // wraps
	if line() != 0 {
		t.Fatalf("next wrap: got line %d, want 0", line())
	}
	m.cycleDiag(-1) // wraps backward
	if line() != 2 {
		t.Fatalf("prev wrap: got line %d, want 2", line())
	}
}
