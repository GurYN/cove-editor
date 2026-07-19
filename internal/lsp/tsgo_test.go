package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTS7PullDiagnostics exercises the full client against the native
// TypeScript 7 server (`tsc --lsp`), which only serves diagnostics via the
// pull model. Skipped when tsc is absent or predates --lsp support.
func TestTS7PullDiagnostics(t *testing.T) {
	if _, err := exec.LookPath("tsc"); err != nil {
		t.Skip("tsc not installed")
	}
	if out, err := exec.Command("tsc", "--version").Output(); err != nil ||
		!strings.HasPrefix(strings.TrimSpace(string(out)), "Version 7") {
		t.Skipf("tsc without native LSP: %s", out)
	}
	dir := t.TempDir()
	appJS := filepath.Join(dir, "app.js")
	src := "const x = 1;\nx = 2;\nconsole.lgo(x);\n"
	os.WriteFile(appJS, []byte(src), 0o644)

	m := NewManager(dir)
	defer m.Shutdown()
	if !m.Open(appJS, []byte(src), 1) {
		t.Fatal("Open returned false")
	}

	// The assignment-to-const error must arrive without any server push.
	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev := <-m.Events():
			if ev.Kind != "diagnostics" || len(ev.Diagnostics) == 0 {
				continue
			}
			for _, d := range ev.Diagnostics {
				if strings.Contains(d.Message, "constant") || strings.Contains(d.Message, "read-only") {
					return
				}
			}
			t.Logf("diagnostics without the const error yet: %+v", ev.Diagnostics)
		case <-deadline:
			t.Fatal("no pulled diagnostics within 30s")
		}
	}
}
