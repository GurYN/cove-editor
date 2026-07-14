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
