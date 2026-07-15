package sidebar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetGitStatusMarksAncestorDirs(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	deep := filepath.Join(root, "a", "b", "c.go")
	m.SetGitStatus(map[string]byte{
		deep:                              'M',
		filepath.Join("/outside", "x.go"): 'A', // outside root: must not loop or mark
	})
	for _, d := range []string{filepath.Join(root, "a", "b"), filepath.Join(root, "a"), root} {
		if !m.gitDirs[d] {
			t.Errorf("ancestor %s not marked", d)
		}
	}
	if m.gitDirs["/outside"] || m.gitDirs["/"] {
		t.Error("marked dirs outside root")
	}
	if m.gitFiles[deep] != 'M' {
		t.Error("file marker lost")
	}
}

// Refresh must keep deep expansion: a/b expanded, refresh, still expanded.
func TestRefreshKeepsDeepExpansion(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(root, "a", "b", "f.txt"), []byte("x"), 0o644)

	m := New(root)
	m.Width, m.Height = 30, 20
	for range 2 { // expand a, then a/b (dirs sort first, selection follows)
		m.Toggle()
		m.Move(1)
	}
	if !strings.Contains(m.View(false), "f.txt") {
		t.Fatal("setup: f.txt not visible after expanding a/b")
	}

	os.WriteFile(filepath.Join(root, "new.txt"), []byte("y"), 0o644)
	m.Refresh()
	v := m.View(false)
	if !strings.Contains(v, "f.txt") {
		t.Fatalf("deep expansion lost after Refresh:\n%s", v)
	}
	if !strings.Contains(v, "new.txt") {
		t.Fatalf("Refresh missed the new file:\n%s", v)
	}
}
