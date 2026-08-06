package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeSampleConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	old := "theme = \"cove-light\"\n\n[editor]\ntab_size = 2\n"
	os.WriteFile(p, []byte(old), 0o644)

	upgradeSampleConfig(p)
	data, _ := os.ReadFile(p)
	got := string(data)
	if !strings.HasPrefix(got, old) {
		t.Fatal("existing content was rewritten")
	}
	for _, want := range []string{"# [update]", "# [files]", "# [git]", "# [keys]", "# [colors]", "# [lsp.zig]", "# [apps.redis]", "# [ai]", "# keymap ="} {
		if !strings.Contains(got, want) {
			t.Errorf("missing appended block for %q", want)
		}
	}
	if strings.Contains(got, "# theme =") {
		t.Error("re-appended a block the user already has")
	}
	// per-key: [editor] exists but lacks line_numbers/confirm_quit → those
	// keys appended under a fresh header, without duplicating tab_size
	if !strings.Contains(got, "# [editor]") || !strings.Contains(got, "# line_numbers") {
		t.Error("partial [editor] table did not get its missing keys appended")
	}
	if strings.Count(got, "tab_size") != 1 {
		t.Error("appended a commented duplicate of a key the user already has")
	}

	upgradeSampleConfig(p) // idempotent: nothing new the second time
	again, _ := os.ReadFile(p)
	if string(again) != got {
		t.Error("second upgrade changed the file")
	}
	if strings.Count(got, "# Options added by newer Cove builds:") != 1 {
		t.Error("header should appear exactly once")
	}

	// per-key inside a fixed table: [git] view present, diff_style missing
	gp := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(gp, []byte("[git]\nview = \"tree\"\n"), 0o644)
	upgradeSampleConfig(gp)
	gdata, _ := os.ReadFile(gp)
	if !strings.Contains(string(gdata), "# diff_style =") {
		t.Error("missing diff_style not appended to a config that has [git] view")
	}
	if strings.Count(string(gdata), "view =") != 1 {
		t.Error("appended a commented duplicate of [git] view")
	}
	upgradeSampleConfig(gp) // and stable afterwards
	gagain, _ := os.ReadFile(gp)
	if string(gagain) != string(gdata) {
		t.Error("per-key upgrade not idempotent")
	}
	// map tables stay table-level: a configured server suppresses the sample
	lp := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(lp, []byte(sampleConfig+"\n[lsp.go]\ncommand = [\"gopls\"]\n"), 0o644)
	upgradeSampleConfig(lp)
	ldata, _ := os.ReadFile(lp)
	if strings.Count(string(ldata), "# [lsp.zig]") != 1 {
		t.Error("[lsp.zig] sample re-appended despite a configured server")
	}

	// a fresh sampleConfig file is already complete
	full := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(full, []byte(sampleConfig), 0o644)
	upgradeSampleConfig(full)
	after, _ := os.ReadFile(full)
	if string(after) != sampleConfig {
		t.Error("upgrade modified a pristine sample config")
	}
}
