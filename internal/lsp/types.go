package lsp

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Minimal LSP protocol types — only the fields Cove reads or writes.
// ponytail: hand-rolled instead of go.lsp.dev/protocol; zero deps and we
// control every field. Extend as features land.

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"` // UTF-16 code units, per the spec
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"` // 1 error, 2 warning, 3 info, 4 hint
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type CompletionItem struct {
	Label            string    `json:"label"`
	Kind             int       `json:"kind,omitempty"`
	Detail           string    `json:"detail,omitempty"`
	InsertText       string    `json:"insertText,omitempty"`
	InsertTextFormat int       `json:"insertTextFormat,omitempty"` // 2 = snippet
	TextEdit         *TextEdit `json:"textEdit,omitempty"`
	SortText         string    `json:"sortText,omitempty"`
}

// SignatureHelp is a textDocument/signatureHelp response.
type SignatureHelp struct {
	Signatures      []SignatureInfo `json:"signatures"`
	ActiveSignature int             `json:"activeSignature"`
	ActiveParameter int             `json:"activeParameter"`
}

type SignatureInfo struct {
	Label      string      `json:"label"`
	Parameters []ParamInfo `json:"parameters"`
	// Per-signature override of the top-level activeParameter (LSP 3.16).
	ActiveParameter *int `json:"activeParameter,omitempty"`
}

// ParamInfo's label is either a substring of the signature label or a
// [start, end) offset pair into it.
type ParamInfo struct {
	Label json.RawMessage `json:"label"`
}

// Active returns the active signature's label, how many signatures there
// are, and the [lo, hi) byte range of the active parameter within the label
// (lo == hi when there is none).
// ponytail: offset-pair labels are UTF-16 per spec; treated as bytes —
// signature labels are ASCII in practice.
func (sh *SignatureHelp) Active() (label string, count, lo, hi int) {
	if len(sh.Signatures) == 0 {
		return "", 0, 0, 0
	}
	si := sh.Signatures[min(max(sh.ActiveSignature, 0), len(sh.Signatures)-1)]
	ap := sh.ActiveParameter
	if si.ActiveParameter != nil {
		ap = *si.ActiveParameter
	}
	label = si.Label
	if ap < 0 || ap >= len(si.Parameters) {
		return label, len(sh.Signatures), 0, 0
	}
	raw := si.Parameters[ap].Label
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		if i := strings.Index(label, s); i >= 0 {
			return label, len(sh.Signatures), i, i + len(s)
		}
		return label, len(sh.Signatures), 0, 0
	}
	var pair [2]int
	if json.Unmarshal(raw, &pair) == nil && pair[0] >= 0 && pair[1] >= pair[0] && pair[1] <= len(label) {
		return label, len(sh.Signatures), pair[0], pair[1]
	}
	return label, len(sh.Signatures), 0, 0
}

// DocumentSymbol is one node of a textDocument/documentSymbol response.
// Kind values are the LSP SymbolKind enum (5 class, 12 function, …).
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// CodeAction is one textDocument/codeAction result. The spec allows both
// CodeAction literals and bare Commands in the same array; a bare Command
// decodes with Command as a JSON string and Arguments at the top level.
type CodeAction struct {
	Title     string          `json:"title"`
	Kind      string          `json:"kind,omitempty"`
	Edit      *WorkspaceEdit  `json:"edit,omitempty"`
	Command   json.RawMessage `json:"command,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Cmd extracts the action's server command, handling both encodings.
func (a CodeAction) Cmd() (name string, args json.RawMessage, ok bool) {
	var s string
	if json.Unmarshal(a.Command, &s) == nil && s != "" {
		return s, a.Arguments, true
	}
	var c struct {
		Command   string          `json:"command"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(a.Command, &c) == nil && c.Command != "" {
		return c.Command, c.Arguments, true
	}
	return "", nil, false
}

// WorkspaceSym is one workspace/symbol result (flat SymbolInformation).
type WorkspaceSym struct {
	Name     string   `json:"name"`
	Kind     int      `json:"kind"`
	Location Location `json:"location"`
}

type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes"`
	// Some servers send documentChanges regardless of our capabilities.
	DocumentChanges []struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Edits []TextEdit `json:"edits"`
	} `json:"documentChanges"`
}

// Normalize folds DocumentChanges into the Changes map.
func (we *WorkspaceEdit) Normalize() {
	if we.Changes == nil {
		we.Changes = map[string][]TextEdit{}
	}
	for _, dc := range we.DocumentChanges {
		if dc.TextDocument.URI != "" {
			we.Changes[dc.TextDocument.URI] = append(we.Changes[dc.TextDocument.URI], dc.Edits...)
		}
	}
	we.DocumentChanges = nil
}

// ---- URI helpers ----

func PathToURI(path string) string {
	abs, _ := filepath.Abs(path)
	return "file://" + strings.ReplaceAll(url.PathEscape(abs), "%2F", "/")
}

func URIToPath(uri string) string {
	s := strings.TrimPrefix(uri, "file://")
	if p, err := url.PathUnescape(s); err == nil {
		return p
	}
	return s
}

// ---- position conversion ----
// LSP characters count UTF-16 code units; our buffers are UTF-8 bytes.

// UTF16Col converts a byte column within line to UTF-16 units.
func UTF16Col(line []byte, byteCol int) int {
	col := 0
	for i := 0; i < byteCol && i < len(line); {
		r, size := utf8.DecodeRune(line[i:])
		col += len(utf16.Encode([]rune{r}))
		i += size
	}
	return col
}

// ByteCol converts a UTF-16 column within line to a byte offset.
func ByteCol(line []byte, utf16Col int) int {
	col := 0
	for i := 0; i < len(line); {
		if col >= utf16Col {
			return i
		}
		r, size := utf8.DecodeRune(line[i:])
		col += len(utf16.Encode([]rune{r}))
		i += size
	}
	return len(line)
}
