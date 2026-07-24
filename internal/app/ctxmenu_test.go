package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func rightClick(m Model, x, y int) Model {
	m, _ = m.update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	return m
}

func TestGitPanelContextMenu(t *testing.T) {
	m, _ := gitSetup(t)
	// Row layout: y=0 tabs, y=1 header, y=2 "Changes (1)", y=3 file row.
	m = rightClick(m, 10, 3)
	if m.ovKind != overlayPalette {
		t.Fatal("right-click on a git file row did not open the menu")
	}
	v := frame(m)
	for _, want := range []string{"a.txt:", "Open Diff", "Stage", "Stash File", "Discard Changes"} {
		if !strings.Contains(v, want) {
			t.Fatalf("git menu missing %q:\n%s", want, v)
		}
	}
	// Run "Stage" through the menu machinery: it must act on the clicked row.
	for _, a := range m.ovActions {
		if a.Title == "Stage" {
			a.Do(&m)
			break
		}
	}
	if v := frame(m); !strings.Contains(v, "Staged (1)") {
		t.Fatalf("menu Stage did not stage:\n%s", v)
	}
	if m.focus != paneGit {
		t.Fatal("menu action lost the git panel focus")
	}

	// Right-click on the section header: repo-level menu.
	m.ovKind = overlayNone
	m = rightClick(m, 10, 2)
	if v := frame(m); !strings.Contains(v, "Commit Staged") || !strings.Contains(v, "Push") {
		t.Fatalf("repo-level menu missing entries:\n%s", v)
	}
}

func TestTreeContextMenu(t *testing.T) {
	m, _ := gitSetup(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlB}) // git panel → file tree
	// y=2 is the first tree row (a.txt).
	m = rightClick(m, 10, 2)
	if m.ovKind != overlayPalette {
		t.Fatal("right-click on the tree did not open the menu")
	}
	v := frame(m)
	for _, want := range []string{"a.txt:", "Open", "Rename…", "Delete…"} {
		if !strings.Contains(v, want) {
			t.Fatalf("tree menu missing %q:\n%s", want, v)
		}
	}
	// A file row must not offer folder/file creation.
	if strings.Contains(v, "New Folder…") || strings.Contains(v, "New File…") {
		t.Fatalf("file row menu offers creation entries:\n%s", v)
	}
	// "Rename…" opens the prompt pre-filled with the selected file's name.
	for _, a := range m.ovActions {
		if a.Title == "Rename…" {
			a.Do(&m)
			break
		}
	}
	if m.mode != modePrompt || !strings.Contains(m.promptText, "a.txt") {
		t.Fatalf("menu Rename did not prompt: mode=%v text=%q", m.mode, m.promptText)
	}
}
