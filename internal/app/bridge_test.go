package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GurYN/cove-editor/internal/bridge"
	"github.com/GurYN/cove-editor/internal/lsp"
)

// pumpBridge stands in for the bubbletea loop: it services bridge
// requests (and language-server events) against m until stop is called.
// Tests must not touch m while the pump runs — go through the pump.
func pumpBridge(m *Model) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case req := <-m.bridgeReqs:
				req.reply <- req.fn(m)
			case ev := <-m.lspm.Events():
				m.handleLSPEvent(ev)
			case <-done:
				return
			}
		}
	}()
	return func() { close(done); <-finished }
}

func call(t *testing.T, h *bridgeHandler, tool string, args string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := h.Call(ctx, tool, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s(%s): %v", tool, args, err)
	}
	data, _ := json.Marshal(res)
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return map[string]any{"text": res}
	}
	return out
}

func bridgeSetup(t *testing.T) (Model, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {\n\tgreet(\"héllo\")\n}\n"), 0o644)
	os.WriteFile(filepath.Join(root, "greet.go"), []byte("package main\n\nfunc greet(s string) { println(s) }\n"), 0o644)
	m := New(root, nil)
	return m, root
}

// TestBridgePositions: 1-based character columns round-trip through LSP's
// UTF-16 positions across multibyte text.
func TestBridgePositions(t *testing.T) {
	lines := [][]byte{[]byte("héllo 😀 x"), []byte("")}
	p, err := lspPos(lines, 1, 9)                      // the "x"
	if err != nil || p.Line != 0 || p.Character != 9 { // é = 1 unit, 😀 = 2 units
		t.Fatalf("lspPos = %+v, %v", p, err)
	}
	if l, c := userPos(lines, p); l != 1 || c != 9 {
		t.Fatalf("userPos = %d:%d", l, c)
	}
	if _, err := lspPos(lines, 3, 1); err == nil {
		t.Fatal("out-of-range line accepted")
	}
	if p, _ := lspPos(lines, 1, 0); p.Character != 0 {
		t.Fatalf("column 0 should mean 1: %+v", p)
	}
}

// TestBridgeOpenFiles: the active file comes first with cursor, selection
// and unsaved state; virtual tabs are left out.
func TestBridgeOpenFiles(t *testing.T) {
	m, root := bridgeSetup(t)
	m.openFile(filepath.Join(root, "greet.go"))
	m.openFile(filepath.Join(root, "main.go"))
	m.openVirtual("x (diff)", "diff")
	m.active = 1 // main.go
	d := m.doc()
	d.ed.Go(3, 0)
	d.ed.MoveV(1, true) // whole line 4 (1-based) selected; cursor on line 5
	stop := pumpBridge(&m)
	out := call(t, m.bridgeH, "open_files", "")
	stop()
	files := out["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	f := files[0].(map[string]any)
	if f["path"] != "main.go" || f["active"] != true || f["cursor_line"].(float64) != 5 {
		t.Fatalf("active = %v", f)
	}
	if f["selection_start_line"].(float64) != 4 || f["selection_end_line"].(float64) != 4 || !strings.Contains(f["selection"].(string), "greet") {
		t.Fatalf("selection = %v", f)
	}
	if files[1].(map[string]any)["path"] != "greet.go" {
		t.Fatalf("second = %v", files[1])
	}
}

// TestBridgeDiagnosticsFromPublished: what a server published is answered
// straight from the model — for one path and for all — with 1-based
// character columns and severity names; a language without a server errors.
func TestBridgeDiagnosticsFromPublished(t *testing.T) {
	m, root := bridgeSetup(t)
	main := filepath.Join(root, "main.go")
	m.openFile(main)
	m.lspStatus["go"] = "ready"
	m.handleLSPEvent(lsp.Event{Kind: "diagnostics", URI: lsp.PathToURI(main), Diagnostics: []lsp.Diagnostic{
		{Range: lsp.Range{Start: lsp.Position{Line: 3, Character: 7}, End: lsp.Position{Line: 3, Character: 14}}, Severity: 1, Message: "undefined: greet", Source: "compiler"},
	}})
	if len(m.doc().ed.Diags) != 1 || len(m.diags) != 1 {
		t.Fatalf("diagnostics not stored: ed=%d map=%d", len(m.doc().ed.Diags), len(m.diags))
	}
	stop := pumpBridge(&m)
	out := call(t, m.bridgeH, "diagnostics", `{"path":"main.go"}`)
	rows := out["diagnostics"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	r := rows[0].(map[string]any)
	if r["path"] != "main.go" || r["line"].(float64) != 4 || r["column"].(float64) != 8 || r["end_column"].(float64) != 15 || r["severity"] != "error" || r["source"] != "compiler" {
		t.Fatalf("row = %v", r)
	}
	all := call(t, m.bridgeH, "diagnostics", "")
	if len(all["diagnostics"].([]any)) != 1 {
		t.Fatalf("all = %v", all)
	}
	stop()
	// Open file, server ready, nothing published: clean, answered without a
	// server round trip (and without a server binary at all).
	m.openFile(filepath.Join(root, "greet.go"))
	stop = pumpBridge(&m)
	if rows := call(t, m.bridgeH, "diagnostics", `{"path":"greet.go"}`)["diagnostics"].([]any); len(rows) != 0 {
		t.Fatalf("clean open file: %v", rows)
	}
	stop()
	if _, err := m.bridgeH.Call(context.Background(), "diagnostics", json.RawMessage(`{"path":"notes.txt"}`)); err == nil || !strings.Contains(err.Error(), "no language server") {
		t.Fatalf("txt: %v", err)
	}
	// A publish with no diagnostics clears the entry.
	m.handleLSPEvent(lsp.Event{Kind: "diagnostics", URI: lsp.PathToURI(main)})
	if len(m.diags) != 0 {
		t.Fatal("empty publish did not clear the map")
	}
}

// TestBridgeOverProtocol: the real handler behind bridge.Serve lists every
// tool and answers a call — what an agent sees end to end.
func TestBridgeOverProtocol(t *testing.T) {
	m, root := bridgeSetup(t)
	m.openFile(filepath.Join(root, "main.go"))
	stop := pumpBridge(&m)
	defer stop()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { bridge.Serve(context.Background(), inR, outW, m.bridgeH, "test"); outW.Close() }()
	defer inW.Close()
	br := bufio.NewReader(outR)
	inW.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"))
	line, _ := br.ReadString('\n')
	for _, name := range []string{"open_files", "diagnostics", "definition", "references", "hover", "document_symbols", "workspace_symbols", "rename"} {
		if !strings.Contains(line, `"name":"`+name+`"`) {
			t.Fatalf("tool %s missing from %s", name, line)
		}
	}
	inW.Write([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"open_files","arguments":{}}}` + "\n"))
	line, _ = br.ReadString('\n')
	if !strings.Contains(line, `main.go`) || strings.Contains(line, `isError`) {
		t.Fatalf("open_files over protocol: %s", line)
	}
}

// TestBridgeEnvReachesTerminal: once the socket is up, a panel shell
// inherits COVE_SOCKET pointing at it, and `cove mcp` can proxy through.
func TestBridgeEnvReachesTerminal(t *testing.T) {
	m, _ := bridgeSetup(t)
	m.width, m.height = 100, 30
	m.layout()
	sock := filepath.Join(t.TempDir(), "cove-t.sock")
	srv, err := bridge.Listen(sock, m.bridgeH, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	m.handleBridgeStarted(bridgeStartedMsg{srv: srv})
	if cmd := m.spawnTerm([]string{"sh", "-c", "echo SOCK=$(basename \"$COVE_SOCKET\"); sleep 30"}, ""); cmd == nil {
		t.Skip("PTY unavailable")
	}
	defer m.terms[0].Close()
	waitScreen(t, &m, "SOCK="+filepath.Base(sock))

	// The proxy leg, as `cove mcp` runs it.
	t.Setenv(bridge.EnvVar, sock)
	stop := pumpBridge(&m)
	defer stop()
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"open_files"}}` + "\n")
	var out strings.Builder
	if err := bridge.Main(in, &out, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `\"root\"`) {
		t.Fatalf("proxied open_files: %q", out.String())
	}
}

// TestBridgeLiveGopls drives the language-server tools against a real
// gopls: definition and references across files (one open in Cove, one
// not), hover, symbols, diagnostics for a closed file (temporary open),
// and a rename that edits the open buffer and writes the closed file.
func TestBridgeLiveGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}
	m, root := bridgeSetup(t)
	os.WriteFile(filepath.Join(root, "broken.go"), []byte("package main\n\nfunc broken() { undefinedThing() }\n"), 0o644)
	main := filepath.Join(root, "main.go")
	m.openFile(main)
	stop := pumpBridge(&m)
	defer m.lspm.Shutdown()

	// gopls takes a moment to initialize; the first call retries on "starting".
	var def map[string]any
	deadline := time.Now().Add(60 * time.Second)
	for {
		res, err := m.bridgeH.Call(context.Background(), "definition", json.RawMessage(`{"path":"main.go","line":4,"column":2}`))
		if err == nil {
			data, _ := json.Marshal(res)
			json.Unmarshal(data, &def)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("definition never answered: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	locs := def["definition"].([]any)
	if len(locs) != 1 || locs[0].(map[string]any)["path"] != "greet.go" || locs[0].(map[string]any)["line"].(float64) != 3 {
		t.Fatalf("definition = %v", locs)
	}
	if !strings.Contains(locs[0].(map[string]any)["text"].(string), "func greet") {
		t.Fatalf("no line text: %v", locs[0])
	}

	refs := call(t, m.bridgeH, "references", `{"path":"greet.go","line":3,"column":6}`)["references"].([]any)
	if len(refs) < 1 {
		t.Fatalf("references = %v", refs)
	}
	hover := call(t, m.bridgeH, "hover", `{"path":"main.go","line":4,"column":2}`)
	if !strings.Contains(hover["text"].(string), "greet") {
		t.Fatalf("hover = %v", hover)
	}
	syms := call(t, m.bridgeH, "document_symbols", `{"path":"greet.go"}`)["symbols"].([]any)
	if len(syms) != 1 || syms[0].(map[string]any)["name"] != "greet" || syms[0].(map[string]any)["kind"] != "function" {
		t.Fatalf("symbols = %v", syms)
	}
	ws := call(t, m.bridgeH, "workspace_symbols", `{"query":"gree"}`)["symbols"].([]any)
	if len(ws) < 1 {
		t.Fatalf("workspace_symbols = %v", ws)
	}

	// broken.go is not open anywhere: the bridge opens it in gopls, waits
	// for the publish, closes it again.
	diags := call(t, m.bridgeH, "diagnostics", `{"path":"broken.go"}`)["diagnostics"].([]any)
	if len(diags) != 1 || !strings.Contains(diags[0].(map[string]any)["message"].(string), "undefinedThing") {
		t.Fatalf("diagnostics = %v", diags)
	}

	ren := call(t, m.bridgeH, "rename", `{"path":"main.go","line":4,"column":2,"new_name":"hello"}`)
	if ren["files_changed"].(float64) != 2 {
		t.Fatalf("rename = %v", ren)
	}
	stop()
	if got := string(m.doc().ed.Buf.Bytes()); !strings.Contains(got, "hello(") {
		t.Fatalf("open buffer not renamed:\n%s", got)
	}
	if m.doc().ed.Dirty {
		t.Fatal("renamed buffer left unsaved")
	}
	disk, _ := os.ReadFile(filepath.Join(root, "greet.go"))
	if !strings.Contains(string(disk), "func hello(") {
		t.Fatalf("closed file not written:\n%s", disk)
	}
	if _, err := os.Stat(filepath.Join(root, "broken.go")); err != nil {
		t.Fatal(err)
	}
}

// TestCoveCommand: a test binary is never the cove on PATH, so the
// registration must use its absolute path — the bare name would resolve
// to whatever older cove is installed.
func TestCoveCommand(t *testing.T) {
	got := coveCommand()
	exe, _ := os.Executable()
	real, _ := filepath.EvalSymlinks(exe)
	if got != real {
		t.Fatalf("coveCommand = %q, want this executable %q", got, real)
	}
}
