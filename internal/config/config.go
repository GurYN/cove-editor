// Package config loads Cove's TOML configuration: theme, keymap,
// keybinding overrides, editor prefs, and language-server registration.
package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Theme  string `toml:"theme"`  // "cove-dark" (default) | "cove-light"
	Keymap string `toml:"keymap"` // "default" | "vim"

	Editor struct {
		TabSize     int  `toml:"tab_size"`
		LineNumbers bool `toml:"line_numbers"`
		ConfirmQuit bool `toml:"confirm_quit"`
	} `toml:"editor"`

	// Keys maps action IDs to key overrides: "file.save" = "ctrl+s".
	// An empty string unbinds (palette-only).
	Keys map[string]string `toml:"keys"`

	// LSP maps a language to its server: [lsp.go] command = ["gopls"].
	LSP map[string]Server `toml:"lsp"`

	// Colors overrides theme entries with hex or ANSI-256 values:
	// [colors] keyword = "#c586c0".
	Colors map[string]string `toml:"colors"`
}

type Server struct {
	Command    []string `toml:"command"`
	Extensions []string `toml:"extensions"`
	LangID     string   `toml:"language_id"`
}

// Path returns the config file location, honoring COVE_CONFIG and
// XDG_CONFIG_HOME.
func Path() string {
	if p := os.Getenv("COVE_CONFIG"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "cove", "config.toml")
}

// Load reads the config; a missing file yields defaults, a broken file
// yields defaults plus the error (the app must still start).
func Load() (Config, error) {
	var c Config
	c.Theme = "cove-dark"
	c.Keymap = "default"
	c.Editor.TabSize = 4
	c.Editor.LineNumbers = true // toml.Decode only overrides present keys
	c.Editor.ConfirmQuit = true
	data, err := os.ReadFile(Path())
	if err != nil {
		return c, nil // no config file is the normal case
	}
	if _, err := toml.Decode(string(data), &c); err != nil {
		return c, err
	}
	if c.Editor.TabSize <= 0 || c.Editor.TabSize > 16 {
		c.Editor.TabSize = 4
	}
	return c, nil
}
