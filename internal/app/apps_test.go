package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestConfiguredAppAction proves [apps.*] config becomes a registry action
// that spawns a labeled terminal instance, and that re-invoking focuses the
// running instance instead of spawning a twin.
func TestConfiguredAppAction(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("[apps.lister]\ncommand = [\"ls\"]\nkey = \"ctrl+alt+l\"\n"), 0o644)
	t.Setenv("COVE_CONFIG", cfgPath)

	m := New(t.TempDir(), nil)
	act := m.reg.ByID("app.lister")
	if act == nil {
		t.Fatal("app.lister not registered")
	}
	if act.Key != "ctrl+alt+l" {
		t.Fatalf("key = %q", act.Key)
	}

	if cmd := act.Do(&m); cmd == nil {
		t.Fatal("spawn returned nil cmd (PTY failed?)")
	}
	defer m.terms[0].Close()
	if len(m.terms) != 1 || m.terms[0].Label != "lister" {
		t.Fatalf("terms = %d, label = %q", len(m.terms), m.terms[0].Label)
	}
	if !m.termOpen || m.focus != paneTerminal {
		t.Fatal("panel not open/focused")
	}

	m.termActive = -1 // poison: re-invoke must find the instance by label
	act.Do(&m)
	if len(m.terms) != 1 || m.termActive != 0 {
		t.Fatalf("re-invoke spawned twin: terms = %d, active = %d", len(m.terms), m.termActive)
	}
}

// TestDeadKeyWarning: ctrl+i arrives as tab in terminals — binding it must
// warn instead of silently never firing.
func TestDeadKeyWarning(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("[keys]\n\"file.save\" = \"ctrl+i\"\n"), 0o644)
	t.Setenv("COVE_CONFIG", cfgPath)

	m := New(t.TempDir(), nil)
	warns := strings.Join(m.cfgWarns, "; ")
	if !strings.Contains(warns, "ctrl+i") || !strings.Contains(warns, "tab") {
		t.Fatalf("no dead-key warning, cfgWarns = %q", warns)
	}
}

// TestConflictWarning: binding an app to a key Cove already uses must warn;
// a clean swap of two keys in [keys] must not.
func TestConflictWarning(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("[apps.htop]\ncommand = [\"htop\"]\nkey = \"ctrl+j\"\n"), 0o644)
	t.Setenv("COVE_CONFIG", cfgPath)
	m := New(t.TempDir(), nil)
	if !strings.Contains(strings.Join(m.cfgWarns, "; "), "already bound to term.toggle") {
		t.Fatalf("no conflict warning, cfgWarns = %q", m.cfgWarns)
	}

	os.WriteFile(cfgPath, []byte("[keys]\n\"term.toggle\" = \"ctrl+g\"\n\"git.toggle\" = \"ctrl+j\"\n"), 0o644)
	m = New(t.TempDir(), nil)
	if strings.Contains(strings.Join(m.cfgWarns, "; "), "already bound") {
		t.Fatalf("swap warned: %q", m.cfgWarns)
	}
}

// TestConfigWarningSurvivesRestore: restoreSession clears lastMsg to hide
// file-open noise — config warnings live in cfgWarns and must survive, and
// the toast must actually render.
func TestConfigWarningSurvivesRestore(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	os.WriteFile(file, []byte("hi\n"), 0o644)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("[apps.htop]\ncommand = [\"htop\"]\nkey = \"ctrl+j\"\n"), 0o644)
	t.Setenv("COVE_CONFIG", cfgPath)

	m := New(dir, nil)
	m.openFile(file)
	m.saveSession()

	m = New(dir, nil) // session exists now: restoreSession runs its wipe
	if len(m.docs) != 1 {
		t.Fatalf("session not restored, docs = %d", len(m.docs))
	}
	if !strings.Contains(strings.Join(m.cfgWarns, "; "), "already bound to term.toggle") {
		t.Fatalf("restore wiped the config warning, cfgWarns = %q", m.cfgWarns)
	}
	m.width, m.height = 100, 30
	m.layout()
	if !strings.Contains(m.View(), "already bound") {
		t.Fatal("config toast not rendered in View")
	}
}

// TestPaletteFromTerminal: ctrl+p must open the palette even when the
// terminal panel has focus (discoverability beats shell history).
func TestPaletteFromTerminal(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	m := New(t.TempDir(), nil)
	if cmd := m.newTerm(); cmd == nil {
		t.Skip("no PTY available")
	}
	defer m.terms[0].Close()
	if m.focus != paneTerminal {
		t.Fatal("terminal not focused after spawn")
	}
	m, _ = m.dispatchKey(tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.ovKind != overlayPalette {
		t.Fatalf("palette not open, ovKind = %d", m.ovKind)
	}
}

// TestSidebarFromTerminal: ctrl+b / ctrl+g must reach Cove's panel toggles
// from a focused terminal, not the shell.
func TestSidebarFromTerminal(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	m := New(t.TempDir(), nil)
	if cmd := m.newTerm(); cmd == nil {
		t.Skip("no PTY available")
	}
	defer m.terms[0].Close()

	m, _ = m.dispatchKey(tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.focus != paneSidebar {
		t.Fatalf("ctrl+b: focus = %v, want sidebar", m.focus)
	}

	m.focus = paneTerminal
	m, _ = m.dispatchKey(tea.KeyMsg{Type: tea.KeyCtrlG})
	if m.focus != paneGit {
		t.Fatalf("ctrl+g: focus = %v, want git panel", m.focus)
	}
}
