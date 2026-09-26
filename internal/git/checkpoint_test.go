package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckpointAndChangedSince: a checkpoint captures tracked and
// untracked files without touching the real index; edits, new files and
// deletions after it come back as hunks the review panel can revert.
func TestCheckpointAndChangedSince(t *testing.T) {
	top := initRepo(t)
	os.WriteFile(filepath.Join(top, "new.txt"), []byte("fresh\n"), 0o644) // untracked at checkpoint time
	os.WriteFile(filepath.Join(top, ".gitignore"), []byte("ignored.txt\n"), 0o644)
	os.WriteFile(filepath.Join(top, "ignored.txt"), []byte("junk\n"), 0o644)
	sha, err := Checkpoint(top)
	if err != nil {
		t.Fatal(err)
	}
	if LastCheckpoint(top) != sha {
		t.Fatalf("ref not updated: %q vs %q", LastCheckpoint(top), sha)
	}
	if st, _ := run(top, "status", "--porcelain"); !strings.Contains(st, "?? new.txt") {
		t.Fatalf("real index touched: %q", st)
	}
	if b, err := ShowAt(top, sha, "new.txt"); err != nil || string(b) != "fresh\n" {
		t.Fatalf("untracked file not in checkpoint: %q %v", b, err)
	}
	if _, err := ShowAt(top, sha, "ignored.txt"); err == nil {
		t.Fatal("ignored file landed in the checkpoint")
	}
	if files, _ := ChangedSince(top, sha); len(files) != 0 {
		t.Fatalf("clean tree reported changes: %+v", files)
	}

	// The agent works: edits a.txt, adds b.go, deletes new.txt.
	os.WriteFile(filepath.Join(top, "a.txt"), []byte("one\ntwo\n"), 0o644)
	os.WriteFile(filepath.Join(top, "b.go"), []byte("package b\n"), 0o644)
	os.Remove(filepath.Join(top, "new.txt"))
	files, err := ChangedSince(top, sha)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %+v", files)
	}
	by := map[string]FileDiff{}
	for _, f := range files {
		by[f.Path] = f
	}
	a := by["a.txt"]
	if a.Status != 'M' || len(a.Hunks) != 1 || a.Hunks[0].Added != 1 || a.Hunks[0].Removed != 0 {
		t.Fatalf("a.txt = %+v", a)
	}
	if h := a.Hunks[0]; h.NewStart != 1 || h.NewLines != 2 || strings.Join(h.Old, "|") != "one" || strings.Join(h.New, "|") != "one|two" {
		t.Fatalf("hunk = %+v", h)
	}
	if by["b.go"].Status != 'A' || by["new.txt"].Status != 'D' || by["b.go"].Hunks != nil || by["new.txt"].Hunks != nil {
		t.Fatalf("statuses: %+v %+v", by["b.go"], by["new.txt"])
	}
	if a.Hunks[0].FirstChange() != 2 {
		t.Fatalf("FirstChange = %d", a.Hunks[0].FirstChange())
	}

	// A second checkpoint replaces the ref; the old commit stays valid.
	sha2, _ := Checkpoint(top)
	if sha2 == sha || LastCheckpoint(top) != sha2 {
		t.Fatal("second checkpoint did not move the ref")
	}
	if files, _ := ChangedSince(top, sha2); len(files) != 0 {
		t.Fatalf("fresh checkpoint reported changes: %+v", files)
	}
}

// TestParseHunks: headers with and without counts, context vs. changed
// lines on each side, the function header, and the no-newline marker.
func TestParseHunks(t *testing.T) {
	patch := "diff --git a/x b/x\n--- a/x\n+++ b/x\n" +
		"@@ -1,3 +1,4 @@ func main() {\n a\n-b\n+B\n+c\n d\n" +
		"@@ -9 +10,0 @@\n-gone\n" +
		"@@ -20,2 +20,2 @@\n x\n-y\n\\ No newline at end of file\n+z\n\\ No newline at end of file\n"
	hs := ParseHunks(patch)
	if len(hs) != 3 {
		t.Fatalf("hunks = %d", len(hs))
	}
	h := hs[0]
	if h.OldStart != 1 || h.OldLines != 3 || h.NewStart != 1 || h.NewLines != 4 || h.Header != "func main() {" {
		t.Fatalf("h0 = %+v", h)
	}
	if strings.Join(h.Old, "|") != "a|b|d" || strings.Join(h.New, "|") != "a|B|c|d" || h.Added != 2 || h.Removed != 1 {
		t.Fatalf("h0 lines = %+v", h)
	}
	if h := hs[1]; h.OldStart != 9 || h.OldLines != 1 || h.NewStart != 10 || h.NewLines != 0 || len(h.New) != 0 {
		t.Fatalf("h1 = %+v", h)
	}
	if h := hs[2]; !h.OldNoNL || !h.NewNoNL || h.Header != "" {
		t.Fatalf("h2 = %+v", h)
	}
	if hs[0].Signature() == hs[1].Signature() {
		t.Fatal("signatures collide")
	}
}

// TestApplyHunksAndStage: staging a subset of hunks puts checkpoint
// content plus those hunks in the index — the worktree keeps the rest —
// and StagePath stages a whole file or its deletion.
func TestApplyHunksAndStage(t *testing.T) {
	top := initRepo(t)
	nums := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
		"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen", "twenty"}
	orig := strings.Join(nums, "\n") + "\n"
	os.WriteFile(filepath.Join(top, "a.txt"), []byte(orig), 0o644)
	if _, err := run(top, "commit", "-qam", "twenty lines"); err != nil {
		t.Fatal(err)
	}
	sha, _ := Checkpoint(top)
	// Two far-apart edits: line 2 and line 18.
	edited := strings.Replace(strings.Replace(orig, "two\n", "TWO\n", 1), "eighteen\n", "EIGHTEEN\n", 1)
	os.WriteFile(filepath.Join(top, "a.txt"), []byte(edited), 0o644)
	files, _ := ChangedSince(top, sha)
	if len(files) != 1 || len(files[0].Hunks) != 2 {
		t.Fatalf("files = %+v", files)
	}
	if !strings.HasPrefix(files[0].Hunks[0].Text, "@@ -1,5 +1,5 @@\n one\n-two\n+TWO\n") {
		t.Fatalf("hunk text = %q", files[0].Hunks[0].Text)
	}
	old, _ := ShowAt(top, sha, "a.txt")
	got := ApplyHunks(old, files[0].Hunks[1:]) // only the second hunk
	want := strings.Replace(orig, "eighteen\n", "EIGHTEEN\n", 1)
	if string(got) != want {
		t.Fatalf("ApplyHunks =\n%s", got)
	}
	if string(ApplyHunks(old, files[0].Hunks)) != edited {
		t.Fatal("ApplyHunks with all hunks != worktree")
	}
	if err := StageContent(top, "a.txt", got); err != nil {
		t.Fatal(err)
	}
	if idx, _ := ShowIndex(top, "a.txt"); string(idx) != want {
		t.Fatalf("index =\n%s", idx)
	}
	if st, _ := run(top, "status", "--porcelain"); !strings.HasPrefix(st, "MM a.txt") {
		t.Fatalf("status = %q (want staged + unstaged)", st)
	}
	// Whole-file staging, including a deletion.
	os.WriteFile(filepath.Join(top, "b.txt"), []byte("b\n"), 0o644)
	if err := StagePath(top, "b.txt"); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(top, "a.txt"))
	if err := StagePath(top, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if st, _ := run(top, "status", "--porcelain"); !strings.Contains(st, "D  a.txt") || !strings.Contains(st, "A  b.txt") {
		t.Fatalf("status = %q", st)
	}
	tree, _ := TreeOf(top, sha)
	if len(tree) != 40 && len(tree) != 64 {
		t.Fatalf("TreeOf = %q", tree)
	}
}

// TestApplyHunksEOF: a hunk that changes the last line without a trailing
// newline, and a pure insertion after the last line.
func TestApplyHunksEOF(t *testing.T) {
	old := []byte("a\nb")
	h := Hunk{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 2, Old: []string{"a", "b"}, New: []string{"a", "B"}, OldNoNL: true, NewNoNL: true}
	if got := ApplyHunks(old, []Hunk{h}); string(got) != "a\nB" {
		t.Fatalf("eof: %q", got)
	}
	old = []byte("a\n")
	h = Hunk{OldStart: 1, OldLines: 0, NewStart: 2, NewLines: 1, New: []string{"z"}}
	if got := ApplyHunks(old, []Hunk{h}); string(got) != "a\nz\n" {
		t.Fatalf("append: %q", got)
	}
}
