package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// updateCheckMsg carries the latest release tag ("" on any failure —
// offline launches must stay silent). manual marks a palette-invoked
// check, which reports "up to date" and failures too.
type updateCheckMsg struct {
	tag    string
	manual bool
}

// updateURL is a var so tests can point it at a stub server.
var updateURL = "https://api.github.com/repos/GurYN/cove-editor/releases/latest"

// checkUpdate fetches the latest release tag from GitHub in the background.
func checkUpdate(manual bool) tea.Cmd {
	return func() tea.Msg {
		cl := &http.Client{Timeout: 3 * time.Second}
		resp, err := cl.Get(updateURL)
		if err != nil {
			return updateCheckMsg{manual: manual}
		}
		defer resp.Body.Close()
		var rel struct {
			TagName string `json:"tag_name"`
		}
		if resp.StatusCode != http.StatusOK ||
			json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel) != nil {
			return updateCheckMsg{manual: manual}
		}
		return updateCheckMsg{tag: rel.TagName, manual: manual}
	}
}

// maybeCheckUpdate fires the launch version check: every launch (one
// background GET, silent on failure — far under GitHub's unauthenticated
// rate limit), never for dev builds, never when [update] check = false.
// A time throttle here proved worse than none: it hid releases published
// within the window (up to 24h of "no toast" after an update shipped).
func (m Model) maybeCheckUpdate() tea.Cmd {
	if !m.updateCheck || Version == "dev" {
		return nil
	}
	return checkUpdate(false)
}

func (m Model) handleUpdateCheck(msg updateCheckMsg) Model {
	switch {
	case newerVersion(Version, msg.tag):
		m.updateToast = "Cove " + msg.tag + " is available (you have " + Version + ").\nbrew upgrade cove"
	case msg.manual && msg.tag == "":
		m.notifyErr("update check failed")
	case msg.manual:
		m.notify("up to date — latest release is " + msg.tag)
	}
	return m
}

// newerVersion reports whether the release tag latest ("v0.2.0") is newer
// than cur. Dev builds and unparsable tags never prompt.
func newerVersion(cur, latest string) bool {
	c, okc := verNums(cur)
	l, okl := verNums(latest)
	if !okc || !okl {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func verNums(s string) ([3]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "-") // ignore prerelease suffixes
	var out [3]int
	fields := strings.Split(s, ".")
	if len(fields) == 0 || len(fields) > 3 {
		return out, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
