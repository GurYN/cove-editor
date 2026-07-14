package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Binary files (NUL in the head) must not open as editor tabs — neither via
// the finder/tree path (loadDoc) nor as the startup argument.
func TestBinaryFilesRefused(t *testing.T) {
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	bin := filepath.Join(t.TempDir(), "blob.zip")
	os.WriteFile(bin, []byte("PK\x03\x04\x00\x00junk"), 0o644)

	if _, err := loadDoc(bin); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("loadDoc accepted a binary: %v", err)
	}

	data, _ := os.ReadFile(bin)
	m := New(bin, data)
	if len(m.docs) != 0 || !strings.Contains(m.lastMsg, "binary") {
		t.Fatalf("startup opened a binary: %d docs, msg %q", len(m.docs), m.lastMsg)
	}

	// UTF-8 text stays openable.
	txt := filepath.Join(t.TempDir(), "ok.go")
	os.WriteFile(txt, []byte("package ok // héllo\n"), 0o644)
	if _, err := loadDoc(txt); err != nil {
		t.Fatalf("text file refused: %v", err)
	}
}
