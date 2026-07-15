package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Files created outside Cove (another instance, a shell) must appear in the
// tree when the terminal regains focus — that's the external sync point.
func TestFocusRefreshesTree(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "old.txt"), []byte("x\n"), 0o644)

	m := New(filepath.Join(dir, "old.txt"), []byte("x\n"))
	m.side.Width, m.side.Height = 30, 20

	os.WriteFile(filepath.Join(dir, "outside.txt"), []byte("y\n"), 0o644)
	if strings.Contains(m.side.View(false), "outside.txt") {
		t.Fatal("tree saw the file before any refresh — test is vacuous")
	}

	next, _ := m.update(tea.FocusMsg{})
	if !strings.Contains(next.side.View(false), "outside.txt") {
		t.Fatal("focus did not refresh the tree: outside.txt missing")
	}
}
