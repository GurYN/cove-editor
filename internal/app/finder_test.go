package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A root-anchored gitignore pattern ("/cove" for the built binary) must not
// hide same-named entries deeper in the tree (cmd/cove/main.go).
func TestListFilesAnchoredGitignore(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "/cove\ndist/\n*.prof\n")
	write("cove", "binary")
	write("cmd/cove/main.go", "package main")
	write("dist/out.txt", "x")
	write("cpu.prof", "x")

	files := listFiles(root)
	if !slices.Contains(files, filepath.Join("cmd", "cove", "main.go")) {
		t.Fatalf("anchored /cove hid cmd/cove/main.go: %v", files)
	}
	for _, gone := range []string{"cove", filepath.Join("dist", "out.txt"), "cpu.prof"} {
		if slices.Contains(files, gone) {
			t.Fatalf("%s should be gitignored: %v", gone, files)
		}
	}
}
