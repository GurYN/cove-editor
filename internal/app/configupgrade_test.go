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
	for _, want := range []string{"# [update]", "# [files]", "# [keys]", "# [colors]", "# [lsp.zig]", "# [apps.redis]", "# keymap ="} {
		if !strings.Contains(got, want) {
			t.Errorf("missing appended block for %q", want)
		}
	}
	if strings.Contains(got, "# theme =") || strings.Contains(got, "# [editor]") {
		t.Error("re-appended a block the user already has")
	}

	upgradeSampleConfig(p) // idempotent: nothing new the second time
	again, _ := os.ReadFile(p)
	if string(again) != got {
		t.Error("second upgrade changed the file")
	}
	if strings.Count(got, "# Options added by newer Cove builds:") != 1 {
		t.Error("header should appear exactly once")
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
