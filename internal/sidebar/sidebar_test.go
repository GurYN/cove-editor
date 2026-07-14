package sidebar

import (
	"path/filepath"
	"testing"
)

func TestSetGitStatusMarksAncestorDirs(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	deep := filepath.Join(root, "a", "b", "c.go")
	m.SetGitStatus(map[string]byte{
		deep: 'M',
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
