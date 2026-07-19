package app

import (
	"bytes"
	"image"
	imgpng "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Binary files and image/PDF assets open as read-only metadata previews —
// never as editable buffers — via both loadDoc and the startup argument.
func TestBinaryAndAssetPreview(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	bin := filepath.Join(t.TempDir(), "blob.zip")
	os.WriteFile(bin, []byte("PK\x03\x04\x00\x00junk"), 0o644)

	d, err := loadDoc(bin)
	if err != nil || !d.virtual || !d.ed.ReadOnly {
		t.Fatalf("binary not a read-only preview: err=%v virtual=%v", err, d != nil && d.virtual)
	}
	if got := string(d.ed.Buf.Bytes()); !strings.Contains(got, "binary file") {
		t.Fatalf("preview text: %q", got)
	}

	// 1×1 PNG: preview must decode and show dimensions, no NUL soup.
	png := filepath.Join(t.TempDir(), "dot.png")
	var img bytes.Buffer
	imgpng.Encode(&img, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	os.WriteFile(png, img.Bytes(), 0o644)
	d, err = loadDoc(png)
	if err != nil || !d.virtual {
		t.Fatalf("png not a preview: %v", err)
	}
	if got := string(d.ed.Buf.Bytes()); !strings.Contains(got, "PNG image, 1×1 px") {
		t.Fatalf("png preview text: %q", got)
	}

	// Startup argument: opens the preview tab instead of refusing.
	data, _ := os.ReadFile(bin)
	m := New(bin, data)
	if len(m.docs) != 1 || !m.docs[0].virtual {
		t.Fatalf("startup: %d docs", len(m.docs))
	}

	// UTF-8 text stays openable and editable.
	txt := filepath.Join(t.TempDir(), "ok.go")
	os.WriteFile(txt, []byte("package ok // héllo\n"), 0o644)
	if d, err := loadDoc(txt); err != nil || d.virtual {
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

// save() must confirm before overwriting a file that appeared on disk after
// a not-yet-existing path was opened (e.g. `cove new.txt` + external create),
// and must go through a temp file so a failed write can't truncate.
func TestSaveGuards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	d := newDoc(path, nil) // opened before the file exists
	d.ed.InsertText("mine")

	os.WriteFile(path, []byte("theirs"), 0o644)
	if msg := d.save(); !strings.Contains(msg, "save again") {
		t.Fatalf("externally-created file overwritten without confirm: %q", msg)
	}
	if msg := d.save(); msg != "saved" {
		t.Fatalf("second save: %q", msg)
	}
	if data, _ := os.ReadFile(path); string(data) != "mine" {
		t.Fatalf("content: %q", data)
	}
	if _, err := os.Stat(path + ".cove~"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}

	// A custom mode survives the write-then-rename.
	os.Chmod(path, 0o600)
	d.ed.InsertText("!")
	if msg := d.save(); msg != "saved" {
		t.Fatalf("resave: %q", msg)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode not preserved: %v", fi.Mode())
	}
}

// Non-UTF-8 text files open with a one-shot lossy-display warning.
func TestInvalidUTF8Warns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latin1.txt")
	os.WriteFile(path, []byte("caf\xe9\n"), 0o644) // Latin-1 é, no NUL
	d, err := loadDoc(path)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if !strings.Contains(d.warn, "UTF-8") {
		t.Fatalf("warn = %q", d.warn)
	}
}
