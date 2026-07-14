package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "nope.toml"))
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Theme != "cove-dark" || c.Keymap != "default" || c.Editor.TabSize != 4 {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if c.ThemeColors()["keyword"] == "" {
		t.Fatal("theme did not resolve")
	}
}

func TestLoadFullConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte(`
theme = "cove-light"
keymap = "vim"

[editor]
tab_size = 2

[keys]
"file.save" = "f5"

[colors]
keyword = "#ff0000"

[lsp.zig]
command = ["zls"]
extensions = [".zig"]
`), 0o644)
	t.Setenv("COVE_CONFIG", p)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Keymap != "vim" || c.Editor.TabSize != 2 || c.Keys["file.save"] != "f5" {
		t.Fatalf("parsed wrong: %+v", c)
	}
	colors := c.ThemeColors()
	if colors["keyword"] != "#ff0000" {
		t.Fatalf("override lost: %q", colors["keyword"])
	}
	if colors["string"] != themes["cove-light"]["string"] {
		t.Fatal("base theme not cove-light")
	}
	if len(c.LSP["zig"].Command) != 1 {
		t.Fatalf("lsp server not parsed: %+v", c.LSP)
	}
}

func TestBrokenConfigStillStarts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("theme = [broken"), 0o644)
	t.Setenv("COVE_CONFIG", p)
	c, err := Load()
	if err == nil {
		t.Fatal("expected parse error")
	}
	if c.Theme != "cove-dark" {
		t.Fatal("defaults not preserved on error")
	}
}
