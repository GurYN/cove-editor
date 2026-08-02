package lsp

import "testing"

func TestLangIDFor(t *testing.T) {
	cases := map[string]string{
		"a.ts":     "typescript",
		"a.tsx":    "typescriptreact",
		"a.jsx":    "javascriptreact",
		"a.js":     "javascript",
		"a.mjs":    "javascript",
		"a.cjs":    "javascript",
		"a.mts":    "typescript",
		"a.cts":    "typescript",
		"a.go":     "go",
		"a.tf":     "terraform",
		"a.tfvars": "terraform-vars",
	}
	for path, want := range cases {
		if got := langIDFor(path, LangFor(path)); got != want {
			t.Errorf("langIDFor(%q) = %q, want %q", path, got, want)
		}
	}
}
