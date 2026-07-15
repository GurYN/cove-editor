// Package tsmarkdown vendors tree-sitter-markdown v0.5.3
// (github.com/tree-sitter-grammars/tree-sitter-markdown, MIT — see LICENSE):
// upstream ships no Go bindings. block/ and inline/ are verbatim copies of the
// generated src/ trees; keep diffs at zero and re-vendor to upgrade.
package tsmarkdown

// #cgo CFLAGS: -std=c11 -fPIC
// #include "block/parser.c"
// #include "block/scanner.c"
import "C"

import "unsafe"

// Language returns the block grammar (document structure).
func Language() unsafe.Pointer {
	return unsafe.Pointer(C.tree_sitter_markdown())
}
