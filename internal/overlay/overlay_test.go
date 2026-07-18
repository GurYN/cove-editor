package overlay

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// The screenshot bug: "git" ranked scattered matches (Go to Symbol…,
// Go to Definition) among the contiguous "Git:" commands.
func TestFilterRanksContiguousFirst(t *testing.T) {
	items := []Item{
		{Label: "Go to Symbol in File (Outline)"},
		{Label: "Go to Definition"},
		{Label: "Open Settings (config.toml)"},
		{Label: "Git: Stage All"},
		{Label: "Git: Switch Branch…"},
		{Label: "Git: Undo Last Commit (Keep Changes Staged)"},
	}
	m := New("Command:", items, 80)
	for _, r := range "git" {
		m, _, _ = m.Update(keyRune(r))
	}
	// Every "Git:" item must rank above every scattered match.
	lastGit, firstOther := -1, len(m.matches)
	for pos, match := range m.matches {
		if items[match.Index].Label[:3] == "Git" {
			lastGit = pos
		} else if pos < firstOther {
			firstOther = pos
		}
	}
	if lastGit > firstOther {
		var got []string
		for _, match := range m.matches {
			got = append(got, items[match.Index].Label)
		}
		t.Fatalf("scattered match ranked above a Git: command:\n%v", got)
	}
}
