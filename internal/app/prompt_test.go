package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Regression: the prompt was append-only — no way to arrow back into a
// commit message and fix a typo.
func TestPromptCursorEditing(t *testing.T) {
	var got string
	var m tea.Model = setup(t).(Model).prompt("Msg:", "", func(_ *Model, s string) { got = s })
	key := func(k tea.KeyMsg) { m, _ = m.Update(k) }
	typ := func(s string) {
		for _, r := range s {
			key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	typ("fix tyop in parser")
	// "tyop" → "typo": go back before "op", delete both, retype.
	for range " in parser" {
		key(tea.KeyMsg{Type: tea.KeyLeft})
	}
	key(tea.KeyMsg{Type: tea.KeyBackspace})
	key(tea.KeyMsg{Type: tea.KeyBackspace})
	typ("po")
	key(tea.KeyMsg{Type: tea.KeyEnd})
	typ("!")
	key(tea.KeyMsg{Type: tea.KeyHome})
	key(tea.KeyMsg{Type: tea.KeyDelete})
	typ("F")
	key(tea.KeyMsg{Type: tea.KeyEnter})
	if got != "Fix typo in parser!" {
		t.Fatalf("edited prompt = %q, want %q", got, "Fix typo in parser!")
	}
}

// Same regression for the find minibar: cursor movement while editing the
// search query, and an independent cursor for the replace input.
func TestFindMinibarCursorEditing(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	key := func(k tea.KeyMsg) { m, _ = m.Update(k) }
	typ := func(s string) {
		for _, r := range s {
			key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	typ("greeet")
	key(tea.KeyMsg{Type: tea.KeyLeft})
	key(tea.KeyMsg{Type: tea.KeyLeft})
	key(tea.KeyMsg{Type: tea.KeyBackspace})
	if q := m.(Model).query; q != "greet" {
		t.Fatalf("query = %q, want greet", q)
	}
	// The fixed query must actually search: sampleSrc contains greet.
	mm := m.(Model)
	if _, total := mm.doc().ed.SearchInfo(); total == 0 {
		t.Fatal("no matches for the edited query")
	}
	// Switch to replace: fresh cursor at the end of the empty repl input.
	key(tea.KeyMsg{Type: tea.KeyCtrlR})
	typ("hi")
	key(tea.KeyMsg{Type: tea.KeyHome})
	typ("say")
	if r := m.(Model).repl; r != "sayhi" {
		t.Fatalf("repl = %q, want sayhi", r)
	}
}

// Left/right must only move the input cursor — never re-anchor the search;
// match navigation belongs to up/down alone.
func TestFindArrowsDontMoveMatch(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	for _, r := range "l" { // sampleSrc has several l's
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // move off the first match
	mm := m.(Model)
	cur, total := mm.doc().ed.SearchInfo()
	if total < 2 {
		t.Fatalf("need 2+ matches, got %d", total)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	mm = m.(Model)
	if after, _ := mm.doc().ed.SearchInfo(); after != cur {
		t.Fatalf("left/right changed the current match: %d → %d", cur, after)
	}
}

// Typing must refine the search in place — not advance one occurrence per
// keystroke.
func TestFindTypingStaysOnMatch(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	// sampleSrc: "greet" appears twice (comment line 5, func line 6). Once
	// "gr" lands on the comment, typing the rest must refine in place.
	for _, r := range "gr" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	mm := m.(Model)
	first, _ := mm.doc().ed.Cursor()
	for _, r := range "eet" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		mm = m.(Model)
		if line, _ := mm.doc().ed.Cursor(); line != first {
			t.Fatalf("typing %q advanced the match: line %d → %d", string(r), first, line)
		}
	}
}
