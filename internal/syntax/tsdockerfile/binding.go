// Package tsdockerfile vendors tree-sitter-dockerfile v0.2.0
// (github.com/camdencheek/tree-sitter-dockerfile, MIT — see LICENSE): upstream's
// Go bindings module declares a broken module path and targets the old smacker
// tree-sitter library. src/ is a verbatim copy of the generated tree; keep
// diffs at zero and re-vendor to upgrade.
package tsdockerfile

// #cgo CFLAGS: -std=c11 -fPIC
// #include "src/parser.c"
// #include "src/scanner.c"
import "C"

import "unsafe"

// Language returns the Dockerfile grammar.
func Language() unsafe.Pointer {
	return unsafe.Pointer(C.tree_sitter_dockerfile())
}
