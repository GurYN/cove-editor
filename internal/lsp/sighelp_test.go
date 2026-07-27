package lsp

import (
	"encoding/json"
	"testing"
)

// Active must handle both parameter-label encodings: substring and
// [start, end) offsets, plus the per-signature activeParameter override.
func TestSignatureHelpActive(t *testing.T) {
	var sh SignatureHelp
	data := `{
		"signatures": [
			{"label": "f(a int, b string)", "parameters": [{"label": "a int"}, {"label": [9, 17]}]},
			{"label": "f(x int)", "parameters": [{"label": "x int"}], "activeParameter": 0}
		],
		"activeSignature": 0, "activeParameter": 1
	}`
	if err := json.Unmarshal([]byte(data), &sh); err != nil {
		t.Fatal(err)
	}
	label, count, lo, hi := sh.Active()
	if label != "f(a int, b string)" || count != 2 {
		t.Fatalf("got label %q count %d", label, count)
	}
	if label[lo:hi] != "b string" {
		t.Fatalf("offset-pair param: got %q", label[lo:hi])
	}

	sh.ActiveSignature, sh.ActiveParameter = 0, 0
	label, _, lo, hi = sh.Active()
	if label[lo:hi] != "a int" {
		t.Fatalf("substring param: got %q", label[lo:hi])
	}

	sh.ActiveSignature, sh.ActiveParameter = 1, 99 // per-sig override wins
	label, _, lo, hi = sh.Active()
	if label != "f(x int)" || label[lo:hi] != "x int" {
		t.Fatalf("override: got %q param %q", label, label[lo:hi])
	}

	empty := &SignatureHelp{}
	if l, c, _, _ := empty.Active(); l != "" || c != 0 {
		t.Fatal("empty SignatureHelp must be inert")
	}
}
