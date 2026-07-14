package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGopls exercises the full client against a real gopls: initialize,
// didOpen, streamed diagnostics, definition, rename. Skipped when gopls is
// not installed (CI installs it).
func TestGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}
	write("go.mod", "module example.com/x\n\ngo 1.22\n")
	mainGo := write("main.go", `package main

func greet() string { return "hi" }

func main() {
	println(greet())
	var unused int
}
`)

	m := NewManager(dir)
	defer m.Shutdown()
	if !m.Open(mainGo, mustRead(t, mainGo), 1) {
		t.Fatal("Open returned false")
	}

	// Diagnostics: the unused variable must arrive via the event channel.
	deadline := time.After(60 * time.Second)
	var diags []Diagnostic
wait:
	for {
		select {
		case ev := <-m.Events():
			if ev.Kind == "diagnostics" && len(ev.Diagnostics) > 0 {
				diags = ev.Diagnostics
				break wait
			}
		case <-deadline:
			t.Fatal("no diagnostics within 60s")
		}
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "unused") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'unused' diagnostic, got %+v", diags)
	}

	c := m.Client(mainGo)
	if c == nil {
		t.Fatal("no client")
	}
	uri := PathToURI(mainGo)

	// Definition of greet() at the call site (line 5, "greet" in println).
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	locs, err := c.Definition(ctx, uri, Position{Line: 5, Character: 10})
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	if len(locs) == 0 || locs[0].Range.Start.Line != 2 {
		t.Fatalf("definition = %+v, want line 2", locs)
	}

	// Rename greet -> hello.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()
	we, err := c.Rename(ctx2, uri, Position{Line: 2, Character: 5}, "hello")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if we == nil || len(we.Changes) == 0 {
		t.Fatalf("rename returned no changes: %+v", we)
	}
	edits := 0
	for _, es := range we.Changes {
		edits += len(es)
	}
	if edits < 2 { // definition + call site
		t.Fatalf("rename edits = %d, want >= 2", edits)
	}

	// Hover over greet.
	ctx3, cancel3 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel3()
	text, err := c.Hover(ctx3, uri, Position{Line: 2, Character: 5})
	if err != nil {
		t.Fatalf("hover: %v", err)
	}
	if !strings.Contains(text, "greet") {
		t.Fatalf("hover = %q", text)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
