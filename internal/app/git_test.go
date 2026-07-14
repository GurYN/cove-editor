package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// gitSetup builds a repo with a committed-then-modified a.txt and returns
// the app rooted there with the git panel open.
func gitSetup(t *testing.T) (Model, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	top := t.TempDir()
	g := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = top
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	g("init", "-q", "-b", "main")
	g("config", "user.email", "t@t.t")
	g("config", "user.name", "t")
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("one\n"), 0o644)
	g("add", "-A")
	g("commit", "-q", "-m", "init")
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("one\ntwo\n"), 0o644)

	m := New(top, nil)
	m, _ = m.update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlG})
	return m, top
}

func frame(m Model) string { return ansi.Strip(m.View()) }

func TestGitPanelStatusAndStage(t *testing.T) {
	m, _ := gitSetup(t)
	v := frame(m)
	if !strings.Contains(v, "Git: main") || !strings.Contains(v, "Changes (1)") ||
		!strings.Contains(v, "M a.txt") {
		t.Fatalf("panel missing status:\n%s", v)
	}
	if !strings.Contains(v, "⎇ main") {
		t.Fatalf("status bar missing branch:\n%s", v)
	}

	m, _ = m.update(tea.KeyMsg{Type: tea.KeySpace}) // stage
	if v = frame(m); !strings.Contains(v, "Staged (1)") {
		t.Fatalf("stage failed:\n%s", v)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeySpace}) // unstage
	if v = frame(m); strings.Contains(v, "Staged (1)") || !strings.Contains(v, "Changes (1)") {
		t.Fatalf("unstage failed:\n%s", v)
	}
}

func TestGitDiffTabIsReadOnly(t *testing.T) {
	m, _ := gitSetup(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter}) // open diff of a.txt
	if len(m.docs) != 1 || !m.docs[0].virtual {
		t.Fatalf("expected one virtual doc, got %+v", m.docs)
	}
	v := frame(m)
	if !strings.Contains(v, "+two") || !strings.Contains(v, "a.txt (diff)") {
		t.Fatalf("diff tab wrong:\n%s", v)
	}
	before := m.doc().ed.Buf.Len()
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.doc().ed.Buf.Len() != before {
		t.Fatal("read-only diff buffer was mutated")
	}
	if got := m.doc().save(); got != "read-only view" {
		t.Fatalf("save on virtual doc = %q", got)
	}
}

func TestGitToggleRefocusesBeforeClosing(t *testing.T) {
	m, _ := gitSetup(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter}) // diff tab steals focus
	if m.focus != paneEditor {
		t.Fatalf("focus = %v, want editor", m.focus)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlG}) // reclaim, not close
	if m.focus != paneGit || !m.gitView {
		t.Fatalf("focus = %v gitView = %v, want git focus with panel open", m.focus, m.gitView)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlG}) // focused: now it closes
	if m.gitView || m.sidebarOpen {
		t.Fatal("second toggle should close the panel")
	}
}

func TestSidebarToggleTriState(t *testing.T) {
	m, _ := gitSetup(t)                             // git panel open + focused
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlB}) // git view → file tree, focused
	if m.gitView || !m.sidebarOpen || m.focus != paneSidebar {
		t.Fatalf("want focused tree, got gitView=%v open=%v focus=%v", m.gitView, m.sidebarOpen, m.focus)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlB}) // focused: closes
	if m.sidebarOpen {
		t.Fatal("second toggle should close")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlB}) // reopens focused
	if !m.sidebarOpen || m.focus != paneSidebar {
		t.Fatalf("want reopened+focused, got open=%v focus=%v", m.sidebarOpen, m.focus)
	}
}

func TestGitCommitFlow(t *testing.T) {
	m, _ := gitSetup(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeySpace}) // stage
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.mode != modePrompt {
		t.Fatal("commit key did not open the prompt")
	}
	for _, r := range "a very long commit message that would previously wrap the footer onto a second row and break the layout" {
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	v := frame(m)
	// The long `git commit` output in lastMsg must not wrap the footer.
	if lines := strings.Split(v, "\n"); len(lines) != 24 {
		t.Fatalf("frame is %d lines, want 24:\n%s", len(lines), v)
	}
	if strings.Contains(v, "Staged (") {
		t.Fatalf("commit left staged files:\n%s", v)
	}
	if !strings.Contains(v, "no changes") {
		t.Fatalf("panel not empty after commit:\n%s", v)
	}
}

func TestGitMouseClickOpensDiff(t *testing.T) {
	m, _ := gitSetup(t)
	// Row layout: y=0 tabs, y=1 panel header, y=2 "Changes (1)", y=3 file.
	m, _ = m.update(tea.MouseMsg{X: 10, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(m.docs) != 1 || !m.docs[0].virtual {
		t.Fatalf("click did not open a diff tab: %+v", m.docs)
	}
}

func TestGitBranchPicker(t *testing.T) {
	m, _ := gitSetup(t)
	// New branch via prompt.
	act := m.reg.ByID("git.branchNew")
	act.Do(&m)
	for _, r := range "feature" {
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(frame(m), "Git: feature") {
		t.Fatalf("branch create failed:\n%s", frame(m))
	}
	// Switch back through the picker overlay.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if m.ovKind != overlayBranches {
		t.Fatal("picker did not open")
	}
	for _, r := range "main" {
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(frame(m), "⎇ main") {
		t.Fatalf("checkout failed:\n%s", frame(m))
	}
}
