package tsmarkdown

// #cgo CFLAGS: -std=c11 -fPIC
// #include "inline/parser.c"
// #include "inline/scanner.c"
import "C"

import "unsafe"

// InlineLanguage returns the inline grammar (emphasis, links, code spans),
// meant to be injected into the block grammar's inline nodes.
func InlineLanguage() unsafe.Pointer {
	return unsafe.Pointer(C.tree_sitter_markdown_inline())
}
