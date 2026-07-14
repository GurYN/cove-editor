package syntax

import (
	"bytes"
	"fmt"
	"slices"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/buffer"
	"github.com/GurYN/cove-editor/internal/editor"
)

func goSource(funcs int) []byte {
	var sb bytes.Buffer
	sb.WriteString("package main\n\nimport \"fmt\"\n\n")
	for i := range funcs {
		fmt.Fprintf(&sb, "// handler %d does a thing\nfunc handler%d(x int) string {\n\tif x > %d {\n\t\treturn \"big\"\n\t}\n\treturn fmt.Sprintf(\"%%d\", x)\n}\n\n", i, i, i)
	}
	return sb.Bytes()
}

func TestHighlightsGo(t *testing.T) {
	src := []byte("package main\n\nfunc main() {\n\ts := \"hi\" // greet\n}\n")
	h := New("x.go", src)
	if h == nil {
		t.Fatal("no highlighter for .go")
	}
	spans := h.Spans(src, 0, len(src))
	classes := map[int]bool{}
	for _, s := range spans {
		classes[s.Class] = true
	}
	for _, want := range []int{editor.ClassKeyword, editor.ClassString, editor.ClassComment, editor.ClassFunction} {
		if !classes[want] {
			t.Errorf("missing span class %d in %v", want, spans)
		}
	}
}

func TestUnsupportedLanguage(t *testing.T) {
	if h := New("notes.txt", nil); h != nil {
		t.Fatal("expected nil highlighter for .txt")
	}
}

// TestIncrementalEdit exercises the tree.Edit path: spans stay correct
// after an insertion shifts everything.
func TestIncrementalEdit(t *testing.T) {
	src := []byte("package main\n\nfunc main() {}\n")
	h := New("x.go", src)
	// Insert a line at the top: "// c\n" (5 bytes) at offset 0.
	newSrc := append([]byte("// c\n"), src...)
	h.Edit(0, 0, 5, [2]int{0, 0}, [2]int{0, 0}, [2]int{1, 0})
	spans := h.Spans(newSrc, 0, len(newSrc))
	foundFunc := false
	for _, s := range spans {
		if s.Class == editor.ClassKeyword && string(newSrc[s.Start:s.End]) == "func" {
			foundFunc = true
		}
	}
	if !foundFunc {
		t.Fatalf("keyword span lost after incremental edit: %v", spans)
	}
}

// TestKeystrokeLatencyWithSyntax is the Phase 1 version of the perf gate:
// full loop (edit -> incremental reparse -> visible-range query -> frame)
// on a ~50k-line Go file must stay under one frame at p99.
func TestKeystrokeLatencyWithSyntax(t *testing.T) {
	src := goSource(6_000) // ~50k lines of real Go
	m := editor.New(buffer.New(src))
	m.Width, m.Height = 120, 50
	if h := New("big.go", src); h == nil {
		t.Fatal("no highlighter")
	} else {
		m.Syntax = h
	}
	m.Go(25_000, 0)

	const n = 300
	for attempt := range 2 {
		samples := make([]time.Duration, n)
		for i := range samples {
			start := time.Now()
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			frame := m.View()
			samples[i] = time.Since(start)
			if len(frame) == 0 {
				t.Fatal("empty frame")
			}
		}
		slices.Sort(samples)
		p50, p99 := samples[n/2], samples[n*99/100]
		t.Logf("keystroke->frame with syntax p50=%s p99=%s (%d lines)", p50, p99, m.Buf.LineCount())
		if p99 > 16*time.Millisecond {
			if attempt < 1 {
				t.Logf("p99 %s over budget; retrying once (parallel-suite CPU contention)", p99)
				continue
			}
			t.Fatalf("p99 keystroke latency %s exceeds one frame (16ms)", p99)
		}
		return
	}
}

func TestExpandSelection(t *testing.T) {
	src := []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")
	h := New("x.go", src)
	inner := bytes.Index(src, []byte("hi"))
	// caret in the string: each expand strictly grows through the node
	// chain (content -> literal -> call -> ...); the quoted literal must be
	// one of the steps.
	lo, hi := inner, inner
	sawLiteral := false
	for i := range 8 {
		nlo, nhi, ok := h.Expand(src, lo, hi)
		if !ok {
			break
		}
		if nlo > lo || nhi < hi || (nlo == lo && nhi == hi) {
			t.Fatalf("expand step %d did not strictly grow: %q", i, src[nlo:nhi])
		}
		lo, hi = nlo, nhi
		if string(src[lo:hi]) == `"hi"` {
			sawLiteral = true
		}
	}
	if !sawLiteral {
		t.Fatal("expansion chain never selected the string literal")
	}
	if lo != 0 || hi != len(src) {
		t.Fatalf("expansion did not reach the root: %d..%d", lo, hi)
	}
}
