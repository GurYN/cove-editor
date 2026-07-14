package config

import "maps"

// Built-in themes. Truecolor hex; lipgloss degrades to 256/16 colors on
// lesser terminals automatically.
var themes = map[string]map[string]string{
	"cove-dark": {
		"keyword": "#c586c0", "string": "#98c379", "comment": "#7f848e",
		"number": "#d19a66", "function": "#61afef", "type": "#56b6c2",
		"constant": "#e5c07b", "property": "#abb2bf", "operator": "#9da5b4",
		"info": "#61afef", "warning": "#e5c07b", "error": "#e06c75",
		"match": "#4b4b1f", "selection": "#264f78",
		"ui.bg": "#2c3244", "ui.fg": "#9da5b4", "ui.border": "#4a5166",
		"gutter":    "#525a6e",
		"git.added": "#98c379", "git.modified": "#e5c07b",
		"git.deleted": "#e06c75", "git.conflict": "#c586c0",
	},
	"cove-light": {
		"keyword": "#af00db", "string": "#0a7a33", "comment": "#8a949e",
		"number": "#b25a00", "function": "#0057b8", "type": "#267f99",
		"constant": "#9a6a00", "property": "#3b4252", "operator": "#5c6773",
		"info": "#0057b8", "warning": "#9a6a00", "error": "#c72e2e",
		"match": "#f3e8a2", "selection": "#add6ff",
		"ui.bg": "#dfe3ea", "ui.fg": "#5c6773", "ui.border": "#b8bfcc",
		"gutter":    "#aab2c0",
		"git.added": "#0a7a33", "git.modified": "#9a6a00",
		"git.deleted": "#c72e2e", "git.conflict": "#af00db",
	},
}

// ThemeColors resolves the configured theme plus per-key overrides.
func (c Config) ThemeColors() map[string]string {
	base, ok := themes[c.Theme]
	if !ok {
		base = themes["cove-dark"]
	}
	out := make(map[string]string, len(base))
	maps.Copy(out, base)
	maps.Copy(out, c.Colors)
	return out
}
