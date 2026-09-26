package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/git"
)

// a.go as committed: A and B far enough apart that edits to each are
// separate hunks under git's 3-line context.
const aOrig = "package a\n\nfunc A() {}\n\n// 1\n// 2\n// 3\n// 4\n// 5\n// 6\n// 7\n\nfunc B() {}\n"

// reviewRepo makes a committed repo with a.go and b.go and a Model on it.
func reviewRepo(t *testing.T, cfg string) (Model, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	g := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	g("init", "-q", "-b", "main")
	g("config", "user.email", "t@t.t")
	g("config", "user.name", "t")
	os.WriteFile(filepath.Join(root, "a.go"), []byte(aOrig), 0o644)
	os.WriteFile(filepath.Join(root, "b.go"), []byte("package a\n\nvar X = 1\n"), 0o644)
	g("add", "-A")
	g("commit", "-q", "-m", "init")
	real, _ := filepath.EvalSymlinks(root)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte(cfg), 0o644)
	t.Setenv("COVE_CONFIG", cfgPath)
	m := New(real, nil)
	m.width, m.height = 120, 40
	m.layout()
	return m, real
}

// checkpointNow runs the checkpoint command synchronously.
func checkpointNow(t *testing.T, m *Model) {
	t.Helper()
	cmd := m.reviewCheckpointCmd()
	if cmd == nil {
		t.Fatalf("no checkpoint cmd: %q", m.lastMsg)
	}
	m.handleCheckpoint(cmd().(checkpointMsg))
	if !m.review.active() {
		t.Fatalf("checkpoint failed: %q", m.lastMsg)
	}
}

// refreshNow runs one review refresh synchronously.
func refreshNow(t *testing.T, m *Model) {
	t.Helper()
	cmd := m.reviewRefreshCmd()
	if cmd == nil {
		t.Fatal("no refresh cmd")
	}
	m.handleReviewMsg(cmd().(reviewMsg))
}

// TestRevertHunk: the edit that puts a hunk's old lines back — a modified
// block, a pure deletion (insert after a line), a change at EOF without a
// trailing newline — and a refusal when the file no longer matches.
func TestRevertHunk(t *testing.T) {
	data := []byte("a\nB\nc\nd\n")
	h := git.Hunk{OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 3, Old: []string{"a", "b", "c"}, New: []string{"a", "B", "c"}}
	off, end, repl, err := revertHunk(data, h)
	if err != nil || off != 0 || end != 6 || string(repl) != "a\nb\nc\n" {
		t.Fatalf("modified: %d %d %q %v", off, end, repl, err)
	}
	// Deletion of "x" after line 1: the diff says @@ -2 +1,0 @@ -x
	data = []byte("a\nb\n")
	h = git.Hunk{OldStart: 2, OldLines: 1, NewStart: 1, NewLines: 0, Old: []string{"x"}}
	off, end, repl, err = revertHunk(data, h)
	if err != nil || off != 2 || end != 2 || string(repl) != "x\n" {
		t.Fatalf("deletion: %d %d %q %v", off, end, repl, err)
	}
	// Last line changed, no newline at EOF on either side.
	data = []byte("a\nNEW")
	h = git.Hunk{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 2, Old: []string{"a", "old"}, New: []string{"a", "NEW"}, OldNoNL: true, NewNoNL: true}
	off, end, repl, err = revertHunk(data, h)
	if err != nil || off != 0 || end != 5 || string(repl) != "a\nold" {
		t.Fatalf("eof: %d %d %q %v", off, end, repl, err)
	}
	// Content moved on: refuse rather than clobber.
	if _, _, _, err := revertHunk([]byte("a\nZ\nc\nd\n"), git.Hunk{NewStart: 1, NewLines: 3, Old: []string{"a"}, New: []string{"a", "B", "c"}}); err == nil {
		t.Fatal("mismatch accepted")
	}
	if _, _, _, err := revertHunk([]byte("a\n"), git.Hunk{NewStart: 5, NewLines: 1, New: []string{"q"}}); err == nil {
		t.Fatal("out-of-range accepted")
	}
}

// TestReviewFlow walks the panel end to end: checkpoint, agent edits on
// disk (modify, create, delete), rows grouped by file, revert on a closed
// file (disk) and an open one (undoable buffer edit, saved), accept hiding
// a hunk across refreshes, the gutter baseline following the checkpoint,
// and accept-all starting a fresh review.
func TestReviewFlow(t *testing.T) {
	m, root := reviewRepo(t, "")
	checkpointNow(t, &m)
	refreshNow(t, &m)
	if len(m.review.rows) != 0 {
		t.Fatalf("clean tree has rows: %+v", m.review.rows)
	}

	// The agent works.
	aEdited := strings.Replace(aOrig, "func A() {}", "func A() { println(1) }", 1) + "\nfunc C() {}\n"
	os.WriteFile(filepath.Join(root, "a.go"), []byte(aEdited), 0o644)
	os.WriteFile(filepath.Join(root, "new.go"), []byte("package a\n"), 0o644)
	os.Remove(filepath.Join(root, "b.go"))
	refreshNow(t, &m)
	p := &m.review
	if len(p.files) != 3 || p.files[0].rel != "a.go" || p.files[1].rel != "b.go" || p.files[2].rel != "new.go" {
		t.Fatalf("files = %+v", p.files)
	}
	if p.files[1].diff.Status != 'D' || p.files[2].diff.Status != 'A' || len(p.files[0].diff.Hunks) != 2 {
		t.Fatalf("statuses/hunks: %+v", p.files)
	}
	if p.hunkCount() != 4 || p.rows[p.sel].hunk != 0 || p.rows[p.sel].file != 0 {
		t.Fatalf("rows = %+v sel = %d", p.rows, p.sel)
	}
	view := m.reviewPanelView()
	if !strings.Contains(view, "3 file(s) · 4 hunk") || !strings.Contains(view, "M a.go") || !strings.Contains(view, "deleted") || !strings.Contains(view, "new file") {
		t.Fatalf("view:\n%s", view)
	}

	// Revert the first hunk of a.go while it's closed: a disk write.
	if cmd := m.reviewRevertSel(); cmd == nil {
		t.Fatalf("revert: %q", m.lastMsg)
	} else {
		m.handleReviewMsg(cmd().(reviewMsg))
	}
	disk, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(disk) != aOrig+"\nfunc C() {}\n" {
		t.Fatalf("disk after revert:\n%s", disk)
	}
	if len(p.files[0].diff.Hunks) != 1 || p.files[0].diff.Hunks[0].Added != 2 {
		t.Fatalf("a.go after revert: %+v", p.files[0].diff)
	}

	// Open a.go: the gutter baseline is the checkpoint, so only C shows as added.
	m.reviewOpenSel(true)
	d := m.doc()
	if d == nil || !same(d.path, filepath.Join(root, "a.go")) || m.focus != paneEditor {
		t.Fatalf("open: doc=%v focus=%d", d, m.focus)
	}
	if line, _ := d.ed.Cursor(); line != 13 { // the blank line before func C, first added line
		t.Fatalf("cursor line = %d, want 13 (hunk %+v)", line, p.files[0].diff.Hunks[0])
	}
	if string(d.head) != aOrig {
		t.Fatalf("baseline = %q", d.head)
	}
	added := 0
	for _, s := range d.ed.Signs {
		if s == 'a' {
			added++
		}
	}
	if added != 2 {
		t.Fatalf("signs = %q", d.ed.Signs)
	}

	// Revert the remaining hunk with the file open: buffer edit, saved, undoable.
	m.focus = paneReview
	if cmd := m.reviewRevertSel(); cmd == nil {
		t.Fatalf("revert open: %q", m.lastMsg)
	} else {
		m.handleReviewMsg(cmd().(reviewMsg))
	}
	if string(d.ed.Buf.Bytes()) != aOrig || d.ed.Dirty {
		t.Fatalf("buffer after revert: %q dirty=%v", d.ed.Buf.Bytes(), d.ed.Dirty)
	}
	d.ed.UndoStep()
	if !strings.Contains(string(d.ed.Buf.Bytes()), "func C()") {
		t.Fatal("revert not undoable")
	}
	d.ed.RedoStep()
	if len(p.files) != 2 || p.files[0].rel != "b.go" {
		t.Fatalf("files after reverts: %+v", p.files)
	}

	// Accept the deletion of b.go: hidden now and after a refresh.
	m.reviewAcceptSel()
	if len(p.files) != 1 || p.files[0].rel != "new.go" {
		t.Fatalf("after accept: %+v", p.files)
	}
	refreshNow(t, &m)
	if len(p.files) != 1 || p.files[0].rel != "new.go" {
		t.Fatalf("accepted came back: %+v", p.files)
	}

	// Revert the new file: asks first, then deletes.
	if cmd := m.reviewRevertSel(); cmd != nil || m.mode != modePrompt {
		t.Fatalf("delete should prompt: cmd=%v mode=%v", cmd, m.mode)
	}
	var after tea.Cmd
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("y")}, {Type: tea.KeyEnter}} {
		next, cmd := m.Update(k)
		m, after = next.(Model), cmd
	}
	if _, err := os.Stat(filepath.Join(root, "new.go")); !os.IsNotExist(err) {
		t.Fatal("new.go not deleted")
	}
	if after == nil {
		t.Fatal("no refresh queued after delete")
	}
	// The prompt returns the deferred refresh (possibly batched).
	var runMsg func(tea.Msg)
	runMsg = func(msg tea.Msg) {
		switch v := msg.(type) {
		case reviewMsg:
			m.handleReviewMsg(v)
		case tea.BatchMsg:
			for _, c := range v {
				if c != nil {
					runMsg(c())
				}
			}
		}
	}
	runMsg(after())
	if len(m.review.files) != 0 {
		t.Fatalf("files after delete: %+v", m.review.files)
	}

	// Accept all: new checkpoint, accepted set cleared, b.go's deletion is
	// now the baseline.
	sha := m.review.cps[root]
	checkpointNow(t, &m)
	if m.review.cps[root] == sha || len(m.review.accepted) != 0 {
		t.Fatal("accept-all did not start a fresh review")
	}
	refreshNow(t, &m)
	if len(m.review.files) != 0 {
		t.Fatalf("fresh review has files: %+v", m.review.files)
	}
	// Baseline back to HEAD for a.go once it left the change set.
	if string(d.head) != aOrig {
		t.Fatalf("baseline after review: %q", d.head)
	}
}

// TestReviewFollow: with follow on and focus in the terminal, the file the
// agent touched opens on its first hunk without moving focus; the next
// touched file replaces that tab when it's still clean; nothing happens
// while the user is typing in the editor.
func TestReviewFollow(t *testing.T) {
	m, root := reviewRepo(t, "[agent]\nfollow = true\n")
	if !m.review.follow {
		t.Fatal("follow not read from config")
	}
	checkpointNow(t, &m)
	m.focus = paneTerminal
	os.WriteFile(filepath.Join(root, "a.go"), []byte(strings.Replace(aOrig, "func A() {}", "func A() { println(1) }", 1)), 0o644)
	refreshNow(t, &m)
	if len(m.docs) != 1 || !same(m.docs[0].path, filepath.Join(root, "a.go")) || m.focus != paneTerminal {
		t.Fatalf("follow: docs=%d focus=%d", len(m.docs), m.focus)
	}
	if line, _ := m.docs[0].ed.Cursor(); line != 2 {
		t.Fatalf("cursor line = %d", line)
	}
	os.WriteFile(filepath.Join(root, "b.go"), []byte("package a\n\nvar X = 2\n"), 0o644)
	refreshNow(t, &m)
	if len(m.docs) != 1 || !same(m.docs[0].path, filepath.Join(root, "b.go")) {
		t.Fatalf("follow tab not reused: %+v", m.docs)
	}
	// The user starts editing: follow stays out of the way.
	m.focus = paneEditor
	os.WriteFile(filepath.Join(root, "a.go"), []byte(strings.Replace(aOrig, "func A() {}", "func A() { println(2) }", 1)), 0o644)
	refreshNow(t, &m)
	if len(m.docs) != 1 || !same(m.docs[0].path, filepath.Join(root, "b.go")) {
		t.Fatalf("follow acted while editing: %+v", m.docs)
	}
}

// TestAgentLaunchCheckpoints: sending to a not-yet-running agent takes the
// review baseline as part of the launch; a relaunch during a review with
// pending hunks keeps it.
func TestAgentLaunchCheckpoints(t *testing.T) {
	m, root := reviewRepo(t, "[apps.claude]\ncommand = [\"cat\"]\nagent = true\n")
	m.openFile(filepath.Join(root, "a.go"))
	// The plain palette entry / keybinding is the usual way in: it must
	// checkpoint too, not only the Agent: … actions.
	if cmd := m.reg.ByID("app.claude").Do(&m); cmd == nil {
		t.Skip("PTY unavailable")
	} else {
		defer m.terms[0].Close()
		msgs := make(chan tea.Msg, 8)
		for _, c := range cmd().(tea.BatchMsg) {
			if c != nil {
				go func() { msgs <- c() }()
			}
		}
		got := false
		for i := 0; i < 2 && !got; i++ {
			if cp, ok := (<-msgs).(checkpointMsg); ok {
				m.handleCheckpoint(cp)
				got = true
			}
		}
		if !got || !m.review.active() {
			t.Fatal("agent launch took no checkpoint")
		}
	}
	// Pending review: relaunching must not reset it.
	os.WriteFile(filepath.Join(root, "b.go"), []byte("package a\n\nvar X = 2\n"), 0o644)
	refreshNow(t, &m)
	if len(m.review.rows) == 0 {
		t.Fatal("setup: no pending rows")
	}
	m.terms[0].Close()
	m.terms = nil
	if cmd := m.agentLaunchCheckpoint(); cmd != nil {
		t.Fatal("relaunch reset a review with pending hunks")
	}
}

// TestReviewPreviewKeepsFocus: Space opens the hunk's file like Enter but
// leaves focus on the panel, so r/a apply to the next decision without an
// Alt+R round trip.
func TestReviewPreviewKeepsFocus(t *testing.T) {
	m, root := reviewRepo(t, "")
	checkpointNow(t, &m)
	os.WriteFile(filepath.Join(root, "a.go"), []byte(strings.Replace(aOrig, "func A() {}", "func A() { println(1) }", 1)), 0o644)
	refreshNow(t, &m)
	m.focus = paneReview
	m, _ = m.dispatchKey(tea.KeyMsg{Type: tea.KeySpace})
	if d := m.doc(); d == nil || !same(d.path, filepath.Join(root, "a.go")) {
		t.Fatalf("preview did not open the file: %v", m.doc())
	}
	if m.focus != paneReview {
		t.Fatalf("focus = %d, want the review panel", m.focus)
	}
	if cmd := m.reg.ByID("review.revert").Do(&m); cmd == nil {
		t.Fatalf("revert after preview: %q", m.lastMsg)
	}
}
