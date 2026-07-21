package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/lsp"
)

// A code action may create a file (gopls add test / extract to new file):
// the workspace edit targets a URI that doesn't exist yet. It must be
// created, filled, and revealed in a tab — not rejected with a read error.
func TestWorkspaceEditCreatesFile(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	var tm tea.Model = New(dir, nil)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m := tm.(Model)

	newFile := filepath.Join(dir, "a_test.go")
	we := &lsp.WorkspaceEdit{Changes: map[string][]lsp.TextEdit{
		lsp.PathToURI(newFile): {{NewText: "package a\n\nfunc TestA(t *T) {}\n"}},
	}}
	files, stale := m.applyWorkspaceEdit(we, map[string]int{})
	if files != 1 || stale != 0 {
		t.Fatalf("files=%d stale=%d, want 1/0", files, stale)
	}
	data, err := os.ReadFile(newFile)
	if err != nil || !strings.Contains(string(data), "TestA") {
		t.Fatalf("created file: %q err=%v", data, err)
	}
	if d := m.doc(); d == nil || !strings.HasSuffix(d.path, "a_test.go") {
		t.Fatal("created file not revealed in a tab")
	}
}
