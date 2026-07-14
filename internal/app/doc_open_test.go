package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Binary files (NUL in the head) must not open as editor tabs — neither via
// the finder/tree path (loadDoc) nor as the startup argument.
func TestBinaryFilesRefused(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	bin := filepath.Join(t.TempDir(), "blob.zip")
	os.WriteFile(bin, []byte("PK\x03\x04\x00\x00junk"), 0o644)

	if _, err := loadDoc(bin); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("loadDoc accepted a binary: %v", err)
	}

	data, _ := os.ReadFile(bin)
	m := New(bin, data)
	if len(m.docs) != 0 || !strings.Contains(m.lastMsg, "binary") {
		t.Fatalf("startup opened a binary: %d docs, msg %q", len(m.docs), m.lastMsg)
	}

	// UTF-8 text stays openable.
	txt := filepath.Join(t.TempDir(), "ok.go")
	os.WriteFile(txt, []byte("package ok // héllo\n"), 0o644)
	if _, err := loadDoc(txt); err != nil {
		t.Fatalf("text file refused: %v", err)
	}
}

// The 2s poll reloads clean buffers edited outside Cove (cursor kept,
// undoable) and warns once — without reloading — when the buffer is dirty.
func TestExternalChangeDetection(t *testing.T) {
	m := setup(t).(Model)
	d := m.doc()
	d.ed.Go(1, 0)

	// Clean buffer: outside edit → reload in place.
	os.WriteFile(d.path, []byte("package sample // edited outside\n"), 0o644)
	os.Chtimes(d.path, time.Now().Add(3*time.Second), time.Now().Add(3*time.Second))
	m2, _ := m.update(watchTickMsg{})
	if got := string(m2.doc().ed.Buf.Bytes()); !strings.Contains(got, "edited outside") {
		t.Fatalf("clean buffer not reloaded: %q", got)
	}
	if m2.doc().ed.Dirty || !strings.Contains(m2.lastMsg, "reloaded") {
		t.Fatalf("dirty=%v msg=%q", m2.doc().ed.Dirty, m2.lastMsg)
	}

	// Dirty buffer: outside edit → warn once, keep the user's content.
	m2, _ = m2.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	os.WriteFile(d.path, []byte("package sample // edited again\n"), 0o644)
	os.Chtimes(d.path, time.Now().Add(6*time.Second), time.Now().Add(6*time.Second))
	m2, _ = m2.update(watchTickMsg{})
	if got := string(m2.doc().ed.Buf.Bytes()); strings.Contains(got, "edited again") {
		t.Fatal("dirty buffer was clobbered by the reload")
	}
	if !strings.Contains(m2.lastMsg, "unsaved edits") {
		t.Fatalf("no warning: %q", m2.lastMsg)
	}
	m2.lastMsg = ""
	m2, _ = m2.update(watchTickMsg{})
	if m2.lastMsg != "" {
		t.Fatalf("warning repeated on next tick: %q", m2.lastMsg)
	}
}
