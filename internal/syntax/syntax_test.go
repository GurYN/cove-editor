package syntax

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
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

// TestAllLanguagesLoad guards against silent nil from a bad query: every
// registered extension must yield a working highlighter that produces spans.
func TestAllLanguagesLoad(t *testing.T) {
	samples := map[string]string{
		"x.go":               "package main\n\nfunc main() {}\n",
		"x.json":             "{\"a\": 1}\n",
		"x.py":               "def f():\n    return 1\n",
		"x.rs":               "fn main() { let x = 1; }\n",
		"x.ts":               "function f(): number { return 1 }\n",
		"x.tsx":              "const x = <div id=\"a\"/>\n",
		"x.js":               "function f() { return 1 }\n",
		"x.mjs":              "export function f() { return 1 }\n",
		"x.cjs":              "module.exports = function f() { return 1 }\n",
		"x.jsx":              "const x = <div id=\"a\"/>\n",
		"x.html":             "<!doctype html><p class=\"a\">hi</p><!-- c -->\n",
		"x.htm":              "<p>hi</p>\n",
		"x.css":              "/* c */ .a { color: #fff; width: 10px; }\n",
		"x.sh":               "#!/bin/sh\nif true; then echo \"hi\"; fi\n",
		"x.bash":             "for i in 1 2; do echo $i; done\n",
		"x.zsh":              "echo hi\n",
		"x.toml":             "# c\n[table]\nkey = \"v\"\nn = 1\nb = true\n",
		"x.md":               "# title\n\nsome **bold** text\n",
		"x.yaml":             "# c\nkey: value\nn: 1\nb: true\nlist:\n  - a\n",
		"x.yml":              "key: \"v\"\n",
		"docker-compose.yml": "services:\n  web:\n    image: nginx:latest\n    ports:\n      - \"80:80\"\n",
		"Dockerfile":         "FROM golang:1.26 AS build\n# c\nARG V=1\nENV K=v\nRUN --mount=type=cache,target=/go go build\nCOPY . /app\n",
		"Dockerfile.prod":    "FROM alpine\nCMD [\"sh\"]\n",
		"Containerfile":      "FROM alpine\n",
		"x.dockerfile":       "FROM alpine\nUSER ${APP_USER}\n",
		"go.mod":             "module example.com/x\n\ngo 1.26\n\nrequire github.com/a/b v1.0.0 // indirect\n",
		"main.tf":            "# c\nresource \"aws_instance\" \"web\" {\n  count = 2\n  ami   = var.ami\n  tags  = { for k in [1] : k => true if k != null }\n}\n",
		"x.tfvars":           "region = \"eu-west-3\"\n",
		"x.hcl":              "job \"a\" {\n  n = 1\n}\n",
		"x.mts":              "export function f(): number { return 1 }\n",
		"x.cts":              "module.exports = 1\n",
		"x.svg":              "<svg viewBox=\"0 0 1 1\"><rect width=\"1\"/></svg>\n",
		"x.xml":              "<?xml version=\"1.0\"?><a b=\"c\">t</a>\n",
		"x.mdx":              "# t\n\nsome *text*\n",
		"x.jsonc":            "{\"a\": 1}\n",
		"x.ini":              "# c\n[sec]\nk = \"v\"\n",
		"x.cfg":              "[sec]\nk = 1\n",
		"x.properties":       "# c\nk = \"v\"\n",
		".eslintrc":          "{\"rules\": {}}\n",
		".babelrc":           "{\"presets\": []}\n",
		".zshrc":             "export PATH=\"$HOME/bin:$PATH\"\n# c\n",
		".bashrc":            "alias ll='ls -l'\n",
		"Makefile":           "# c\nall:\n\tgo build ./...\n",
		"Justfile":           "# c\nbuild:\n\tgo build\n",
		"Cargo.lock":         "[[package]]\nname = \"a\"\nversion = \"1.0.0\"\n",
		"Pipfile":            "[packages]\nrequests = \"*\"\n",
		"uv.lock":            "version = 1\n",
		"poetry.lock":        "[[package]]\nname = \"a\"\n",
		"Procfile":           "web: bin/server\n",
		".env":               "# c\nAPI_KEY=\"secret\"\nPORT=8080\n",
		".env.local":         "DEBUG=true\n",
		".gitignore":         "# build output\n*.log\ndist/\n",
		".dockerignore":      "node_modules\n# c\n",
		".gitattributes":     "# c\n*.go text eol=lf\n",
		".editorconfig":      "# c\nroot = true\n\n[*.py]\nindent_size = 4\n",
		".gitconfig":         "# c\n[user]\nname = \"x\"\n",
		".gitmodules":        "[submodule \"a\"]\npath = \"a\"\n",
		".npmrc":             "# c\nregistry = \"https://registry.npmjs.org\"\n",
	}
	for name, src := range samples {
		h := New(name, []byte(src))
		if h == nil {
			t.Errorf("%s: no highlighter (grammar or query failed to load)", name)
			continue
		}
		if spans := h.Spans([]byte(src), 0, len(src)); len(spans) == 0 {
			t.Errorf("%s: highlighter produced no spans", name)
		}
	}
}

// spanAt reports whether some span with the wanted class covers offset.
func spanAt(spans []editor.HLSpan, off, class int) bool {
	for _, s := range spans {
		if s.Class == class && s.Start <= off && off < s.End {
			return true
		}
	}
	return false
}

// TestInjections: embedded languages must highlight — markdown inline +
// fenced code, HTML <script>/<style>.
func TestInjections(t *testing.T) {
	md := "# t\n\n**bold** and `code`\n\n```go\nfunc main() {}\n```\n"
	h := New("x.md", []byte(md))
	if h == nil {
		t.Fatal("no markdown highlighter")
	}
	spans := h.Spans([]byte(md), 0, len(md))
	for name, off := range map[string]int{
		"bold": strings.Index(md, "bold"), "code span": strings.Index(md, "`code`") + 1,
	} {
		want := editor.ClassKeyword
		if name == "code span" {
			want = editor.ClassString
		}
		if !spanAt(spans, off, want) {
			t.Errorf("markdown inline: %s not highlighted in %v", name, spans)
		}
	}
	if !spanAt(spans, strings.Index(md, "func"), editor.ClassKeyword) {
		t.Errorf("go fence: 'func' not keyword-highlighted in %v", spans)
	}

	html := "<script>var x = 1;</script><style>a { color: red }</style>\n"
	h = New("x.html", []byte(html))
	if h == nil {
		t.Fatal("no html highlighter")
	}
	spans = h.Spans([]byte(html), 0, len(html))
	if !spanAt(spans, strings.Index(html, "var"), editor.ClassKeyword) {
		t.Errorf("script: 'var' not keyword-highlighted in %v", spans)
	}
	if !spanAt(spans, strings.Index(html, "color"), editor.ClassProperty) {
		t.Errorf("style: 'color' not property-highlighted in %v", spans)
	}
}

// TestInjectionEdit: after an edit that adds a fence, children must resync.
func TestInjectionEdit(t *testing.T) {
	src := []byte("text\n\n```go\nreturn\n```\n")
	h := New("x.md", src)
	if h == nil {
		t.Fatal("no markdown highlighter")
	}
	if !spanAt(h.Spans(src, 0, len(src)), 12, editor.ClassKeyword) { // "return"
		t.Fatal("fence not highlighted before edit")
	}
	// Insert "x " at the start: everything shifts by 2.
	next := append([]byte("x "), src...)
	h.Edit(0, 0, 2, [2]int{0, 0}, [2]int{0, 0}, [2]int{0, 2})
	if !spanAt(h.Spans(next, 0, len(next)), 14, editor.ClassKeyword) {
		t.Fatal("fence highlight did not survive an edit")
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
