package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSyncWatched: the mtime sweep must report external creates, edits, and
// deletes exactly once, and stay quiet when nothing changed.
func TestSyncWatched(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	os.WriteFile(a, []byte("package a\n"), 0o644)

	m := New(dir, nil) // baseline sweep runs in New
	if ev := m.syncWatched(); len(ev) != 0 {
		t.Fatalf("quiet sweep reported %d events", len(ev))
	}

	os.WriteFile(a, []byte("package a // edited\n"), 0o644)
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(a, later, later) // don't depend on fs mtime granularity
	b := filepath.Join(dir, "b.go")
	os.WriteFile(b, []byte("package a\n"), 0o644)

	got := map[string]int{}
	for _, e := range m.syncWatched() {
		got[filepath.Base(e.URI)] = e.Type
	}
	if got["a.go"] != 2 || got["b.go"] != 1 || len(got) != 2 {
		t.Fatalf("events = %v, want a.go:2 b.go:1", got)
	}

	os.Remove(b)
	ev := m.syncWatched()
	if len(ev) != 1 || ev[0].Type != 3 || !strings.HasSuffix(ev[0].URI, "b.go") {
		t.Fatalf("delete events = %v, want one b.go:3", ev)
	}
}
