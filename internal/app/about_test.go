package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAboutBox(t *testing.T) {
	m := setup(t)
	// open via the registry action, like the palette would
	mm := m.(Model)
	mm.reg.ByID("app.about").Do(&mm)
	if !mm.aboutOpen {
		t.Fatal("app.about did not open the about box")
	}
	frame := mm.View()
	if !strings.Contains(frame, "terminal IDE") || !strings.Contains(frame, Version) {
		t.Fatalf("about box missing name/version:\n%.800s", frame)
	}

	// any key closes
	var next tea.Model = mm
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if next.(Model).aboutOpen {
		t.Fatal("keypress did not close the about box")
	}
}
