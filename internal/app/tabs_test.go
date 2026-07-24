package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// tabsSetup opens 12 files on an 80-col screen — far more tabs than fit.
func tabsSetup(t *testing.T) Model {
	t.Helper()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	m := New(dir, nil)
	m, _ = m.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for i := 0; i < 12; i++ {
		p := filepath.Join(dir, fmt.Sprintf("file%02d.txt", i))
		os.WriteFile(p, []byte("x\n"), 0o644)
		m.openFile(p)
	}
	return m
}

func TestTabBarOverflowScrolls(t *testing.T) {
	m := tabsSetup(t)
	m.active = len(m.docs) - 1
	bar := ansi.Strip(m.renderTabBar())
	if w := lipgloss.Width(bar); w != 80 {
		t.Fatalf("tab bar width = %d, want 80:\n%q", w, bar)
	}
	if !strings.Contains(bar, "file11.txt") || !strings.HasPrefix(bar, "  ‹ ") {
		t.Fatalf("active last tab not visible / no left indicator:\n%q", bar)
	}
	// Every tab must be reachable: activating any index shows its label.
	for i := range m.docs {
		m.active = i
		if b := ansi.Strip(m.renderTabBar()); !strings.Contains(b, fmt.Sprintf("file%02d.txt", i)) {
			t.Fatalf("active tab %d scrolled out of view:\n%q", i, b)
		}
		if w := lipgloss.Width(ansi.Strip(m.renderTabBar())); w != 80 {
			t.Fatalf("bar width %d at active=%d", w, i)
		}
	}
	m.active = 0
	if b := ansi.Strip(m.renderTabBar()); !strings.HasSuffix(b, " ›  ") || strings.HasPrefix(b, "  ‹") {
		t.Fatalf("want right indicator only at first tab:\n%q", b)
	}
}

func TestTabBarClickMapsToVisibleTabs(t *testing.T) {
	m := tabsSetup(t)
	m.active = len(m.docs) - 1
	first, _, _ := m.tabWindow()
	if first == 0 {
		t.Fatal("test premise broken: everything fits")
	}
	// Hidden tabs keep a zero click range.
	for i, r := range m.tabRanges() {
		if i < first && r.end != r.start {
			t.Fatalf("hidden tab %d has click range %+v", i, r)
		}
	}
	// Clicking mid-label of the first visible tab activates it.
	r := m.tabRanges()[first]
	m, _ = m.update(tea.MouseMsg{X: r.start + 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != first {
		t.Fatalf("click activated tab %d, want %d", m.active, first)
	}
	// Clicking ‹ jumps to the nearest hidden-left tab.
	first, _, _ = m.tabWindow()
	m, _ = m.update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != first-1 {
		t.Fatalf("‹ click: active = %d, want %d", m.active, first-1)
	}
	// Clicking › jumps right of the window.
	_, last, _ := m.tabWindow()
	m, _ = m.update(tea.MouseMsg{X: 79, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != last+1 {
		t.Fatalf("› click: active = %d, want %d", m.active, last+1)
	}
}
