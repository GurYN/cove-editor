package syntax

import "testing"

// Folds must return one region per multi-line construct, outermost per
// start line, sorted — the two functions here, not their inner blocks.
func TestFolds(t *testing.T) {
	src := []byte("package p\n\nfunc a() {\n\tx := 1\n\t_ = x\n}\n\nfunc b() {\n\t_ = 2\n}\n")
	h := New("t.go", src)
	if h == nil {
		t.Fatal("no highlighter for t.go")
	}
	defer h.Close()

	folds := h.Folds(src, 0, len(src))
	want := [][2]int{{2, 5}, {7, 9}}
	if len(folds) != 2 || folds[0] != want[0] || folds[1] != want[1] {
		t.Fatalf("got %v, want %v", folds, want)
	}

	// Window restricted to func b's header line only.
	start := 0
	for line, i := 0, 0; i < len(src); i++ {
		if line == 7 {
			start = i
			break
		}
		if src[i] == '\n' {
			line++
		}
	}
	folds = h.Folds(src, start, start+1)
	if len(folds) != 1 || folds[0] != want[1] {
		t.Fatalf("windowed: got %v, want [%v]", folds, want[1])
	}
}
