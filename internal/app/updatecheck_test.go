package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.9.0", "v0.10.0", true}, // numeric, not lexicographic
		{"v0.1.0", "v0.1.1", true},
		{"v0.1.0", "v1.0.0", true},
		{"v0.2.0", "v0.2.0", false},
		{"v0.2.0", "v0.1.9", false},
		{"v1.0.0", "v0.9.9", false},
		{"dev", "v9.9.9", false},   // dev builds never prompt
		{"v0.1.0", "", false},      // failed fetch
		{"v0.1.0", "junk", false},  // unparsable tag
		{"v0.1.0", "v0.2", true},   // short tags parse (missing fields = 0)
		{"0.1.0", "v0.1.1", true},  // v prefix optional
		{"v0.1.0", "v0.2.0-rc1", true}, // prerelease suffix ignored
	}
	for _, c := range cases {
		if got := newerVersion(c.cur, c.latest); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestCheckUpdateFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name": "v0.3.0", "name": "Cove v0.3.0"}`))
	}))
	defer srv.Close()
	old := updateURL
	updateURL = srv.URL
	defer func() { updateURL = old }()

	msg := checkUpdate(true)().(updateCheckMsg)
	if msg.tag != "v0.3.0" || !msg.manual {
		t.Fatalf("got %+v, want tag v0.3.0 manual", msg)
	}
}

func TestCheckUpdateFailsSilently(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // rate-limited
	}))
	defer srv.Close()
	old := updateURL
	updateURL = srv.URL
	defer func() { updateURL = old }()

	if msg := checkUpdate(false)().(updateCheckMsg); msg.tag != "" {
		t.Fatalf("got tag %q, want empty on non-200", msg.tag)
	}
}

func TestHandleUpdateCheck(t *testing.T) {
	oldV := Version
	Version = "v0.1.0"
	defer func() { Version = oldV }()

	var m Model
	if got := m.handleUpdateCheck(updateCheckMsg{tag: "v0.2.0"}).lastMsg; got != "Cove v0.2.0 available — brew upgrade cove" {
		t.Fatalf("newer: lastMsg = %q", got)
	}
	if got := m.handleUpdateCheck(updateCheckMsg{tag: "v0.1.0"}).lastMsg; got != "" {
		t.Fatalf("auto up-to-date must stay silent, got %q", got)
	}
	if got := m.handleUpdateCheck(updateCheckMsg{tag: "v0.1.0", manual: true}).lastMsg; got != "up to date — latest release is v0.1.0" {
		t.Fatalf("manual up-to-date: lastMsg = %q", got)
	}
	if got := m.handleUpdateCheck(updateCheckMsg{manual: true}).lastMsg; got != "update check failed" {
		t.Fatalf("manual failure: lastMsg = %q", got)
	}
}

func TestMaybeCheckUpdateStamp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COVE_CONFIG", filepath.Join(dir, "config.toml"))
	oldV := Version
	Version = "v0.1.0"
	defer func() { Version = oldV }()

	m := Model{updateCheck: true}
	if m.maybeCheckUpdate() == nil {
		t.Fatal("first launch: expected a check cmd")
	}
	if m.maybeCheckUpdate() != nil {
		t.Fatal("second launch same day: expected no check")
	}
	// stamp older than a day → checks again
	stale := time.Now().Add(-25 * time.Hour)
	os.Chtimes(updateStampPath(), stale, stale)
	if m.maybeCheckUpdate() == nil {
		t.Fatal("stale stamp: expected a check cmd")
	}

	if (Model{updateCheck: false}).maybeCheckUpdate() != nil {
		t.Fatal("[update] check = false must disable the check")
	}
	Version = "dev"
	if m.maybeCheckUpdate() != nil {
		t.Fatal("dev build must never check")
	}
}
