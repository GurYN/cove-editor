package app

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/config"
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

// updateStampPath is the last-check marker, next to config.toml; its
// mtime is the timestamp (the file stays empty).
func updateStampPath() string {
	return filepath.Join(filepath.Dir(config.Path()), "last-update-check")
}

// maybeCheckUpdate fires the launch version check: at most once a day,
// never for dev builds, never when [update] check = false.
func (m Model) maybeCheckUpdate() tea.Cmd {
	if !m.updateCheck || Version == "dev" {
		return nil
	}
	p := updateStampPath()
	if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		return nil
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, nil, 0o644)
	return checkUpdate(false)
}

func (m Model) handleUpdateCheck(msg updateCheckMsg) Model {
	switch {
	case newerVersion(Version, msg.tag):
		m.lastMsg = "Cove " + msg.tag + " available — brew upgrade cove"
	case msg.manual && msg.tag == "":
		m.lastMsg = "update check failed"
	case msg.manual:
		m.lastMsg = "up to date — latest release is " + msg.tag
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
