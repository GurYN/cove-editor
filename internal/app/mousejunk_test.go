package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestIsMouseJunk(t *testing.T) {
	junk := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	for _, s := range []string{
		";33M[<64;57;33M[<66;57;33M", // leaked flood from a real session
		"[<66;57;33M",                // one whole report minus ESC
		"<66;57;33M",                 // tail after a fused alt+[
	} {
		if !isMouseJunk(junk(s)) {
			t.Errorf("not flagged: %q", s)
		}
	}
	paste := junk("[<66;57;33M")
	paste.Paste = true
	for name, msg := range map[string]tea.KeyMsg{
		"single rune":   junk("M"),
		"plain word":    junk("hello"),
		"digits paste":  junk("66;57"),
		"paste of junk": paste,
	} {
		if isMouseJunk(msg) {
			t.Errorf("%s wrongly flagged: %q", name, string(msg.Runes))
		}
	}
}

// A wheel flood's split report arrives as alt+[ + tail runes; neither may
// reach the buffer. The same chord typed with no mouse nearby still unfuses
// to Esc + '['.
func TestBrokenMouseReportDropped(t *testing.T) {
	m := setup(t)
	altBracket := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}, Alt: true}

	buf := func(m tea.Model) string {
		am := m.(Model)
		return string(am.doc().ed.Buf.Bytes())
	}
	before := buf(m)

	m, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	m, _ = m.Update(altBracket)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<66;57;33M")})
	if got := buf(m); got != before {
		t.Fatalf("mouse junk reached the buffer:\n%.200s", got)
	}

	am := m.(Model)
	am.lastMouse = time.Time{} // no recent mouse: unfuse must still insert '['
	m, _ = tea.Model(am).Update(altBracket)
	if got := buf(m); !strings.Contains(got, "[") {
		t.Fatalf("cold alt+[ no longer unfuses to '[':\n%.200s", got)
	}
}
