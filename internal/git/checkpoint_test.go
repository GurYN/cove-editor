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
