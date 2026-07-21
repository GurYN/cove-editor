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
	if mm.ovKind != overlayDiags {
		t.Fatal("search overlay not open")
	}
	var m2 tea.Model = mm
	m2, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m2.(Model); len(got.jumps) == 0 {
		t.Fatal("hit jump did not record a jump-list entry")
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
