package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GurYN/cove-editor/internal/editor"
)

func TestRenderMarkdown(t *testing.T) {
	src := "# Title\n\nSome **bold** and `code` and [a link](https://x.example).\n\n- item one\n> quoted\n\n```go\nfmt.Println(1)\n```\n\n---\n"
	text, spans := renderMarkdown([]byte(src))

	for _, want := range []string{"Title", "Some bold and code and a link.", "• item one", "│ quoted", "│ fmt.Println(1) │", "╭─", "╰─", strings.Repeat("─", 40)} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered text missing %q:\n%s", want, text)
		}
	}
	for _, gone := range []string{"#", "**", "`", "](", "https://x.example", "```"} {
		if strings.Contains(text, gone) {
			t.Errorf("marker %q survived rendering:\n%s", gone, text)
		}
	}

	// Title span is ClassFunction and covers the word.
	if len(spans) == 0 || spans[0].Class != editor.ClassFunction || text[spans[0].Start:spans[0].End] != "Title" {
		t.Fatalf("bad heading span: %+v", spans)
	}
	for i := 1; i < len(spans); i++ {
		if spans[i].Start < spans[i-1].Start {
			t.Fatalf("spans not sorted at %d: %+v", i, spans)
		}
	}

	// staticSyntax filters to the requested window.
	syn := staticSyntax{spans: spans}
	got := syn.Spans(nil, spans[0].Start, spans[0].End)
	if len(got) == 0 || got[0] != spans[0] {
		t.Fatalf("staticSyntax window filter broken: %+v", got)
	}
	if s := syn.Spans(nil, len(text)+100, len(text)+200); len(s) != 0 {
		t.Fatalf("expected no spans past EOF, got %+v", s)
	}
}

func TestRenderMarkdownTable(t *testing.T) {
	src := "| Name | Description |\n|---|---|\n| `x` | short |\n| yy | a **much** longer cell |\n"
	text, _ := renderMarkdown([]byte(src))
	want := strings.Join([]string{
		"┌──────┬────────────────────┐",
		"│ Name │ Description        │",
		"├──────┼────────────────────┤",
		"│ x    │ short              │",
		"│ yy   │ a much longer cell │",
		"└──────┴────────────────────┘",
	}, "\n")
	if !strings.Contains(text, want) {
		t.Errorf("table misaligned:\ngot:\n%s\nwant:\n%s", text, want)
	}
}

func TestMarkdownPreviewAction(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	m := New(dir, nil)
	p := filepath.Join(dir, "readme.md")
	os.WriteFile(p, []byte("# Hello\n"), 0o644)
	m.openFile(p)
	m.markdownPreview()
	d := m.doc()
	if d == nil || !d.virtual || d.path != "Preview: readme.md" {
		t.Fatalf("expected preview tab, got %+v", d)
	}
	if got := string(d.ed.Buf.Bytes()); !strings.Contains(got, "Hello") || strings.Contains(got, "#") {
		t.Fatalf("preview content wrong: %q", got)
	}
	// Rerunning replaces in place, no duplicate tab.
	n := len(m.docs)
	m.focusPane(false)
	m.markdownPreview() // active is now the preview tab itself: not .md, notifies
	if len(m.docs) != n {
		t.Fatalf("preview duplicated: %d tabs", len(m.docs))
	}
}
