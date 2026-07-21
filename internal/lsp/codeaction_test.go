package lsp

import (
	"encoding/json"
	"testing"
)

// The spec allows codeAction results to mix CodeAction literals and bare
// Commands; Cmd() must read the command out of both encodings.
func TestCodeActionCmdEncodings(t *testing.T) {
	var literal CodeAction
	json.Unmarshal([]byte(`{"title":"Fix import","kind":"quickfix",
		"command":{"command":"gopls.apply_fix","arguments":[{"Fix":"x"}]}}`), &literal)
	name, args, ok := literal.Cmd()
	if !ok || name != "gopls.apply_fix" || len(args) == 0 {
		t.Fatalf("literal: name=%q ok=%v args=%s", name, ok, args)
	}

	var bare CodeAction
	json.Unmarshal([]byte(`{"title":"Organize","command":"ts.organizeImports","arguments":[1]}`), &bare)
	name, args, ok = bare.Cmd()
	if !ok || name != "ts.organizeImports" || string(args) != "[1]" {
		t.Fatalf("bare: name=%q ok=%v args=%s", name, ok, args)
	}

	var editOnly CodeAction
	json.Unmarshal([]byte(`{"title":"Inline","edit":{"changes":{}}}`), &editOnly)
	if _, _, ok := editOnly.Cmd(); ok {
		t.Fatal("edit-only action must not report a command")
	}
	if editOnly.Edit == nil {
		t.Fatal("edit not decoded")
	}
}
