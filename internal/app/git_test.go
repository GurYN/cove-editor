package app

import (
	"fmt"
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

func TestGitGutterSigns(t *testing.T) {
	m, top := gitSetup(t) // a.txt committed as "one\n", now "one\ntwo\n"
	m.openFile(filepath.Join(top, "a.txt"))
	d := m.doc()
	if d == nil || len(d.ed.Signs) < 2 {
		t.Fatalf("no signs computed: %+v", d)
	}
	if d.ed.Signs[0] != 0 || d.ed.Signs[1] != 'a' {
		t.Fatalf("signs = %q, want line 2 added", d.ed.Signs)
	}
	if !strings.Contains(frame(m), "▎  2 two") {
		t.Fatalf("gutter bar missing:\n%s", frame(m))
	}
	// Type on line 1, flush the debounce: line 1 becomes modified.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m, _ = m.update(changeTickMsg{})
	if s := m.doc().ed.Signs; s[0] != 'm' {
		t.Fatalf("signs after edit = %q, want line 1 modified", s)
	}
	// Save + commit everything moves HEAD: signs must clear.
	m.doc().save()
	act := m.reg.ByID("git.stageAll")
	act.Do(&m)
	m.gitCommitPrompt()
	for _, r := range "wip" {
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, s := range m.doc().ed.Signs {
		if s != 0 {
			t.Fatalf("signs after commit = %q, want none", m.doc().ed.Signs)
		}
	}
}

func TestGitInlineBlame(t *testing.T) {
	m, top := gitSetup(t)
	m.openFile(filepath.Join(top, "a.txt"))
	m.reg.ByID("git.blame").Do(&m)
	cmd := m.blameCmdIfNeeded() // what the Update choke point schedules
	if cmd == nil {
		t.Fatal("no blame fetch scheduled")
	}
	m, _ = m.update(cmd().(blameMsg))
	// Line 1 is committed: author + summary in the status bar.
	if v := frame(m); !strings.Contains(v, "t, just now · init") {
		t.Fatalf("blame annotation missing:\n%s", v)
	}
	// Line 2 is the uncommitted edit.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyDown})
	if v := frame(m); !strings.Contains(v, "uncommitted changes") {
		t.Fatalf("uncommitted annotation missing:\n%s", v)
	}
	// A transient message outranks blame.
	m.lastMsg = "saved"
	if v := frame(m); strings.Contains(v, "uncommitted changes") {
		t.Fatalf("lastMsg should win the slot:\n%s", v)
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

// Enter on a branch name that matches nothing offers to create it off the
// current branch; "y" runs checkout -b.
func TestBranchPickerCreatesMissingBranch(t *testing.T) {
	m, top := gitSetup(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}}) // panel: open branch picker
	if m.ovKind != overlayBranches {
		t.Fatal("branch picker did not open")
	}
	for _, r := range "feature/x" {
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modePrompt || !strings.Contains(m.promptLabel, `"feature/x"`) ||
		!strings.Contains(m.promptLabel, "main") {
		t.Fatalf("no create prompt (mode=%v label=%q)", m.mode, m.promptLabel)
	}
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'y'}}, {Type: tea.KeyEnter}} {
		m, _ = m.update(k)
	}
	if m.gitSnap.Branch != "feature/x" {
		t.Fatalf("on branch %q, want feature/x", m.gitSnap.Branch)
	}
	_ = top

	// Escape (or answering n) must not create anything.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	for _, r := range "nope" {
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEscape})
	if m.mode == modePrompt {
		t.Fatal("escape from the picker should not prompt")
	}
}

// The footer summarizes push/pull results instead of echoing git's chatter.
func TestGitOpStatusMessages(t *testing.T) {
	m, _ := gitSetup(t)
	for _, tc := range []struct{ op, out, want string }{
		{"push", "remote:\nremote: Create a pull request\nbranch 'develop' set up to track 'origin/develop'.", "published branch main to origin"},
		{"push", "To /tmp/bare\n   abc..def  main -> main", "pushed main"},
		{"push", "Everything up-to-date", "already up to date"},
		{"pull", "Updating abc..def\nFast-forward", "pulled main"},
		{"pull", "Already up to date.", "already up to date"},
	} {
		mm, _ := m.handleGitOp(gitOpMsg{op: tc.op, out: tc.out})
		if mm.lastMsg != tc.want {
			t.Fatalf("%s %q: got %q, want %q", tc.op, tc.out, mm.lastMsg, tc.want)
		}
	}
	mm, _ := m.handleGitOp(gitOpMsg{op: "push", err: fmt.Errorf("git: boom")})
	if mm.lastMsg != "git: boom" {
		t.Fatalf("error message lost: %q", mm.lastMsg)
	}
}

// "x" in the git panel discards the selected file's changes after a y/n
// confirm: tracked files restore to HEAD (open tabs reload, undoably),
// untracked files are deleted.
func TestGitRestoreFile(t *testing.T) {
	m, top := gitSetup(t) // a.txt committed as "one\n", now "one\ntwo\n"
	m.openFile(filepath.Join(top, "a.txt"))
	m.focus = paneGit
	for range 5 { // land on the file row (rows include section headers)
		if _, ok := m.gitSelected(); ok {
			break
		}
		m.gitMove(+1)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.mode != modePrompt || !strings.Contains(m.promptLabel, "Discard changes to a.txt") {
		t.Fatalf("no confirm prompt: mode=%v label=%q", m.mode, m.promptLabel)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if data, _ := os.ReadFile(filepath.Join(top, "a.txt")); string(data) != "one\n" {
		t.Fatalf("file not restored: %q", data)
	}
	d := m.docByPath(filepath.Join(top, "a.txt"))
	if got := string(d.ed.Buf.Bytes()); got != "one\n" {
		t.Fatalf("open tab not reloaded: %q", got)
	}
	if d.ed.Dirty {
		t.Fatal("reloaded tab marked dirty")
	}
	d.ed.UndoStep() // the discard is one undoable edit
	if got := string(d.ed.Buf.Bytes()); got != "one\ntwo\n" {
		t.Fatalf("undo did not bring the change back: %q", got)
	}

	// Untracked file: "x" prompts to delete it.
	os.WriteFile(filepath.Join(top, "new.txt"), []byte("x\n"), 0o644)
	m.refreshGit()
	for range 9 {
		r, ok := m.gitSelected()
		if ok && r.fs.Untracked() {
			break
		}
		m.gitMove(+1)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !strings.Contains(m.promptLabel, "Delete untracked") {
		t.Fatalf("wrong prompt for untracked: %q", m.promptLabel)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(filepath.Join(top, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file survived the delete")
	}
}
