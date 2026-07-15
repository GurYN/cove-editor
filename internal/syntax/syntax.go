// Package syntax wraps tree-sitter: incremental parsing and highlight spans.
// All CGo is confined here; the editor sees only the editor.Syntax interface.
package syntax

import (
	_ "embed"
	"path/filepath"
	"strings"

	tstoml "github.com/tree-sitter-grammars/tree-sitter-toml/bindings/go"
	ts "github.com/tree-sitter/go-tree-sitter"

	tsmarkdown "github.com/GurYN/cove-editor/internal/syntax/tsmarkdown"
	tsbash "github.com/tree-sitter/tree-sitter-bash/bindings/go"
	tscss "github.com/tree-sitter/tree-sitter-css/bindings/go"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tshtml "github.com/tree-sitter/tree-sitter-html/bindings/go"
	tsjson "github.com/tree-sitter/tree-sitter-json/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsrust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tstypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/GurYN/cove-editor/internal/editor"
)

//go:embed queries/go.scm
var goScm string

//go:embed queries/json.scm
var jsonScm string

//go:embed queries/python.scm
var pythonScm string

//go:embed queries/rust.scm
var rustScm string

//go:embed queries/typescript.scm
var typescriptScm string

//go:embed queries/html.scm
var htmlScm string

//go:embed queries/css.scm
var cssScm string

//go:embed queries/bash.scm
var bashScm string

//go:embed queries/toml.scm
var tomlScm string

//go:embed queries/markdown.scm
var markdownScm string

//go:embed queries/markdown-inline.scm
var markdownInlineScm string

// injection marks a node type whose content is parsed with another grammar
// (markdown inline text, HTML <script>/<style>, fenced code blocks).
type injection struct {
	node   string // node type hosting the foreign content
	parent string // required parent node type ("" = any)
	lang   string // language name in langs; langFence resolves from the fence info string
}

// langFence: resolve the injected language from the code fence info string.
const langFence = "\x00fence"

type langDef struct {
	lang  *ts.Language
	query string
	inj   []injection
}

// langs is keyed by language name so injections (and fence info strings) can
// reference grammars directly; exts maps file extensions onto it.
var langs = map[string]func() langDef{
	"go":     func() langDef { return langDef{lang: ts.NewLanguage(tsgo.Language()), query: goScm} },
	"json":   func() langDef { return langDef{lang: ts.NewLanguage(tsjson.Language()), query: jsonScm} },
	"python": func() langDef { return langDef{lang: ts.NewLanguage(tspython.Language()), query: pythonScm} },
	"rust":   func() langDef { return langDef{lang: ts.NewLanguage(tsrust.Language()), query: rustScm} },
	"typescript": func() langDef {
		return langDef{lang: ts.NewLanguage(tstypescript.LanguageTypescript()), query: typescriptScm}
	},
	"tsx":  func() langDef { return langDef{lang: ts.NewLanguage(tstypescript.LanguageTSX()), query: typescriptScm} },
	"css":  func() langDef { return langDef{lang: ts.NewLanguage(tscss.Language()), query: cssScm} },
	"bash": func() langDef { return langDef{lang: ts.NewLanguage(tsbash.Language()), query: bashScm} },
	"toml": func() langDef { return langDef{lang: ts.NewLanguage(tstoml.Language()), query: tomlScm} },
	"html": func() langDef {
		return langDef{lang: ts.NewLanguage(tshtml.Language()), query: htmlScm, inj: []injection{
			{node: "raw_text", parent: "script_element", lang: "typescript"},
			{node: "raw_text", parent: "style_element", lang: "css"},
		}}
	},
	// ponytail: the markdown block grammar's incremental reparse is slow
	// (~110ms/keystroke at 40k lines, linear — ~3ms on a 1k-line README);
	// that's the grammar's external scanner, not the injection layer (5ms).
	// If huge markdown ever matters, debounce reparse for .md specifically.
	"markdown": func() langDef {
		return langDef{lang: ts.NewLanguage(tsmarkdown.Language()), query: markdownScm, inj: []injection{
			{node: "inline", lang: "markdown-inline"},
			{node: "code_fence_content", parent: "fenced_code_block", lang: langFence},
		}}
	},
	"markdown-inline": func() langDef {
		return langDef{lang: ts.NewLanguage(tsmarkdown.InlineLanguage()), query: markdownInlineScm}
	},
}

var exts = map[string]string{
	".go": "go", ".json": "json", ".py": "python", ".rs": "rust",
	".ts": "typescript", ".js": "typescript", ".mjs": "typescript", ".cjs": "typescript",
	".tsx": "tsx", ".jsx": "tsx",
	".html": "html", ".htm": "html", ".css": "css",
	".sh": "bash", ".bash": "bash", ".zsh": "bash",
	".toml": "toml", ".md": "markdown", ".markdown": "markdown",
}

// fenceLangs maps code fence info strings (```go, ```js …) to language names.
var fenceLangs = map[string]string{
	"go": "go", "golang": "go",
	"js": "typescript", "javascript": "typescript", "ts": "typescript", "typescript": "typescript",
	"jsx": "tsx", "tsx": "tsx",
	"py": "python", "python": "python",
	"rs": "rust", "rust": "rust",
	"json": "json", "html": "html", "css": "css",
	"sh": "bash", "bash": "bash", "shell": "bash", "zsh": "bash",
	"toml": "toml",
}

// classFor maps a tree-sitter capture name to an editor style class.
// Longest-prefix conventions: "function.builtin" still means function.
func classFor(capture string) int {
	head, _, _ := strings.Cut(capture, ".")
	switch head {
	case "keyword", "include", "repeat", "conditional", "label":
		return editor.ClassKeyword
	case "string", "escape", "text":
		return editor.ClassString
	case "comment":
		return editor.ClassComment
	case "number", "float":
		return editor.ClassNumber
	case "function", "method", "constructor":
		return editor.ClassFunction
	case "type", "attribute":
		return editor.ClassType
	case "constant", "boolean":
		return editor.ClassConstant
	case "property", "field", "tag":
		return editor.ClassProperty
	case "operator":
		return editor.ClassOperator
	default:
		return editor.ClassNone
	}
}

// Highlighter implements editor.Syntax for one buffer.
type Highlighter struct {
	parser  *ts.Parser
	query   *ts.Query
	classes []int // capture index -> style class
	tree    *ts.Tree
	stale   bool

	inj        []injection       // injection specs for this language (usually nil)
	children   map[string]*child // injected language name -> its parser/tree
	childStale bool              // parent reparsed since children were built
	childLo    int               // viewport the children currently cover
	childHi    int
}

// child is one injected language: its tree covers only the ranges collected
// from the parent tree (via SetIncludedRanges), reparsed whole on each edit.
type child struct {
	parser  *ts.Parser
	query   *ts.Query
	classes []int
	tree    *ts.Tree // nil when the buffer currently has no ranges for it
}

func compile(d langDef) (*ts.Parser, *ts.Query, []int, bool) {
	q, qerr := ts.NewQuery(d.lang, d.query)
	if qerr != nil {
		return nil, nil, nil, false
	}
	p := ts.NewParser()
	if err := p.SetLanguage(d.lang); err != nil {
		return nil, nil, nil, false
	}
	var classes []int
	for _, name := range q.CaptureNames() {
		classes = append(classes, classFor(name))
	}
	return p, q, classes, true
}

// New returns a highlighter for the file at path, or nil if the language is
// not supported.
func New(path string, src []byte) *Highlighter {
	name, ok := exts[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil
	}
	d := langs[name]()
	p, q, classes, ok := compile(d)
	if !ok {
		return nil
	}
	h := &Highlighter{parser: p, query: q, classes: classes, inj: d.inj}
	h.tree = p.Parse(src, nil)
	if len(h.inj) > 0 {
		h.children = map[string]*child{}
		h.childStale = true
	}
	return h
}

// parseChildren walks the parent tree for injection sites intersecting the
// viewport [lo, hi) and reparses each injected language over those ranges
// only — full-file child coverage costs seconds on a 40k-line markdown file.
// ponytail: children rebuild from scratch whenever the parent reparses or the
// viewport moves (no incremental edit routing); viewports are small.
func (h *Highlighter) parseChildren(src []byte, lo, hi int) {
	ranges := map[string][]ts.Range{}
	c := h.tree.Walk()
	defer c.Close()
	for ok := true; ok; {
		n := c.Node()
		matched := false
		inView := int(n.EndByte()) >= lo && int(n.StartByte()) <= hi
		for _, in := range h.inj {
			if !inView || n.Kind() != in.node {
				continue
			}
			if in.parent != "" && (n.Parent() == nil || n.Parent().Kind() != in.parent) {
				continue
			}
			lang := in.lang
			if lang == langFence {
				lang = fenceLang(n, src)
			}
			if lang != "" {
				ranges[lang] = append(ranges[lang], n.Range())
			}
			matched = true
			break
		}
		// Descend only into nodes overlapping the viewport, never into
		// injected content itself.
		if !matched && inView && c.GotoFirstChild() {
			continue
		}
		for {
			if c.GotoNextSibling() {
				break
			}
			if !c.GotoParent() {
				ok = false
				break
			}
		}
	}
	for name, ch := range h.children {
		if _, live := ranges[name]; !live && ch.tree != nil {
			ch.tree.Close()
			ch.tree = nil
		}
	}
	for name, rs := range ranges {
		ch := h.children[name]
		if ch == nil {
			def, known := langs[name]
			if !known {
				continue
			}
			p, q, classes, ok := compile(def())
			if !ok {
				continue
			}
			ch = &child{parser: p, query: q, classes: classes}
			h.children[name] = ch
		}
		if ch.parser.SetIncludedRanges(rs) != nil {
			continue
		}
		if ch.tree != nil {
			ch.tree.Close()
		}
		ch.tree = ch.parser.Parse(src, nil)
	}
}

// fenceLang resolves a fenced code block's language from its info string
// (```go, ```js …); "" when unknown or absent.
func fenceLang(content *ts.Node, src []byte) string {
	block := content.Parent()
	for i := uint(0); i < block.NamedChildCount(); i++ {
		n := block.NamedChild(i)
		if n.Kind() != "info_string" {
			continue
		}
		info := string(src[n.StartByte():n.EndByte()])
		word, _, _ := strings.Cut(strings.TrimSpace(info), " ")
		return fenceLangs[strings.ToLower(word)]
	}
	return ""
}

// Edit implements editor.Syntax: feed the change to the old tree and mark
// it for lazy reparse on the next Spans call.
func (h *Highlighter) Edit(startOff, oldEndOff, newEndOff int, start, oldEnd, newEnd [2]int) {
	h.tree.Edit(&ts.InputEdit{
		StartByte:      uint(startOff),
		OldEndByte:     uint(oldEndOff),
		NewEndByte:     uint(newEndOff),
		StartPosition:  ts.Point{Row: uint(start[0]), Column: uint(start[1])},
		OldEndPosition: ts.Point{Row: uint(oldEnd[0]), Column: uint(oldEnd[1])},
		NewEndPosition: ts.Point{Row: uint(newEnd[0]), Column: uint(newEnd[1])},
	})
	h.stale = true
}

// Expand implements editor.Syntax: the smallest named node strictly
// containing [lo, hi) — structural selection.
func (h *Highlighter) Expand(src []byte, lo, hi int) (int, int, bool) {
	h.refresh(src)
	node := h.tree.RootNode().NamedDescendantForByteRange(uint(lo), uint(hi))
	for node != nil {
		nlo, nhi := int(node.StartByte()), int(node.EndByte())
		if nlo < lo || nhi > hi {
			return nlo, nhi, true
		}
		node = node.Parent()
	}
	return 0, 0, false
}

func (h *Highlighter) refresh(src []byte) {
	if h.stale {
		newTree := h.parser.Parse(src, h.tree)
		h.tree.Close()
		h.tree = newTree
		h.stale = false
		h.childStale = true
	}
}

// Spans implements editor.Syntax: reparse if stale, then query the visible
// byte range only.
func (h *Highlighter) Spans(src []byte, startOff, endOff int) []editor.HLSpan {
	h.refresh(src)
	spans := querySpans(h.query, h.classes, h.tree, src, startOff, endOff, nil)
	if len(h.inj) > 0 && (h.childStale || startOff != h.childLo || endOff != h.childHi) {
		h.parseChildren(src, startOff, endOff)
		h.childStale, h.childLo, h.childHi = false, startOff, endOff
	}
	for _, ch := range h.children {
		if ch.tree != nil {
			spans = querySpans(ch.query, ch.classes, ch.tree, src, startOff, endOff, spans)
		}
	}
	return spans
}

func querySpans(q *ts.Query, classes []int, tree *ts.Tree, src []byte, startOff, endOff int, spans []editor.HLSpan) []editor.HLSpan {
	qc := ts.NewQueryCursor()
	defer qc.Close()
	qc.SetByteRange(uint(startOff), uint(endOff))
	matches := qc.Matches(q, tree.RootNode(), src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		for _, c := range m.Captures {
			class := classes[c.Index]
			if class == editor.ClassNone {
				continue
			}
			spans = append(spans, editor.HLSpan{
				Start: int(c.Node.StartByte()),
				End:   int(c.Node.EndByte()),
				Class: class,
			})
		}
	}
	return spans
}
