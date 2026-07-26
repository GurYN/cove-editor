package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	if got := m.handleUpdateCheck(updateCheckMsg{tag: "v0.2.0"}).updateToast; got != "Cove v0.2.0 is available (you have v0.1.0).\nbrew upgrade cove" {
		t.Fatalf("newer: updateToast = %q", got)
	}
	if got := m.handleUpdateCheck(updateCheckMsg{tag: "v0.1.0"}); got.lastMsg != "" || got.updateToast != "" {
		t.Fatalf("auto up-to-date must stay silent, got %q / %q", got.lastMsg, got.updateToast)
	}
	if got := m.handleUpdateCheck(updateCheckMsg{tag: "v0.1.0", manual: true}).lastMsg; got != "up to date — latest release is v0.1.0" {
		t.Fatalf("manual up-to-date: lastMsg = %q", got)
	}
	if got := m.handleUpdateCheck(updateCheckMsg{manual: true}).lastMsg; got != "update check failed" {
		t.Fatalf("manual failure: lastMsg = %q", got)
	}
}

func TestUpdateToastRendered(t *testing.T) {
	oldV := Version
	Version = "v0.1.0"
	defer func() { Version = oldV }()
	m := setup(t).(Model)
	m = m.handleUpdateCheck(updateCheckMsg{tag: "v99.0.0"})
	frame := ansi.Strip(m.View())
	if !strings.Contains(frame, "↑ update") || !strings.Contains(frame, "brew upgrade cove") {
		t.Fatal("update toast not rendered")
	}
	// any key dismisses it
	m2, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if strings.Contains(ansi.Strip(m2.View()), "↑ update") {
		t.Fatal("toast survived a keypress")
	}
}

func TestMaybeCheckUpdate(t *testing.T) {
	oldV := Version
	Version = "v0.1.0"
	defer func() { Version = oldV }()

	m := Model{updateCheck: true}
	if m.maybeCheckUpdate() == nil {
		t.Fatal("launch: expected a check cmd")
	}
	if m.maybeCheckUpdate() == nil {
		t.Fatal("every launch checks — no throttle")
	}

	if (Model{updateCheck: false}).maybeCheckUpdate() != nil {
		t.Fatal("[update] check = false must disable the check")
	}
	Version = "dev"
	if m.maybeCheckUpdate() != nil {
		t.Fatal("dev build must never check")
	}
}
