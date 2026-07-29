package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestTermCycleKeys proves ctrl+pgdown/pgup switch the active terminal
// instance (wrapping) instead of reaching the shell, even with the terminal
// focused.
func TestTermCycleKeys(t *testing.T) {
	cfgPath := t.TempDir() + "/config.toml"
	t.Setenv("COVE_CONFIG", cfgPath)
	m := New(t.TempDir(), nil)

	if cmd := m.spawnTerm([]string{"sleep", "30"}, ""); cmd == nil {
		t.Skip("PTY unavailable")
	}
	m.spawnTerm([]string{"sleep", "30"}, "")
	defer m.terms[0].Close()
	defer m.terms[1].Close()
	if m.termActive != 1 || m.focus != paneTerminal {
		t.Fatalf("setup: active = %d, focus = %d", m.termActive, m.focus)
	}

	m, _ = m.dispatchKey(tea.KeyMsg{Type: tea.KeyCtrlPgDown})
	if m.termActive != 0 {
		t.Fatalf("next did not wrap: active = %d", m.termActive)
	}
	m, _ = m.dispatchKey(tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	if m.termActive != 1 {
		t.Fatalf("prev did not wrap: active = %d", m.termActive)
	}
}
