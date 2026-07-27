package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A dirty buffer's snapshot survives a "crash" (no clean shutdown) and is
// restored, undoably, when the file is reopened by a fresh instance.
func TestBackupRestoreAfterCrash(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("hello\n"), 0o644)

	m := New(path, []byte("hello\n"))
	d := m.doc()
	d.ed.InsertText("edited ")
	if !d.ed.Dirty {
		t.Fatal("buffer should be dirty")
	}
	m.writeBackups() // the watch tick's snapshot
	if !d.backedUp {
		t.Fatal("snapshot not written")
	}

	// "Crash": no clearBackups. A fresh instance reopens the file.
	m2 := New(path, []byte("hello\n"))
	d2 := m2.doc()
	if got := string(d2.ed.Buf.Bytes()); got != "edited hello\n" {
		t.Fatalf("restore: got %q", got)
	}
	if !d2.ed.Dirty {
		t.Fatal("restored buffer must read dirty")
	}
	d2.ed.UndoStep() // restore is one undoable transaction
	if got := string(d2.ed.Buf.Bytes()); got != "hello\n" {
		t.Fatalf("undo after restore: got %q", got)
	}
}

// A file rewritten on disk after the crash outranks the snapshot: no
// restore, backup dropped, user told.
func TestBackupStaleOnDiskChange(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("hello\n"), 0o644)

	m := New(path, []byte("hello\n"))
	m.doc().ed.InsertText("edited ")
	m.writeBackups()

	// Rewrite on disk with a different mtime, as an outside tool would.
	os.WriteFile(path, []byte("rewritten\n"), 0o644)
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(path, later, later)

	m2 := New(path, []byte("rewritten\n"))
	if got := string(m2.doc().ed.Buf.Bytes()); got != "rewritten\n" {
		t.Fatalf("stale snapshot must not restore: got %q", got)
	}
	if !strings.Contains(m2.lastMsg, "discarded") {
		t.Fatalf("expected a discard notice, got %q", m2.lastMsg)
	}
	if _, err := os.Stat(backupPath(path)); !os.IsNotExist(err) {
		t.Fatal("stale backup not removed")
	}
}

// Saving cleans the buffer; the next tick removes the snapshot. Quit
// removes them outright.
func TestBackupClearedWhenClean(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("hello\n"), 0o644)

	m := New(path, []byte("hello\n"))
	d := m.doc()
	d.ed.InsertText("x")
	m.writeBackups()
	if _, err := os.Stat(backupPath(path)); err != nil {
		t.Fatal("snapshot missing")
	}
	if s := d.save(); s != "saved" {
		t.Fatalf("save: %s", s)
	}
	m.writeBackups() // clean buffer → snapshot goes
	if _, err := os.Stat(backupPath(path)); !os.IsNotExist(err) {
		t.Fatal("snapshot should be removed once clean")
	}

	d.ed.InsertText("y")
	m.writeBackups()
	m.clearBackups() // quit path
	if _, err := os.Stat(backupPath(path)); !os.IsNotExist(err) {
		t.Fatal("quit must drop snapshots")
	}
}
