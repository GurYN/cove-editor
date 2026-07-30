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

func TestContextMenuMouseSelect(t *testing.T) {
	m, _ := gitSetup(t)
	m = rightClick(m, 10, 3) // git file row → menu with "Open Diff", "Open File", "Stage", ...
	// Find "Stage" (item index 2) in the rendered frame and left-click it.
	v := frame(m)
	var y int
	for i, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "Stash File") {
			y = i - 1 // "Stage" sits one row above
		}
	}
	if y == 0 {
		t.Fatalf("menu not rendered:\n%s", v)
	}
	x := m.width / 2
	m, _ = m.update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.ovKind != overlayNone {
		t.Fatal("click on a menu item did not close the menu")
	}
	if v := frame(m); !strings.Contains(v, "Staged (1)") {
		t.Fatalf("click on Stage did not stage:\n%s", v)
	}

	// A click on the box border/title keeps the menu open; outside closes it.
	m = rightClick(m, 10, 3)
	m, _ = m.update(tea.MouseMsg{X: x, Y: y - 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}) // title row
	if m.ovKind == overlayNone {
		t.Fatal("click on the menu title closed the menu")
	}
	m, _ = m.update(tea.MouseMsg{X: x, Y: m.height - 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.ovKind != overlayNone {
		t.Fatal("click outside the menu did not close it")
	}
}

func TestContextMenuMouseHover(t *testing.T) {
	m, _ := gitSetup(t)
	m = rightClick(m, 10, 3)
	if m.ov.Selected() != 0 {
		t.Fatal("menu should open with the first item selected")
	}
	var y int
	for i, line := range strings.Split(frame(m), "\n") {
		if strings.Contains(line, "Stage") && !strings.Contains(line, "Unstage") {
			y = i
		}
	}
	m, _ = m.update(tea.MouseMsg{X: m.width / 2, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if m.ovKind == overlayNone {
		t.Fatal("hover closed the menu")
	}
	if m.ov.Selected() != 3 {
		t.Fatalf("hover over Stage selected item %d, want 3", m.ov.Selected())
	}
}

func TestOverlayMouseWheel(t *testing.T) {
	m, _ := gitSetup(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlP}) // palette: more items than fit
	before := frame(m)
	wheel := func(b tea.MouseButton) {
		m, _ = m.update(tea.MouseMsg{X: m.width / 2, Y: 5, Action: tea.MouseActionPress, Button: b})
	}
	// The first visible list row: two lines under the "Command:" title.
	topRow := func(v string) string {
		lines := strings.Split(v, "\n")
		for i, l := range lines {
			if strings.Contains(l, "Command:") {
				return lines[i+1]
			}
		}
		return ""
	}
	wheel(tea.MouseButtonWheelDown)
	if m.ovKind == overlayNone {
		t.Fatal("wheel closed the palette")
	}
	down := frame(m)
	if topRow(down) == topRow(before) {
		t.Fatal("wheel down did not scroll the list")
	}
	wheel(tea.MouseButtonWheelUp)
	if topRow(frame(m)) != topRow(before) {
		t.Fatal("wheel up did not scroll back")
	}
	if m.ov.Selected() < 0 {
		t.Fatal("wheel lost the selection")
	}
	// Fast trackpad scrolling floods horizontal wheel events: never a close.
	wheel(tea.MouseButtonWheelLeft)
	wheel(tea.MouseButtonWheelRight)
	if m.ovKind == overlayNone {
		t.Fatal("horizontal wheel closed the palette")
	}
}
