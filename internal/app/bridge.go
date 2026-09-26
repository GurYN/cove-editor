package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/bridge"
	"github.com/GurYN/cove-editor/internal/lsp"
)

// The MCP bridge (see internal/bridge) lets the agent in the terminal
// panel ask the editor instead of the shell: diagnostics the language
// servers already published, definitions and references from a warm
// server, a rename that keeps open buffers consistent, and which files the
// user is looking at.
//
// Tool calls arrive on their own goroutines. Anything that reads or
// changes Model state runs inside the bubbletea loop via inLoop (a
// bridgeReqMsg round trip); pure language-server calls go straight to the
// client, which is goroutine-safe.

// bridgeReqMsg carries a closure into the update loop; the reply channel
// gets its result.
type bridgeReqMsg struct {
	fn    func(*Model) any
	reply chan any
}

// bridgeStartedMsg reports the socket server Init launched.
type bridgeStartedMsg struct {
	srv *bridge.Server
	err error
}

func listenBridge(ch <-chan bridgeReqMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// startBridge listens on the per-process socket; the result lands in the
// loop as bridgeStartedMsg (Init has a value receiver, so it can't set
// m.bridge itself).
func (m Model) startBridge() tea.Cmd {
	if !m.mcpEnabled {
		return nil
	}
	h := m.bridgeH
	return func() tea.Msg {
		srv, err := bridge.Listen(bridge.SocketPath(os.Getpid()), h, Version)
		return bridgeStartedMsg{srv: srv, err: err}
	}
}

func (m *Model) handleBridgeStarted(msg bridgeStartedMsg) tea.Cmd {
	if msg.err != nil {
		m.notifyErr("mcp: " + msg.err.Error())
		return nil
	}
	m.bridge = msg.srv
	return listenBridge(m.bridgeReqs)
}

// bridgeEnv is the extra environment panel terminals get so `cove mcp`
// finds this editor.
func (m *Model) bridgeEnv() []string {
	if m.bridge == nil {
		return nil
	}
	return []string{bridge.EnvVar + "=" + m.bridge.Path()}
}

// ---- handler ----

type bridgeHandler struct {
	root string
	lspm *lsp.Manager
	reqs chan bridgeReqMsg
}

var errEditorBusy = errors.New("editor did not answer in time")

// inLoop runs fn inside the update loop and returns its result.
func (h *bridgeHandler) inLoop(ctx context.Context, fn func(*Model) any) (any, error) {
	reply := make(chan any, 1)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case h.reqs <- bridgeReqMsg{fn: fn, reply: reply}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errEditorBusy
	}
	select {
	case v := <-reply:
		return v, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errEditorBusy
	}
}

func schema(props string, required ...string) json.RawMessage {
	req := "[]"
	if len(required) > 0 {
		b, _ := json.Marshal(required)
		req = string(b)
	}
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + req + `}`)
}

const posProps = `"path":{"type":"string","description":"File path, relative to the workspace root or absolute"},` +
	`"line":{"type":"integer","description":"1-based line"},` +
	`"column":{"type":"integer","description":"1-based character column (default 1)"}`

func (h *bridgeHandler) Tools() []bridge.Tool {
	return []bridge.Tool{
		{Name: "open_files", Description: "Files the user has open in the editor, with cursor position, selection and unsaved state; the active one first. Use it to know what the user is looking at.",
			InputSchema: schema("")},
		{Name: "diagnostics", Description: "Errors and warnings from the running language servers (gopls, pyright, tsc, rust-analyzer, clangd, …). With a path: that file's diagnostics, opening it in the server if needed. Without: everything published so far. Faster and more precise than rebuilding.",
			InputSchema: schema(`"path":{"type":"string","description":"File path; omit for all known diagnostics"}`)},
		{Name: "definition", Description: "Go to definition of the symbol at a position, via the language server.",
			InputSchema: schema(posProps, "path", "line")},
		{Name: "references", Description: "All references to the symbol at a position across the workspace, via the language server (exact, unlike grep).",
			InputSchema: schema(posProps, "path", "line")},
		{Name: "hover", Description: "Type signature and documentation of the symbol at a position.",
			InputSchema: schema(posProps, "path", "line")},
		{Name: "document_symbols", Description: "Outline of a file: functions, types, methods, fields with their lines.",
			InputSchema: schema(`"path":{"type":"string"}`, "path")},
		{Name: "workspace_symbols", Description: "Find symbols by name across the workspace (fuzzy, via the language server).",
			InputSchema: schema(`"query":{"type":"string"}`, "query")},
		{Name: "rename", Description: "Rename the symbol at a position everywhere it is used, through the language server. Files open in the editor are edited in place (undoable) and saved; others are written to disk.",
			InputSchema: schema(posProps+`,"new_name":{"type":"string"}`, "path", "line", "new_name")},
	}
}

func (h *bridgeHandler) Call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	var a struct {
		Path    string `json:"path"`
		Line    int    `json:"line"`
		Column  int    `json:"column"`
		Query   string `json:"query"`
		NewName string `json:"new_name"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("arguments: %v", err)
		}
	}
	switch name {
	case "open_files":
		return h.openFiles(ctx)
	case "diagnostics":
		return h.diagnostics(ctx, a.Path)
	case "definition", "references":
		return h.locations(ctx, name, a.Path, a.Line, a.Column)
	case "hover":
		return h.hover(ctx, a.Path, a.Line, a.Column)
	case "document_symbols":
		return h.documentSymbols(ctx, a.Path)
	case "workspace_symbols":
		return h.workspaceSymbols(ctx, a.Query)
	case "rename":
		return h.rename(ctx, a.Path, a.Line, a.Column, a.NewName)
	}
	return nil, errors.New("unknown tool: " + name)
}

// ---- paths and positions ----

func (h *bridgeHandler) abs(path string) (string, error) {
	if path == "" {
		return "", errors.New("path required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.root, path)
	}
	return filepath.Clean(path), nil
}

func (h *bridgeHandler) rel(abs string) string {
	if r, err := filepath.Rel(h.root, abs); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return abs
}

// fileLines returns a file's lines as the language server sees them: the
// open buffer when the user has it open (unsaved edits included), else disk.
func (h *bridgeHandler) fileLines(ctx context.Context, abs string) ([][]byte, error) {
	v, err := h.inLoop(ctx, func(m *Model) any {
		if d := m.docByPath(abs); d != nil && !d.virtual {
			return d.ed.Buf.Bytes()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	data, _ := v.([]byte)
	if data == nil {
		data, err = os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	}
	return bytes.Split(data, []byte("\n")), nil
}

// lspPos converts a 1-based line and 1-based character column to an LSP
// position (0-based line, UTF-16 column).
func lspPos(lines [][]byte, line, column int) (lsp.Position, error) {
	if line < 1 || line > len(lines) {
		return lsp.Position{}, fmt.Errorf("line %d out of range (file has %d lines)", line, len(lines))
	}
	l := lines[line-1]
	if column < 1 {
		column = 1
	}
	byteCol := 0
	for i := 1; i < column && byteCol < len(l); i++ {
		_, size := utf8.DecodeRune(l[byteCol:])
		byteCol += size
	}
	return lsp.Position{Line: line - 1, Character: lsp.UTF16Col(l, byteCol)}, nil
}

// userPos converts an LSP position back to 1-based line and character column.
func userPos(lines [][]byte, p lsp.Position) (line, column int) {
	if p.Line < 0 || p.Line >= len(lines) {
		return p.Line + 1, p.Character + 1
	}
	l := lines[p.Line]
	return p.Line + 1, utf8.RuneCount(l[:lsp.ByteCol(l, p.Character)]) + 1
}

// lineCache reads each file once per tool call.
type lineCache struct {
	h     *bridgeHandler
	ctx   context.Context
	lines map[string][][]byte
}

func (c *lineCache) get(abs string) [][]byte {
	if c.lines == nil {
		c.lines = map[string][][]byte{}
	}
	if l, ok := c.lines[abs]; ok {
		return l
	}
	l, _ := c.h.fileLines(c.ctx, abs)
	c.lines[abs] = l
	return l
}

type location struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
	Text      string `json:"text,omitempty"` // the line, trimmed
}

func (c *lineCache) loc(uri string, r lsp.Range) location {
	abs := lsp.URIToPath(uri)
	lines := c.get(abs)
	l := location{Path: c.h.rel(abs)}
	l.Line, l.Column = userPos(lines, r.Start)
	l.EndLine, l.EndColumn = userPos(lines, r.End)
	if r.Start.Line >= 0 && r.Start.Line < len(lines) {
		l.Text = strings.TrimSpace(string(lines[r.Start.Line]))
	}
	return l
}

// ---- server access ----

// client returns the language server for abs, with a clear error when the
// language has none or it isn't installed.
func (h *bridgeHandler) client(abs string) (*lsp.Client, error) {
	if lsp.LangFor(abs) == "" {
		return nil, fmt.Errorf("no language server configured for %s files", filepath.Ext(abs))
	}
	c := h.lspm.Client(abs)
	if c == nil {
		return nil, fmt.Errorf("language server for %s is not installed or failed to start (see Cove's status bar)", lsp.LangFor(abs))
	}
	return c, nil
}

// ensureOpen announces a file the user doesn't have open to its server
// (some servers only answer for open documents) and returns the matching
// close. The close is skipped if the user opened the file meanwhile.
func (h *bridgeHandler) ensureOpen(ctx context.Context, abs string) (func(), error) {
	v, err := h.inLoop(ctx, func(m *Model) any { return m.docByPath(abs) != nil })
	if err != nil {
		return nil, err
	}
	if v.(bool) {
		return func() {}, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	h.lspm.Open(abs, data, 1)
	return func() {
		h.inLoop(context.Background(), func(m *Model) any {
			if m.docByPath(abs) == nil {
				h.lspm.Close(abs)
			}
			return nil
		})
	}, nil
}

// ---- tools ----

func (h *bridgeHandler) openFiles(ctx context.Context) (any, error) {
	type openFile struct {
		Path      string `json:"path"`
		Active    bool   `json:"active,omitempty"`
		Unsaved   bool   `json:"unsaved,omitempty"`
		Line      int    `json:"cursor_line"`
		Column    int    `json:"cursor_column"`
		SelStart  int    `json:"selection_start_line,omitempty"`
		SelEnd    int    `json:"selection_end_line,omitempty"`
		Selection string `json:"selection,omitempty"`
	}
	v, err := h.inLoop(ctx, func(m *Model) any {
		var out []openFile
		for i, d := range m.docs {
			if d.virtual {
				continue
			}
			f := openFile{Path: h.rel(d.path), Active: i == m.active, Unsaved: d.ed.Dirty}
			line, col := d.ed.Cursor()
			f.Line, f.Column = line+1, utf8.RuneCount(d.ed.Buf.Line(line)[:min(col, len(d.ed.Buf.Line(line)))])+1
			if lo, hi := d.ed.SelectionRange(); lo != hi {
				sl, _ := d.ed.Buf.Pos(lo)
				el, ec := d.ed.Buf.Pos(hi)
				if ec == 0 && el > sl {
					el--
				}
				f.SelStart, f.SelEnd = sl+1, el+1
				if sel := d.ed.Buf.Slice(lo, hi); len(sel) <= 4000 {
					f.Selection = string(sel)
				}
			}
			if f.Active {
				out = append([]openFile{f}, out...)
			} else {
				out = append(out, f)
			}
		}
		return out
	})
	if err != nil {
		return nil, err
	}
	files, _ := v.([]openFile)
	if files == nil {
		files = []openFile{}
	}
	return map[string]any{"root": h.root, "files": files}, nil
}

type diagRow struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Source    string `json:"source,omitempty"`
}

func severityName(s int) string {
	switch s {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	default:
		return "hint"
	}
}

func (h *bridgeHandler) diagRows(ctx context.Context, byPath map[string][]lsp.Diagnostic) []diagRow {
	c := &lineCache{h: h, ctx: ctx}
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	rows := []diagRow{}
	for _, p := range paths {
		lines := c.get(p)
		for _, d := range byPath[p] {
			r := diagRow{Path: h.rel(p), Severity: severityName(d.Severity), Message: d.Message, Source: d.Source}
			r.Line, r.Column = userPos(lines, d.Range.Start)
			r.EndLine, r.EndColumn = userPos(lines, d.Range.End)
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Path != rows[j].Path {
			return rows[i].Path < rows[j].Path
		}
		return rows[i].Line < rows[j].Line
	})
	return rows
}

// diagnostics answers from what servers already published; for a file
// nobody has open it opens the file in its server and waits for the
// publish (pyright and tsc only diagnose open documents).
func (h *bridgeHandler) diagnostics(ctx context.Context, path string) (any, error) {
	if path == "" {
		v, err := h.inLoop(ctx, func(m *Model) any {
			cp := make(map[string][]lsp.Diagnostic, len(m.diags))
			for p, d := range m.diags {
				cp[p] = d
			}
			return cp
		})
		if err != nil {
			return nil, err
		}
		rows := h.diagRows(ctx, v.(map[string][]lsp.Diagnostic))
		return map[string]any{"diagnostics": rows, "note": "files not yet opened by anyone may be missing; pass a path to diagnose one"}, nil
	}
	abs, err := h.abs(path)
	if err != nil {
		return nil, err
	}
	if lsp.LangFor(abs) == "" {
		return nil, fmt.Errorf("no language server configured for %s files", filepath.Ext(abs))
	}
	type known struct {
		diags  []lsp.Diagnostic
		have   bool
		status string
	}
	ch := make(chan []lsp.Diagnostic, 1)
	v, err := h.inLoop(ctx, func(m *Model) any {
		k := known{status: m.lspStatus[lsp.LangFor(abs)]}
		if d, ok := m.diags[abs]; ok {
			k.diags, k.have = d, true
		} else if m.docByPath(abs) != nil && k.status == "ready" {
			k.have = true // open, server ready, nothing published: clean
		} else {
			m.diagWait[abs] = append(m.diagWait[abs], ch)
		}
		return k
	})
	if err != nil {
		return nil, err
	}
	k := v.(known)
	if !k.have {
		// Nothing published yet: the server itself has to look at the file.
		if _, err := h.client(abs); err != nil {
			return nil, err
		}
		closeDoc, err := h.ensureOpen(ctx, abs)
		if err != nil {
			return nil, err
		}
		select {
		case k.diags = <-ch:
		case <-time.After(6 * time.Second):
		case <-ctx.Done():
		}
		h.inLoop(context.Background(), func(m *Model) any {
			ws := m.diagWait[abs]
			for i, w := range ws {
				if w == ch {
					m.diagWait[abs] = append(ws[:i], ws[i+1:]...)
					break
				}
			}
			if len(m.diagWait[abs]) == 0 {
				delete(m.diagWait, abs)
			}
			k.status = m.lspStatus[lsp.LangFor(abs)]
			return nil
		})
		closeDoc()
	}
	rows := h.diagRows(ctx, map[string][]lsp.Diagnostic{abs: k.diags})
	out := map[string]any{"diagnostics": rows}
	if k.status != "ready" && k.status != "" {
		out["note"] = "language server is " + k.status + "; results may be incomplete"
	}
	return out, nil
}

func (h *bridgeHandler) locations(ctx context.Context, kind, path string, line, column int) (any, error) {
	abs, err := h.abs(path)
	if err != nil {
		return nil, err
	}
	c, err := h.client(abs)
	if err != nil {
		return nil, err
	}
	lines, err := h.fileLines(ctx, abs)
	if err != nil {
		return nil, err
	}
	pos, err := lspPos(lines, line, column)
	if err != nil {
		return nil, err
	}
	closeDoc, err := h.ensureOpen(ctx, abs)
	if err != nil {
		return nil, err
	}
	defer closeDoc()
	lctx, cancel := lsp.Ctx()
	defer cancel()
	var locs []lsp.Location
	if kind == "definition" {
		locs, err = c.Definition(lctx, lsp.PathToURI(abs), pos)
	} else {
		locs, err = c.References(lctx, lsp.PathToURI(abs), pos)
	}
	if err != nil {
		return nil, err
	}
	cache := &lineCache{h: h, ctx: ctx}
	out := make([]location, 0, len(locs))
	for _, l := range locs {
		out = append(out, cache.loc(l.URI, l.Range))
	}
	return map[string]any{kind: out}, nil
}

func (h *bridgeHandler) hover(ctx context.Context, path string, line, column int) (any, error) {
	abs, err := h.abs(path)
	if err != nil {
		return nil, err
	}
	c, err := h.client(abs)
	if err != nil {
		return nil, err
	}
	lines, err := h.fileLines(ctx, abs)
	if err != nil {
		return nil, err
	}
	pos, err := lspPos(lines, line, column)
	if err != nil {
		return nil, err
	}
	closeDoc, err := h.ensureOpen(ctx, abs)
	if err != nil {
		return nil, err
	}
	defer closeDoc()
	lctx, cancel := lsp.Ctx()
	defer cancel()
	text, err := c.Hover(lctx, lsp.PathToURI(abs), pos)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return "no documentation at that position", nil
	}
	return text, nil
}

var symbolKinds = [...]string{"", "file", "module", "namespace", "package", "class", "method", "property",
	"field", "constructor", "enum", "interface", "function", "variable", "constant", "string", "number",
	"boolean", "array", "object", "key", "null", "enum_member", "struct", "event", "operator", "type_parameter"}

func kindName(k int) string {
	if k > 0 && k < len(symbolKinds) {
		return symbolKinds[k]
	}
	return "symbol"
}

func (h *bridgeHandler) documentSymbols(ctx context.Context, path string) (any, error) {
	abs, err := h.abs(path)
	if err != nil {
		return nil, err
	}
	c, err := h.client(abs)
	if err != nil {
		return nil, err
	}
	closeDoc, err := h.ensureOpen(ctx, abs)
	if err != nil {
		return nil, err
	}
	defer closeDoc()
	lctx, cancel := lsp.Ctx()
	defer cancel()
	syms, err := c.DocumentSymbols(lctx, lsp.PathToURI(abs))
	if err != nil {
		return nil, err
	}
	type row struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Line  int    `json:"line"`
		Depth int    `json:"depth,omitempty"`
	}
	lines, _ := h.fileLines(ctx, abs)
	var rows []row
	var walk func(ss []lsp.DocumentSymbol, depth int)
	walk = func(ss []lsp.DocumentSymbol, depth int) {
		for _, s := range ss {
			l, _ := userPos(lines, s.SelectionRange.Start)
			rows = append(rows, row{Name: s.Name, Kind: kindName(s.Kind), Line: l, Depth: depth})
			walk(s.Children, depth+1)
		}
	}
	walk(syms, 0)
	if rows == nil {
		rows = []row{}
	}
	return map[string]any{"path": h.rel(abs), "symbols": rows}, nil
}

func (h *bridgeHandler) workspaceSymbols(ctx context.Context, query string) (any, error) {
	if query == "" {
		return nil, errors.New("query required")
	}
	// Any running server can answer; ask each and merge. A server that
	// isn't running for a language isn't started just for this.
	type row struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
		Path string `json:"path"`
		Line int    `json:"line"`
	}
	rows := []row{}
	cache := &lineCache{h: h, ctx: ctx}
	for _, c := range h.lspm.Running() {
		lctx, cancel := lsp.Ctx()
		syms, err := c.WorkspaceSymbols(lctx, query)
		cancel()
		if err != nil {
			continue
		}
		for _, s := range syms {
			abs := lsp.URIToPath(s.Location.URI)
			l, _ := userPos(cache.get(abs), s.Location.Range.Start)
			rows = append(rows, row{Name: s.Name, Kind: kindName(s.Kind), Path: h.rel(abs), Line: l})
		}
	}
	if len(h.lspm.Running()) == 0 {
		return nil, errors.New("no language server running yet: open a file of the language in Cove first, or use document_symbols on a path")
	}
	return map[string]any{"symbols": rows}, nil
}

func (h *bridgeHandler) rename(ctx context.Context, path string, line, column int, newName string) (any, error) {
	if newName == "" {
		return nil, errors.New("new_name required")
	}
	abs, err := h.abs(path)
	if err != nil {
		return nil, err
	}
	c, err := h.client(abs)
	if err != nil {
		return nil, err
	}
	lines, err := h.fileLines(ctx, abs)
	if err != nil {
		return nil, err
	}
	pos, err := lspPos(lines, line, column)
	if err != nil {
		return nil, err
	}
	closeDoc, err := h.ensureOpen(ctx, abs)
	if err != nil {
		return nil, err
	}
	defer closeDoc()
	lctx, cancel := lsp.Ctx()
	defer cancel()
	we, err := c.Rename(lctx, lsp.PathToURI(abs), pos, newName)
	if err != nil {
		return nil, err
	}
	if we == nil {
		return nil, errors.New("nothing to rename at that position")
	}
	we.Normalize()
	v, err := h.inLoop(ctx, func(m *Model) any {
		revs := make(map[string]int, len(m.docs))
		for _, d := range m.docs {
			revs[d.path] = d.ed.Rev
		}
		files, stale := m.applyWorkspaceEdit(we, revs)
		m.side.Refresh()
		m.refreshGit()
		m.syncWatched()
		return [2]int{files, stale}
	})
	if err != nil {
		return nil, err
	}
	n := v.([2]int)
	changed := make([]string, 0, len(we.Changes))
	for uri := range we.Changes {
		changed = append(changed, h.rel(lsp.URIToPath(uri)))
	}
	sort.Strings(changed)
	out := map[string]any{"files_changed": n[0], "files": changed}
	if n[1] > 0 {
		out["skipped"] = fmt.Sprintf("%d file(s) skipped: edited in Cove while the rename ran; retry", n[1])
	}
	return out, nil
}

// ---- registration with Claude Code ----

// bridgeInstallCmd runs `claude mcp add` once, at user scope, so every
// Claude Code session — inside Cove or not — knows the `cove mcp` server.
// Outside Cove it serves no tools and stays out of the way.
func (m *Model) bridgeInstallCmd() tea.Cmd {
	if _, err := exec.LookPath("claude"); err != nil {
		m.notifyErr("claude not found on PATH — install Claude Code first, or register manually: see README")
		return nil
	}
	cove := coveCommand()
	return func() tea.Msg {
		// Replace, never keep: an earlier registration may point at another
		// build (a dev binary, or a brew path that has since moved).
		exec.Command("claude", "mcp", "remove", "--scope", "user", "cove").Run()
		out, err := exec.Command("claude", "mcp", "add", "--scope", "user", "cove", "--", cove, "mcp").CombinedOutput()
		return gitFetchDoneMsg{do: func(m *Model) tea.Cmd {
			if err != nil {
				m.notifyErr("claude mcp add: " + firstLine(strings.TrimSpace(string(out))))
				return nil
			}
			m.notify("registered " + cove + " mcp with Claude Code (user scope)")
			return nil
		}}
	}
}

// coveCommand is the binary to register: the bare name "cove" only when the
// cove on PATH is this very build (so a brew upgrade keeps working), else
// this executable's absolute path. Registering a bare "cove" that resolves
// to an older install would hand Claude Code a full-screen editor on its
// MCP pipes — an older cove has no mcp subcommand and opens "mcp" as a file.
func coveCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return "cove"
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if onPath, err := exec.LookPath("cove"); err == nil {
		if real, err := filepath.EvalSymlinks(onPath); err == nil && real == exe {
			return "cove"
		}
	}
	return exe
}
