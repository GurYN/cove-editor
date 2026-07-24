package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if m.focus != paneGit || !m.git.view {
		t.Fatalf("focus = %v gitView = %v, want git focus with panel open", m.focus, m.git.view)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlG}) // focused: now it closes
	if m.git.view || m.sidebarOpen {
		t.Fatal("second toggle should close the panel")
	}
}

func TestSidebarToggleTriState(t *testing.T) {
	m, _ := gitSetup(t)                             // git panel open + focused
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlB}) // git view → file tree, focused
	if m.git.view || !m.sidebarOpen || m.focus != paneSidebar {
		t.Fatalf("want focused tree, got gitView=%v open=%v focus=%v", m.git.view, m.sidebarOpen, m.focus)
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
	// Create and switch fetch first (async) — pump the cmd loop through it.
	m2, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = pump(t, m2, cmd, func(m Model) bool { return strings.Contains(frame(m), "Git: feature") }, 5*time.Second).(Model)
	// Switch back through the picker overlay.
	m2, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = pump(t, m2, cmd, func(m Model) bool { return m.ovKind == overlayBranches }, 5*time.Second).(Model)
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
	m2, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}}) // panel: open branch picker (fetch first)
	m = pump(t, m2, cmd, func(m Model) bool { return m.ovKind == overlayBranches }, 5*time.Second).(Model)
	for _, r := range "feature/x" {
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modePrompt || !strings.Contains(m.promptLabel, `"feature/x"`) ||
		!strings.Contains(m.promptLabel, "main") {
		t.Fatalf("no create prompt (mode=%v label=%q)", m.mode, m.promptLabel)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m2, cmd = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = pump(t, m2, cmd, func(m Model) bool { return m.git.repos[0].snap.Branch == "feature/x" }, 5*time.Second).(Model)
	_ = top

	// Escape (or answering n) must not create anything. (Closing the overlay
	// moved focus to the editor — refocus the panel as a user would.)
	m.focus = paneGit
	m2, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = pump(t, m2, cmd, func(m Model) bool { return m.ovKind == overlayBranches }, 5*time.Second).(Model)
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
		{"fetch", "From /tmp/bare\n   abc..def  main -> origin/main", "fetched"},
	} {
		mm, _ := m.handleGitOp(gitOpMsg{repo: m.git.repos[0], op: tc.op, out: tc.out})
		if mm.lastMsg != tc.want {
			t.Fatalf("%s %q: got %q, want %q", tc.op, tc.out, mm.lastMsg, tc.want)
		}
	}
	mm, _ := m.handleGitOp(gitOpMsg{repo: m.git.repos[0], op: "push", err: fmt.Errorf("git: boom")})
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
		if _, ok := m.git.selected(); ok {
			break
		}
		m.git.move(+1, m.gitHeight())
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
		r, ok := m.git.selected()
		if ok && r.fs.Untracked() {
			break
		}
		m.git.move(+1, m.gitHeight())
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

// "r" refreshes local status and fetches, so behind/ahead counts track the
// real remote (a PR merged on the forge shows up without a pull).
func TestRefreshFetchesRemote(t *testing.T) {
	m, top := gitSetup(t)
	bare := t.TempDir()
	g := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// -b main: without it the bare repo's HEAD points at the host git's
	// init.defaultBranch (master on CI), and the clone below checks out
	// nothing — its push then never advances main.
	g(bare, "init", "-q", "--bare", "-b", "main")
	g(top, "remote", "add", "origin", bare)
	g(top, "push", "-q", "-u", "origin", "main")

	// Second clone advances the remote's main.
	w2 := t.TempDir()
	g(w2, "clone", "-q", bare, "w2")
	w2 = filepath.Join(w2, "w2")
	g(w2, "config", "user.email", "t@t.t")
	g(w2, "config", "user.name", "t")
	os.WriteFile(filepath.Join(w2, "b.txt"), []byte("hi\n"), 0o644)
	g(w2, "add", "-A")
	g(w2, "commit", "-q", "-m", "remote work")
	g(w2, "push", "-q")

	m.refreshGit()
	if m.git.repos[0].snap.Behind != 0 {
		t.Fatalf("stale tracking ref should still say behind=0, got %d", m.git.repos[0].snap.Behind)
	}
	m2, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("refresh returned no fetch cmd")
	}
	m2, _ = m2.update(cmd()) // run the fetch, deliver its gitOpMsg
	if m2.git.repos[0].snap.Behind != 1 {
		t.Fatalf("behind=%d after fetch, want 1", m2.git.repos[0].snap.Behind)
	}
	if m2.git.busy != "" {
		t.Fatalf("gitBusy stuck at %q", m2.git.busy)
	}
}

func TestGitGraphTabAndDrillIn(t *testing.T) {
	m, _ := gitSetup(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}}) // graph from panel
	if len(m.docs) != 1 || !m.docs[0].virtual || m.docs[0].path != gitGraphTitle {
		t.Fatalf("expected graph tab, got %+v", m.docs)
	}
	v := frame(m)
	if !strings.Contains(v, "●") || !strings.Contains(v, "init") {
		t.Fatalf("graph tab missing content:\n%s", v)
	}

	// Enter on the first graph line opens that commit's diff tab.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.docs) != 2 || !strings.HasSuffix(m.docs[1].path, " (commit)") {
		t.Fatalf("drill-in failed, docs = %+v", m.docs)
	}
	v = frame(m)
	if !strings.Contains(v, "init") || !strings.Contains(v, "+one") {
		t.Fatalf("commit tab wrong:\n%s", v)
	}
}

// multiSetup builds a non-repo root holding two child repos (alpha, beta),
// each with a committed-then-modified file, and opens the git panel.
func multiSetup(t *testing.T) (Model, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, name)
		os.MkdirAll(dir, 0o755)
		g := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		g("init", "-q", "-b", "main")
		g("config", "user.email", "t@t.t")
		g("config", "user.name", "t")
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte("one\n"), 0o644)
		g("add", "-A")
		g("commit", "-q", "-m", "init")
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte("one\ntwo\n"), 0o644)
	}
	os.WriteFile(filepath.Join(root, "README.md"), []byte("root file\n"), 0o644)

	m := New(root, nil)
	m, _ = m.update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlG})
	return m, root
}

// A folder holding several checkouts renders one section group per repo,
// and panel actions target the repo under the cursor — never its neighbor.
func TestGitMultiRepoPanel(t *testing.T) {
	m, root := multiSetup(t)
	if len(m.git.repos) != 2 {
		t.Fatalf("discovered %d repos, want 2", len(m.git.repos))
	}
	v := frame(m)
	if !strings.Contains(v, "alpha · main") || !strings.Contains(v, "beta · main") {
		t.Fatalf("missing repo headers:\n%s", v)
	}

	// Cursor starts on alpha's file: stage-all must touch only alpha.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if m.lastMsg != "alpha: staged all changes" {
		t.Fatalf("toast = %q", m.lastMsg)
	}
	alpha, beta := m.git.repos[0], m.git.repos[1]
	if !gitHasStaged(alpha) || gitHasStaged(beta) {
		t.Fatalf("stage-all leaked across repos: alpha=%v beta=%v",
			gitHasStaged(alpha), gitHasStaged(beta))
	}

	// Move to beta's row: the commit prompt must name its target.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyDown})
	if r := m.curRepo(); r != beta {
		t.Fatalf("cursor repo = %+v, want beta", r)
	}
	if !strings.Contains(m.gitSeg(), "beta:main") {
		t.Fatalf("status segment = %q, want beta:main", m.gitSeg())
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.lastMsg != "beta: nothing staged — stage files first (space in the git panel)" {
		t.Fatalf("commit targeted wrong repo: %q", m.lastMsg)
	}

	// Editor focus + a file from alpha: global actions follow the file.
	m.openFile(filepath.Join(root, "alpha", "alpha.txt"))
	if r := m.curRepo(); r != alpha {
		t.Fatalf("active-file repo = %+v, want alpha", r)
	}
	if d := m.doc(); d == nil || len(d.ed.Signs) < 2 {
		t.Fatal("gutter signs not computed for a child-repo file")
	}

	// A root file belongs to no repo: an ambiguous action pops the picker.
	m.openFile(filepath.Join(root, "README.md"))
	if r := m.curRepo(); r != nil {
		t.Fatalf("root file resolved to %v, want no repo", r.name)
	}
	if cmd := m.gitOp("push"); cmd != nil || m.ovKind != overlayRepos {
		t.Fatalf("ambiguous push should open the repo picker, got kind=%v", m.ovKind)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEscape}) // dismiss
	if m.ovRepoDo != nil {
		t.Fatal("escape left a pending repo action")
	}
}

// git.sync rebases the current branch onto the picked one; --autostash
// carries the dirty a.txt across the rebase.
func TestGitSyncRebase(t *testing.T) {
	m, top := gitSetup(t)
	g := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = top
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// feature forks at init; main gains b.txt; feature keeps the dirty a.txt.
	g("branch", "feature")
	os.WriteFile(filepath.Join(top, "b.txt"), []byte("b\n"), 0o644)
	g("add", "b.txt")
	g("commit", "-q", "-m", "on main")
	g("checkout", "-q", "feature")
	m.refreshGit() // branch moved under the app's feet

	m2, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // panel: sync picker (fetch first)
	m = pump(t, m2, cmd, func(m Model) bool { return m.ovKind == overlaySync }, 5*time.Second).(Model)
	for _, r := range "main" {
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m2, cmd = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = pump(t, m2, cmd, func(m Model) bool {
		return strings.Contains(m.lastMsg, "rebased feature onto main")
	}, 5*time.Second).(Model)

	if _, err := os.Stat(filepath.Join(top, "b.txt")); err != nil {
		t.Fatalf("main's commit missing after rebase: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(top, "a.txt")); string(data) != "one\ntwo\n" {
		t.Fatalf("autostash lost the dirty edit: %q", data)
	}
}

// git.stash shelves the dirty edit (a.txt back to HEAD), git.stashPop
// restores it; popping an empty stash reports instead of erroring raw.
func TestGitStashAndPop(t *testing.T) {
	m, top := gitSetup(t) // a.txt committed as "one\n", dirty as "one\ntwo\n"
	stashed := func(m Model) bool {
		data, _ := os.ReadFile(filepath.Join(top, "a.txt"))
		return string(data) == "one\n"
	}
	cmd := m.reg.ByID("git.stash").Do(&m)
	m = pump(t, m, cmd, stashed, 5*time.Second).(Model)
	if !strings.Contains(m.lastMsg, "stashed") {
		t.Fatalf("lastMsg = %q", m.lastMsg)
	}
	cmd = m.reg.ByID("git.stashPop").Do(&m)
	m = pump(t, m, cmd, func(m Model) bool { return !stashed(m) }, 5*time.Second).(Model)
	if !strings.Contains(m.lastMsg, "stash popped") {
		t.Fatalf("lastMsg = %q", m.lastMsg)
	}
	cmd = m.reg.ByID("git.stashPop").Do(&m)
	pump(t, m, cmd, func(m Model) bool {
		return strings.Contains(m.lastMsg, "no stash to pop")
	}, 5*time.Second)
}

// git.stashFile shelves only the selected row; other dirty files stay put.
func TestGitStashSelectedFile(t *testing.T) {
	m, top := gitSetup(t) // a.txt dirty
	os.WriteFile(filepath.Join(top, "b.txt"), []byte("b\n"), 0o644)
	m.refreshGit()
	for i, r := range m.git.rows { // select the a.txt row
		if r.header == "" && r.fs.Path == "a.txt" {
			m.git.sel = i
			break
		}
	}
	m.reg.ByID("git.stashFile").Do(&m)
	if data, _ := os.ReadFile(filepath.Join(top, "a.txt")); string(data) != "one\n" {
		t.Fatalf("a.txt not stashed: %q", data)
	}
	if _, err := os.Stat(filepath.Join(top, "b.txt")); err != nil {
		t.Fatalf("b.txt should be untouched: %v", err)
	}
	cmd := m.reg.ByID("git.stashPop").Do(&m)
	pump(t, m, cmd, func(m Model) bool {
		data, _ := os.ReadFile(filepath.Join(top, "a.txt"))
		return string(data) == "one\ntwo\n"
	}, 5*time.Second)
}

// git.amend folds staged changes into HEAD; keeping the pre-filled subject
// preserves a multi-line message via --no-edit.
func TestGitAmend(t *testing.T) {
	m, top := gitSetup(t)
	g := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = top
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	gOut := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = top
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	g("commit", "-qam", "subject\n\nbody line") // commit the dirty a.txt, multi-line msg
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	g("add", "-A")
	m.refreshGit()

	m.reg.ByID("git.amend").Do(&m) // prompt pre-filled with "subject"
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := gOut("rev-list", "--count", "HEAD"); got != "2" {
		t.Fatalf("commit count = %s, want 2 (amend must not add one)", got)
	}
	if got := gOut("log", "-1", "--format=%B"); !strings.Contains(got, "body line") {
		t.Fatalf("unchanged subject should keep the body, got %q", got)
	}
	// Reword: type a new message over the pre-fill.
	m.reg.ByID("git.amend").Do(&m)
	m.promptText = "new subject"
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := gOut("log", "-1", "--format=%s"); got != "new subject" {
		t.Fatalf("reword failed: %q", got)
	}
}

// A push rejected because local history was rewritten (diverged from
// upstream) offers a --force-with-lease push instead of the "pull first"
// advice, which would re-merge the old commits.
func TestGitPushForceAfterDivergence(t *testing.T) {
	m, top := gitSetup(t)
	g := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = top
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v: %s", err, out)
	}
	g("remote", "add", "origin", bare)
	g("commit", "-qam", "second")
	g("push", "-qu", "origin", "main")
	g("commit", "-q", "--amend", "-m", "second, rewritten") // diverge from origin/main
	m.refreshGit()

	cmd := m.reg.ByID("git.push").Do(&m)
	m = pump(t, m, cmd, func(m Model) bool { return m.mode == modePrompt }, 5*time.Second).(Model)
	if !strings.Contains(m.promptLabel, "Force push") {
		t.Fatalf("expected force-push offer, got %q", m.promptLabel)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m2, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = pump(t, m2, cmd, func(m Model) bool { return strings.Contains(m.lastMsg, "force-pushed main") }, 5*time.Second).(Model)
	if m.git.repos[0].snap.Ahead != 0 || m.git.repos[0].snap.Behind != 0 {
		t.Fatalf("still diverged after force push: +%d -%d", m.git.repos[0].snap.Ahead, m.git.repos[0].snap.Behind)
	}
}

func TestGitPanelTreeView(t *testing.T) {
	m, top := gitSetup(t)
	os.MkdirAll(filepath.Join(top, "pkg", "sub"), 0o755)
	os.WriteFile(filepath.Join(top, "pkg", "b.txt"), []byte("b\n"), 0o644)
	os.WriteFile(filepath.Join(top, "pkg", "sub", "c.txt"), []byte("c\n"), 0o644)

	m.git.tree = true
	m.refreshGit()
	v := frame(m)
	for _, want := range []string{"▾ pkg/", "  ▾ sub/", "U   b.txt", "U     c.txt", "M a.txt"} {
		if !strings.Contains(v, want) {
			t.Fatalf("tree view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "pkg/b.txt") {
		t.Fatalf("tree view still shows full paths:\n%s", v)
	}
	// selection starts on a file row; dir rows never satisfy selected()
	if r, ok := m.git.selected(); !ok || r.fs.Path == "" {
		t.Fatalf("selection not on a file row: %+v", r)
	}

	// Collapse pkg/: move onto the dir row, Enter folds everything under it.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyDown}) // a.txt → pkg/
	if r := m.git.rows[m.git.sel]; r.dir != "pkg" {
		t.Fatalf("cursor row = %+v, want dir pkg", r)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	v = frame(m)
	if !strings.Contains(v, "▸ pkg/") || strings.Contains(v, "b.txt") || strings.Contains(v, "sub/") {
		t.Fatalf("collapse failed:\n%s", v)
	}
	if r := m.git.rows[m.git.sel]; r.dir != "pkg" {
		t.Fatalf("cursor left the toggled dir: %+v", r)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter}) // expand again
	if v = frame(m); !strings.Contains(v, "▾ pkg/") || !strings.Contains(v, "U   b.txt") {
		t.Fatalf("expand failed:\n%s", v)
	}
	// Collapse survives a refresh.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m.refreshGit()
	if v = frame(m); !strings.Contains(v, "▸ pkg/") || strings.Contains(v, "b.txt") {
		t.Fatalf("collapse lost on refresh:\n%s", v)
	}

	// [git] view = "tree" wires through config
	cfg := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfg, []byte("[git]\nview = \"tree\"\n"), 0o644)
	t.Setenv("COVE_CONFIG", cfg)
	m2 := New(top, nil)
	if !m2.git.tree {
		t.Fatal("config [git] view=tree not applied")
	}
}

func TestGitPanelFlatDefault(t *testing.T) {
	m, _ := gitSetup(t)
	if m.git.tree {
		t.Fatal("tree view must be opt-in; default is flat")
	}
}

func TestGitPanelOpenFile(t *testing.T) {
	m, top := gitSetup(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if m.focus != paneEditor {
		t.Fatalf("focus = %v, want editor", m.focus)
	}
	d := m.doc()
	if d == nil || d.virtual || !strings.HasSuffix(d.path, "a.txt") {
		t.Fatalf("expected a.txt open as a real tab, got %+v", d)
	}
	if !strings.Contains(string(d.ed.Buf.Bytes()), "two") {
		t.Fatal("opened buffer is not the working copy")
	}
	_ = top
}

func TestGitClickKeepsPanelFocus(t *testing.T) {
	m, _ := gitSetup(t)
	// Click the file row: diff preview opens but the panel keeps focus…
	m, _ = m.update(tea.MouseMsg{X: 10, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(m.docs) != 1 || !m.docs[0].virtual {
		t.Fatalf("click did not open the diff preview: %+v", m.docs)
	}
	if m.focus != paneGit {
		t.Fatalf("focus = %v, want git panel", m.focus)
	}
	// …so panel shortcuts still work: 'a' stages everything.
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if v := frame(m); !strings.Contains(v, "Staged (1)") {
		t.Fatalf("'a' after click did not stage:\n%s", v)
	}
}
