package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func searchSetup(t *testing.T) (Model, string) {
	t.Helper()
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nvar Needle = 1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("no match here\nneedle in text\n"), 0o644)
	var m tea.Model = New(dir, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return m.(Model), dir
}

func TestProjectSearchSmartCase(t *testing.T) {
	m, _ := searchSetup(t)
	// lowercase query: case-insensitive → hits in both files
	msg := m.projectSearchCmd("needle")().(psearchMsg)
	if len(msg.hits) != 2 {
		t.Fatalf("smart-case search: %d hits, want 2 (%+v)", len(msg.hits), msg.hits)
	}
	// cased query: exact → only a.go
	msg = m.projectSearchCmd("Needle")().(psearchMsg)
	if len(msg.hits) != 1 || !strings.HasSuffix(msg.hits[0].ref.path, "a.go") {
		t.Fatalf("cased search: %+v", msg.hits)
	}
}

func TestProjectSearchSeesDirtyBuffer(t *testing.T) {
	m, dir := searchSetup(t)
	m.openFile(filepath.Join(dir, "a.go"))
	m.doc().ed.InsertText("xyzzy ")
	if m.doc().ed.Dirty != true {
		t.Fatal("buffer should be dirty")
	}
	msg := m.projectSearchCmd("xyzzy")().(psearchMsg)
	if len(msg.hits) != 1 {
		t.Fatalf("unsaved edit invisible to search: %+v", msg.hits)
	}
	// picking a hit jumps there and records a jump-list entry
	mm := m.openProjectSearch(msg)
	if !mm.search.view || mm.focus != paneSearch {
		t.Fatal("search panel not open and focused")
	}
	if len(mm.search.rows) != 2 || mm.search.rows[0].header == "" || mm.search.rows[1].header != "" {
		t.Fatalf("rows not grouped file-header-then-hit: %+v", mm.search.rows)
	}
	if mm.search.sel != 1 {
		t.Fatalf("selection on row %d, want first hit (1)", mm.search.sel)
	}
	var m2 tea.Model = mm
	m2, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := m2.(Model)
	if len(got.jumps) == 0 {
		t.Fatal("hit jump did not record a jump-list entry")
	}
	if got.focus != paneEditor {
		t.Fatal("enter should move focus into the editor")
	}
}

func TestProjectSearchFilters(t *testing.T) {
	m, dir := searchSetup(t)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "c.go"), []byte("needle deep\n"), 0o644)
	m.side.Refresh()

	m.search.inc = "*.go"
	msg := m.projectSearchCmd("needle")().(psearchMsg)
	if len(msg.hits) != 2 { // a.go + sub/c.go, b.txt filtered out
		t.Fatalf("include *.go: %d hits, want 2 (%+v)", len(msg.hits), msg.hits)
	}
	m.search.exc = "sub"
	msg = m.projectSearchCmd("needle")().(psearchMsg)
	if len(msg.hits) != 1 || !strings.HasSuffix(msg.hits[0].ref.path, "a.go") {
		t.Fatalf("exclude sub: %+v", msg.hits)
	}
}

func TestMatchGlob(t *testing.T) {
	for _, tc := range []struct {
		pat, rel string
		want     bool
	}{
		{"*.go", "internal/app/x.go", true},
		{"*.go", "x.txt", false},
		{"vendor", "vendor/lib/x.go", true},
		{"internal/*", "internal/app/x.go", true},
		{"internal/app", "internal/app/x.go", true},
		{"app", "internal/app/x.go", true},
		{"other/*", "internal/app/x.go", false},
		{"**/syntax/*", "internal/syntax/x.c", true},
		{"**/syntax/*", "internal/syntax/deep/x.c", true},
		{"**/*.c", "internal/syntax/x.c", true},
		{"syntax/**", "internal/syntax/deep/x.c", true},
		{"internal/**/x.c", "internal/syntax/deep/x.c", true},
		{"**/*.c", "internal/syntax/x.go", false},
	} {
		if got := matchGlob(tc.pat, tc.rel); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pat, tc.rel, got, tc.want)
		}
	}
}

// TestSearchFilterPromptEndToEnd drives the real key path: panel focused,
// "x" opens the exclude prompt, typed text + Enter must land in the model
// (a stale-copy capture once lost it) and re-run the search filtered.
func TestSearchFilterPromptEndToEnd(t *testing.T) {
	m, _ := searchSetup(t)
	var tm tea.Model = m.openProjectSearch(m.projectSearchCmd("needle")().(psearchMsg))
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if tm.(Model).mode != modePrompt {
		t.Fatal("x did not open the exclude prompt")
	}
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("*.txt")})
	tm, cmd := tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := tm.(Model)
	if got.search.exc != "*.txt" {
		t.Fatalf("exclude filter = %q, want %q", got.search.exc, "*.txt")
	}
	if cmd == nil {
		t.Fatal("filter change did not re-run the search")
	}
	msg := cmd().(psearchMsg)
	if len(msg.hits) != 1 || !strings.HasSuffix(msg.hits[0].ref.path, "a.go") {
		t.Fatalf("re-run not filtered: %+v", msg.hits)
	}
}

func TestSearchPanelClickPreviews(t *testing.T) {
	m, _ := searchSetup(t)
	msg := m.projectSearchCmd("needle")().(psearchMsg)
	m = m.openProjectSearch(msg)
	m.searchClick(1) // first hit row (row 0 is its file header)
	if m.focus != paneSearch {
		t.Fatal("click should keep focus on the panel (preview)")
	}
	if d := m.doc(); d == nil {
		t.Fatal("click did not open the hit's file")
	}
}

func TestProjectReplace(t *testing.T) {
	m, dir := searchSetup(t)
	m.openFile(filepath.Join(dir, "a.go")) // open doc path: undoable edit + save
	count, files := m.applyProjectReplace("eedle", "ail")
	if count != 2 || files != 2 {
		t.Fatalf("replace = %d in %d files, want 2 in 2", count, files)
	}
	if s := m.doc().ed.Buf.Bytes(); !strings.Contains(string(s), "Nail = 1") {
		t.Fatalf("open doc not replaced: %q", s)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if !strings.Contains(string(onDisk), "Nail = 1") {
		t.Fatalf("open doc replacement not saved: %q", onDisk)
	}
	closed, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
	if !strings.Contains(string(closed), "nail in text") {
		t.Fatalf("closed file not replaced: %q", closed)
	}
	// open-doc replacement is one undo step
	m.doc().ed.UndoStep()
	if s := m.doc().ed.Buf.Bytes(); !strings.Contains(string(s), "Needle = 1") {
		t.Fatalf("undo did not restore: %q", s)
	}
}
