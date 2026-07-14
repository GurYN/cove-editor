package lsp

import (
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
	Label      string    `json:"label"`
	Kind       int       `json:"kind,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	InsertText string    `json:"insertText,omitempty"`
	TextEdit   *TextEdit `json:"textEdit,omitempty"`
	SortText   string    `json:"sortText,omitempty"`
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
