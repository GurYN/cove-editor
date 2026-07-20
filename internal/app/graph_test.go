package app

import (
	"strings"
	"testing"

	"github.com/GurYN/cove-editor/internal/git"
)

// A merge history: feature branched off the root, merged into main.
//
//	main  m3 ─▶ m2(merge: m1, f1) ; f1 ─▶ m0 ; m1 ─▶ m0
func TestRenderGraph(t *testing.T) {
	cs := []git.GraphCommit{
		{Hash: "m3", Short: "aaaa333", Parents: []string{"m2"}, Refs: "HEAD -> main", Subject: "top", Author: "t", Age: "1h ago"},
		{Hash: "m2", Short: "aaaa222", Parents: []string{"m1", "f1"}, Subject: "merge", Author: "t", Age: "2h ago"},
		{Hash: "f1", Short: "ffff111", Parents: []string{"m0"}, Refs: "feature", Subject: "feat", Author: "t", Age: "3h ago"},
		{Hash: "m1", Short: "aaaa111", Parents: []string{"m0"}, Subject: "mid", Author: "t", Age: "4h ago"},
		{Hash: "m0", Short: "aaaa000", Parents: nil, Subject: "root", Author: "t", Age: "5h ago"},
	}
	out := renderGraph(cs)
	lines := strings.Split(out, "\n")

	// header: "main" vertical in lane 0 (4 rows), blank row, 5 commit rows.
	if len(lines) != 10 {
		t.Fatalf("lines = %d\n%s", len(lines), out)
	}
	if lines[0] != "m" || lines[3] != "n" || lines[4] != "" {
		t.Fatalf("header:\n%s", out)
	}
	want := []string{
		"●  aaaa333 (HEAD -> main) top · t, 1h ago",
		"●─╮  aaaa222 merge · t, 2h ago",
		"│ ●  ffff111 (feature) feat · t, 3h ago",
		"● │  aaaa111 mid · t, 4h ago",
		"●─╯  aaaa000 root · t, 5h ago",
	}
	for i, w := range want {
		if lines[5+i] != w {
			t.Fatalf("row %d = %q, want %q", i, lines[5+i], w)
		}
	}
}
