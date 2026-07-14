// Package syntax wraps tree-sitter: incremental parsing and highlight spans.
// All CGo is confined here; the editor sees only the editor.Syntax interface.
package syntax

import (
	_ "embed"
	"path/filepath"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
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

type langDef struct {
	lang  *ts.Language
	query string
}

var langs = map[string]func() langDef{
	".go":   func() langDef { return langDef{ts.NewLanguage(tsgo.Language()), goScm} },
	".json": func() langDef { return langDef{ts.NewLanguage(tsjson.Language()), jsonScm} },
	".py":   func() langDef { return langDef{ts.NewLanguage(tspython.Language()), pythonScm} },
	".rs":   func() langDef { return langDef{ts.NewLanguage(tsrust.Language()), rustScm} },
	".ts":   func() langDef { return langDef{ts.NewLanguage(tstypescript.LanguageTypescript()), typescriptScm} },
	".tsx":  func() langDef { return langDef{ts.NewLanguage(tstypescript.LanguageTSX()), typescriptScm} },
	".js":   func() langDef { return langDef{ts.NewLanguage(tstypescript.LanguageTypescript()), typescriptScm} },
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
}

// New returns a highlighter for the file at path, or nil if the language is
// not supported.
func New(path string, src []byte) *Highlighter {
	def, ok := langs[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil
	}
	d := def()
	q, qerr := ts.NewQuery(d.lang, d.query)
	if qerr != nil {
		return nil
	}
	p := ts.NewParser()
	if err := p.SetLanguage(d.lang); err != nil {
		return nil
	}
	h := &Highlighter{parser: p, query: q}
	for _, name := range q.CaptureNames() {
		h.classes = append(h.classes, classFor(name))
	}
	h.tree = p.Parse(src, nil)
	return h
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
	}
}

// Spans implements editor.Syntax: reparse if stale, then query the visible
// byte range only.
func (h *Highlighter) Spans(src []byte, startOff, endOff int) []editor.HLSpan {
	h.refresh(src)
	qc := ts.NewQueryCursor()
	defer qc.Close()
	qc.SetByteRange(uint(startOff), uint(endOff))
	var spans []editor.HLSpan
	matches := qc.Matches(h.query, h.tree.RootNode(), src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		for _, c := range m.Captures {
			class := h.classes[c.Index]
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
