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

// Folds must anchor on the construct's header line in every language —
// wrapper kinds collide across grammars ("block" is Go's junk container
// but HCL's actual construct), hence the per-language foldWrappers sets.
func TestFoldsPerLanguageAnchors(t *testing.T) {
	cases := []struct {
		name, path, src string
		want            [2]int
	}{
		{"hcl", "t.tf", "resource \"a\" \"b\" {\n  name = 1\n  loc  = 2\n}\n", [2]int{0, 3}},
		{"python", "t.py", "def f():\n    x = 1\n    return x\n", [2]int{0, 2}},
		{"typescript", "t.ts", "function f() {\n  const x = 1;\n  return x;\n}\n", [2]int{0, 3}},
		{"rust", "t.rs", "fn f() -> i32 {\n    let x = 1;\n    x\n}\n", [2]int{0, 3}},
	}
	for _, c := range cases {
		h := New(c.path, []byte(c.src))
		if h == nil {
			t.Fatalf("%s: no highlighter", c.name)
		}
		folds := h.Folds([]byte(c.src), 0, len(c.src))
		h.Close()
		if len(folds) == 0 || folds[0] != c.want {
			t.Fatalf("%s: got %v, want first fold %v", c.name, folds, c.want)
		}
		for _, f := range folds[1:] {
			if f[0] == c.want[0]+1 && f[1] >= c.want[1]-1 {
				t.Fatalf("%s: junk wrapper fold %v shadows the construct %v", c.name, f, c.want)
			}
		}
	}
}
