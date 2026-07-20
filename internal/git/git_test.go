package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo makes a repo with one committed file and returns its top.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	top := t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		if _, err := run(top, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	mustGit("init", "-q", "-b", "main")
	mustGit("config", "user.email", "t@t.t")
	mustGit("config", "user.name", "t")
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("one\n"), 0o644)
	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "init")
	// macOS: TempDir is a /var -> /private/var symlink; match git's view.
	real, _ := run(top, "rev-parse", "--show-toplevel")
	return real
}

func TestStatusStageCommitFlow(t *testing.T) {
	top := initRepo(t)

	os.WriteFile(filepath.Join(top, "a.txt"), []byte("one\ntwo\n"), 0o644)
	os.WriteFile(filepath.Join(top, "b.txt"), []byte("new\n"), 0o644)

	snap, err := Status(top)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Branch != "main" {
		t.Fatalf("branch = %q", snap.Branch)
	}
	if len(snap.Files) != 2 {
		t.Fatalf("files = %+v", snap.Files)
	}
	if f := snap.Files[0]; f.Path != "a.txt" || f.Work != 'M' || f.Staged() {
		t.Fatalf("a.txt = %+v", f)
	}
	if f := snap.Files[1]; f.Path != "b.txt" || !f.Untracked() {
		t.Fatalf("b.txt = %+v", f)
	}

	// Files inside an untracked directory must list individually, not as "dir/".
	os.MkdirAll(filepath.Join(top, "newdir"), 0o755)
	os.WriteFile(filepath.Join(top, "newdir", "c.txt"), []byte("c\n"), 0o644)
	snap, _ = Status(top)
	found := false
	for _, f := range snap.Files {
		if strings.HasSuffix(f.Path, "/") {
			t.Fatalf("collapsed dir entry: %+v", f)
		}
		if f.Path == "newdir/c.txt" && f.Untracked() {
			found = true
		}
	}
	if !found {
		t.Fatalf("newdir/c.txt missing: %+v", snap.Files)
	}
	os.RemoveAll(filepath.Join(top, "newdir"))

	d, err := Diff(top, "a.txt", false)
	if err != nil || !strings.Contains(d, "+two") {
		t.Fatalf("diff = %q, %v", d, err)
	}
	if u := DiffUntracked(top, "b.txt"); !strings.Contains(u, "+new") {
		t.Fatalf("untracked diff = %q", u)
	}

	if err := Stage(top, "a.txt"); err != nil {
		t.Fatal(err)
	}
	snap, _ = Status(top)
	if f := snap.Files[0]; f.Index != 'M' || !f.Staged() {
		t.Fatalf("after stage: %+v", f)
	}
	if err := Unstage(top, "a.txt"); err != nil {
		t.Fatal(err)
	}
	snap, _ = Status(top)
	if f := snap.Files[0]; f.Staged() {
		t.Fatalf("after unstage: %+v", f)
	}

	if err := StageAll(top); err != nil {
		t.Fatal(err)
	}
	if _, err := Commit(top, "second"); err != nil {
		t.Fatal(err)
	}
	snap, _ = Status(top)
	if len(snap.Files) != 0 {
		t.Fatalf("after commit: %+v", snap.Files)
	}
}

// Non-ASCII filenames must round-trip: git's default core.quotePath would
// C-quote them ("caf\303\251.go") and every later pathspec would miss.
func TestStatusNonASCIIPath(t *testing.T) {
	top := initRepo(t)
	os.WriteFile(filepath.Join(top, "café.go"), []byte("x\n"), 0o644)
	snap, err := Status(top)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range snap.Files {
		if f.Path == "café.go" {
			found = true
			if err := Stage(top, f.Path); err != nil {
				t.Fatalf("stage via parsed path: %v", err)
			}
		}
	}
	if !found {
		t.Fatalf("café.go not in status: %+v", snap.Files)
	}
}

func TestBlame(t *testing.T) {
	top := initRepo(t)
	lines, err := Blame(top, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %+v", lines)
	}
	b := lines[0]
	if b.Author != "t" || b.Summary != "init" || len(b.SHA) != 7 || b.Time == 0 {
		t.Fatalf("blame = %+v", b)
	}
}

func TestAlignMapsUnchangedLines(t *testing.T) {
	_, oldFor := Align([]byte("a\nb\nc"), []byte("a\nX\nnew\nc"))
	want := []int{0, -1, -1, 2}
	for i, v := range want {
		if oldFor[i] != v {
			t.Fatalf("oldFor = %v, want %v", oldFor, want)
		}
	}
}

func TestBranches(t *testing.T) {
	top := initRepo(t)
	if err := CreateBranch(top, "feature"); err != nil {
		t.Fatal(err)
	}
	bs, err := Branches(top)
	if err != nil || len(bs) != 2 {
		t.Fatalf("branches = %v, %v", bs, err)
	}
	if err := Checkout(top, "main"); err != nil {
		t.Fatal(err)
	}
	snap, _ := Status(top)
	if snap.Branch != "main" {
		t.Fatalf("branch = %q", snap.Branch)
	}
}

func TestRemoteBranches(t *testing.T) {
	origin := initRepo(t)
	run(origin, "branch", "feature") // remote-only branch
	top := t.TempDir()
	if _, err := run(top, "clone", "-q", "--branch", "main", origin, "."); err != nil {
		t.Fatal(err)
	}
	top, _ = run(top, "rev-parse", "--show-toplevel")

	bs, err := Branches(top)
	if err != nil {
		t.Fatal(err)
	}
	var remote *Branch
	for i, b := range bs {
		if b.Name == "main" && b.Remote {
			t.Fatal("main has a local counterpart; origin/main must be hidden")
		}
		if b.Name == "origin/feature" {
			remote = &bs[i]
		}
	}
	if remote == nil || !remote.Remote {
		t.Fatalf("origin/feature missing from %+v", bs)
	}
	local, err := CheckoutRemote(top, "origin/feature")
	if err != nil || local != "feature" {
		t.Fatalf("CheckoutRemote = %q, %v", local, err)
	}
	snap, _ := Status(top)
	if snap.Branch != "feature" || snap.Upstream != "origin/feature" {
		t.Fatalf("branch=%q upstream=%q", snap.Branch, snap.Upstream)
	}
}

// The user-facing flow: remote and local both commit to the same line, then
// pull. git ≥2.27 with no pull.rebase config refuses to reconcile at all
// (no conflict state, just a hint) — Pull must fall back to merging.
func TestPullDivergentConflicts(t *testing.T) {
	origin := initRepo(t)
	top := t.TempDir()
	if _, err := run(top, "clone", "-q", origin, "."); err != nil {
		t.Fatal(err)
	}
	top, _ = run(top, "rev-parse", "--show-toplevel")
	run(top, "config", "user.email", "t@t.t")
	run(top, "config", "user.name", "t")

	os.WriteFile(filepath.Join(origin, "a.txt"), []byte("remote\n"), 0o644)
	run(origin, "commit", "-q", "-am", "remote change")
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("local\n"), 0o644)
	run(top, "commit", "-q", "-am", "local change")

	if _, err := Pull(top); err == nil {
		t.Fatal("expected pull to fail with a merge conflict")
	}
	snap, _ := Status(top)
	if len(snap.Files) != 1 || !snap.Files[0].Conflict() {
		t.Fatalf("no conflict state after divergent pull: %+v", snap.Files)
	}
	data, _ := os.ReadFile(filepath.Join(top, "a.txt"))
	if !strings.Contains(string(data), "<<<<<<<") {
		t.Fatalf("no conflict markers: %q", data)
	}

	// Resolve to ours: the working tree then equals HEAD, zero changed files —
	// but the merge still needs a commit, and the UI needs to know that.
	if err := ResolveOurs(top, "a.txt"); err != nil {
		t.Fatal(err)
	}
	snap, _ = Status(top)
	if !snap.Merging {
		t.Fatal("merge in progress not detected")
	}
	if MergeMsg(top) == "" {
		t.Fatal("no prepared merge message")
	}
	if _, err := Commit(top, "merge remote"); err != nil {
		t.Fatalf("concluding commit refused: %v", err)
	}
	snap, _ = Status(top)
	if snap.Merging {
		t.Fatal("merge not concluded after commit")
	}
}

func TestResolveConflict(t *testing.T) {
	top := initRepo(t)
	run(top, "checkout", "-q", "-b", "feature")
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("theirs\n"), 0o644)
	run(top, "commit", "-q", "-am", "theirs")
	run(top, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("ours\n"), 0o644)
	run(top, "commit", "-q", "-am", "ours")
	runLoose(top, "merge", "feature") // conflicts by construction

	snap, _ := Status(top)
	if len(snap.Files) != 1 || !snap.Files[0].Conflict() {
		t.Fatalf("files = %+v", snap.Files)
	}
	if err := ResolveTheirs(top, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(top, "a.txt")); string(data) != "theirs\n" {
		t.Fatalf("content = %q", data)
	}
	snap, _ = Status(top)
	if len(snap.Files) != 1 || !snap.Files[0].Staged() || snap.Files[0].Conflict() {
		t.Fatalf("after resolve: %+v", snap.Files)
	}
}

func TestUnstageBeforeFirstCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	top := t.TempDir()
	run(top, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("x\n"), 0o644)
	top, _ = run(top, "rev-parse", "--show-toplevel")
	if err := Stage(top, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := Unstage(top, "a.txt"); err != nil {
		t.Fatal(err) // exercises the rm --cached fallback (no HEAD yet)
	}
	snap, _ := Status(top)
	if len(snap.Files) != 1 || !snap.Files[0].Untracked() {
		t.Fatalf("files = %+v", snap.Files)
	}
}

func TestTopNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	// Guard against the temp dir living under a real repo.
	if os.Getenv("CI") == "" {
		os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nonexistent\n"), 0o644)
	}
	if _, err := Top(dir); err == nil {
		t.Skip("temp dir is inside a repo")
	}
}

// Push on a branch with no upstream publishes it (push -u origin HEAD)
// instead of failing with "no upstream branch".
func TestPushPublishesNewBranch(t *testing.T) {
	top := initRepo(t)
	bare := t.TempDir()
	if _, err := run(bare, "init", "-q", "--bare"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(top, "remote", "add", "origin", bare); err != nil {
		t.Fatal(err)
	}
	if err := CreateBranch(top, "develop"); err != nil {
		t.Fatal(err)
	}
	if _, err := Push(top); err != nil {
		t.Fatalf("push of unpublished branch failed: %v", err)
	}
	if up, err := run(top, "rev-parse", "--abbrev-ref", "@{upstream}"); err != nil || up != "origin/develop" {
		t.Fatalf("upstream = %q (%v), want origin/develop", up, err)
	}
	// Second push goes down the plain-push path.
	if _, err := Push(top); err != nil {
		t.Fatalf("second push failed: %v", err)
	}
}

func TestUndoCommit(t *testing.T) {
	top := initRepo(t)
	os.WriteFile(filepath.Join(top, "b.txt"), []byte("two\n"), 0o644)
	if err := StageAll(top); err != nil {
		t.Fatal(err)
	}
	if _, err := Commit(top, "wrong branch"); err != nil {
		t.Fatal(err)
	}
	if sum, err := HeadSummary(top); err != nil || !strings.Contains(sum, "wrong branch") {
		t.Fatalf("HeadSummary = %q (%v), want subject 'wrong branch'", sum, err)
	}
	if err := UndoCommit(top); err != nil {
		t.Fatal(err)
	}
	// Back to the initial commit, with b.txt staged again.
	if sum, _ := HeadSummary(top); !strings.Contains(sum, "init") {
		t.Fatalf("HEAD = %q, want the initial commit", sum)
	}
	snap, err := Status(top)
	if err != nil {
		t.Fatal(err)
	}
	staged := false
	for _, f := range snap.Files {
		if f.Path == "b.txt" && f.Staged() {
			staged = true
		}
	}
	if !staged {
		t.Fatal("b.txt not staged after undo — soft reset should keep the index")
	}
	// Initial commit has no parent: must error, not wipe anything.
	if err := UndoCommit(top); err == nil {
		t.Fatal("UndoCommit on the initial commit should fail")
	}
}

func TestLogAndShowCommit(t *testing.T) {
	top := initRepo(t)
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("one\ntwo\n"), 0o644)
	if _, err := run(top, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(top, "commit", "-q", "-m", "second"); err != nil {
		t.Fatal(err)
	}

	cs, err := Log(top, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Subject != "second" || cs[1].Subject != "init" {
		t.Fatalf("log = %+v", cs)
	}
	if cs[0].SHA == "" || cs[0].Author != "t" || cs[0].Time == 0 {
		t.Fatalf("entry = %+v", cs[0])
	}

	out, err := ShowCommit(top, cs[0].SHA)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "second") || !strings.Contains(out, "+two") {
		t.Fatalf("show = %q", out)
	}

	// no commits yet: empty, not an error
	empty := t.TempDir()
	if _, err := run(empty, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	cs, err = Log(empty, 10)
	if err != nil || cs != nil {
		t.Fatalf("empty repo: %v %+v", cs, err)
	}
}

func TestGraphLog(t *testing.T) {
	top := initRepo(t)
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("two\n"), 0o644)
	run(top, "commit", "-q", "-am", "second")

	cs, err := GraphLog(top, 100)
	if err != nil || len(cs) != 2 {
		t.Fatalf("commits = %+v, %v", cs, err)
	}
	c := cs[0]
	if c.Subject != "second" || len(c.Parents) != 1 || c.Parents[0] != cs[1].Hash {
		t.Fatalf("head = %+v", c)
	}
	if !strings.Contains(c.Refs, "main") || c.Author != "t" || c.Age == "" {
		t.Fatalf("head = %+v", c)
	}
	if len(cs[1].Parents) != 0 {
		t.Fatalf("root has parents: %+v", cs[1])
	}
}
