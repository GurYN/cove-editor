package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// Event is what the manager surfaces to the UI loop.
type Event struct {
	URI         string
	Diagnostics []Diagnostic // valid when Kind == "diagnostics"
	Kind        string       // "diagnostics" | "status"
	Lang        string
	Status      string // "starting" | "ready" | "dead"
}

// Client owns one language-server process.
type Client struct {
	lang   string
	argv   []string
	root   string
	events chan<- Event

	mu      sync.Mutex
	conn    *conn
	cmd     *exec.Cmd
	state   string   // starting | ready | dead
	queued  []func() // notifications deferred until ready
	pull    bool     // server uses pull diagnostics (textDocument/diagnostic)
	pulling map[string]bool // uri → pull request in flight
	repull  map[string]bool // uri → doc changed mid-pull, go again
}

func newClient(lang string, argv []string, root string, events chan<- Event) *Client {
	c := &Client{lang: lang, argv: argv, root: root, events: events, state: "starting",
		pulling: map[string]bool{}, repull: map[string]bool{}}
	go c.start()
	return c
}

func (c *Client) start() {
	c.emit("starting")
	cmd := exec.Command(c.argv[0], c.argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.die()
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.die()
		return
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		c.die()
		return
	}
	// Reap the process on any exit path — without this every dead server
	// stays a zombie until Cove itself exits. Readers just see EOF.
	go cmd.Wait()
	c.mu.Lock()
	if c.state == "dead" { // Kill() arrived before we even started
		c.mu.Unlock()
		cmd.Process.Kill()
		return
	}
	c.cmd = cmd // set before initialize so Kill() can reach the process
	c.mu.Unlock()
	conn := newConn(stdin, stdout, c.onNotify, func(error) { c.die() })

	initParams := map[string]any{
		"processId": nil,
		"rootUri":   PathToURI(c.root),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{},
				"diagnostic":         map[string]any{}, // pull model (TS7 native, future servers)
				"hover":              map[string]any{"contentFormat": []string{"plaintext", "markdown"}},
				"documentSymbol":     map[string]any{"hierarchicalDocumentSymbolSupport": true},
				"completion": map[string]any{
					"completionItem": map[string]any{"snippetSupport": false},
				},
			},
			"workspace": map[string]any{
				// Ask for plain Changes maps, not documentChanges.
				"workspaceEdit": map[string]any{"documentChanges": false},
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var initRes struct {
		Capabilities struct {
			// Present (object or bool) when the server wants the client to
			// pull diagnostics instead of waiting for pushes.
			DiagnosticProvider json.RawMessage `json:"diagnosticProvider"`
		} `json:"capabilities"`
	}
	if err := conn.Call(ctx, "initialize", initParams, &initRes); err != nil {
		cmd.Process.Kill()
		c.die()
		return
	}
	conn.Notify("initialized", struct{}{})

	c.mu.Lock()
	if c.state == "dead" { // Kill() landed mid-initialize: stay dead
		c.mu.Unlock()
		cmd.Process.Kill()
		return
	}
	if dp := string(initRes.Capabilities.DiagnosticProvider); dp != "" && dp != "null" && dp != "false" {
		c.pull = true
	}
	c.conn = conn
	c.state = "ready"
	queued := c.queued
	c.queued = nil
	c.mu.Unlock()
	for _, f := range queued {
		f()
	}
	c.emit("ready")
}

func (c *Client) die() {
	c.mu.Lock()
	already := c.state == "dead"
	c.state = "dead"
	c.mu.Unlock()
	if !already {
		c.emit("dead")
	}
}

func (c *Client) emit(status string) {
	select {
	case c.events <- Event{Kind: "status", Lang: c.lang, Status: status}:
	default:
	}
}

func (c *Client) onNotify(method string, params json.RawMessage) {
	switch method {
	case "textDocument/publishDiagnostics":
		var p struct {
			URI         string       `json:"uri"`
			Diagnostics []Diagnostic `json:"diagnostics"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		// Diagnostics block until the UI drains them — they must not be lost.
		c.events <- Event{Kind: "diagnostics", URI: p.URI, Diagnostics: p.Diagnostics, Lang: c.lang}
	case "client/registerCapability":
		// Dynamic route to the same pull flag the initialize result sets.
		var p struct {
			Registrations []struct {
				Method string `json:"method"`
			} `json:"registrations"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		for _, r := range p.Registrations {
			if r.Method == "textDocument/diagnostic" {
				c.mu.Lock()
				c.pull = true
				c.mu.Unlock()
			}
		}
	}
}

// ---- pull diagnostics (LSP 3.17 textDocument/diagnostic) ----
// Push servers (gopls, pyright) volunteer diagnostics; pull servers (TS7's
// native `tsc --lsp`) wait to be asked. maybePull asks after every open/
// change/save and funnels the answer into the same diagnostics Event, so
// the UI never knows which model the server uses.

// maybePull starts one pull per uri; a change landing mid-pull queues
// exactly one re-pull so the newest state always wins.
func (c *Client) maybePull(uri string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pull || c.state != "ready" {
		return
	}
	if c.pulling[uri] {
		c.repull[uri] = true
		return
	}
	c.pulling[uri] = true
	go c.pullDiags(uri)
}

func (c *Client) pullDiags(uri string) {
	for {
		conn, err := c.readyConn()
		if err != nil {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var res struct {
			Kind  string       `json:"kind"`
			Items []Diagnostic `json:"items"`
		}
		err = conn.Call(ctx, "textDocument/diagnostic",
			map[string]any{"textDocument": map[string]any{"uri": uri}}, &res)
		cancel()
		if err != nil {
			break
		}
		if res.Kind != "unchanged" { // we send no previousResultId, so always full
			c.events <- Event{Kind: "diagnostics", URI: uri, Diagnostics: res.Items, Lang: c.lang}
		}
		c.mu.Lock()
		if c.repull[uri] {
			delete(c.repull, uri)
			c.mu.Unlock()
			continue
		}
		delete(c.pulling, uri)
		c.mu.Unlock()
		return
	}
	// Errored out: clear the flags so the next change starts a fresh pull.
	c.mu.Lock()
	delete(c.pulling, uri)
	delete(c.repull, uri)
	c.mu.Unlock()
}

// ready returns the live conn or an error while starting/dead.
func (c *Client) readyConn() (*conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case "ready":
		return c.conn, nil
	case "starting":
		return nil, fmt.Errorf("%s: language server starting…", c.lang)
	default:
		return nil, fmt.Errorf("%s: language server unavailable", c.lang)
	}
}

// notifyOrQueue delivers a notification now, or after initialize completes.
func (c *Client) notifyOrQueue(f func()) {
	c.mu.Lock()
	if c.state == "starting" {
		c.queued = append(c.queued, f)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	f()
}

// ---- document sync ----

func (c *Client) DidOpen(uri, langID, text string, version int) {
	c.notifyOrQueue(func() {
		if conn, err := c.readyConn(); err == nil {
			conn.Notify("textDocument/didOpen", map[string]any{
				"textDocument": map[string]any{
					"uri": uri, "languageId": langID, "version": version, "text": text,
				},
			})
			c.maybePull(uri)
		}
	})
}

func (c *Client) DidChange(uri string, version int, fullText string) {
	c.notifyOrQueue(func() {
		if conn, err := c.readyConn(); err == nil {
			conn.Notify("textDocument/didChange", map[string]any{
				"textDocument":   map[string]any{"uri": uri, "version": version},
				"contentChanges": []map[string]any{{"text": fullText}}, // full sync
			})
			c.maybePull(uri)
		}
	})
}

func (c *Client) DidSave(uri string) {
	if conn, err := c.readyConn(); err == nil {
		conn.Notify("textDocument/didSave", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		})
		c.maybePull(uri)
	}
}

func (c *Client) DidClose(uri string) {
	if conn, err := c.readyConn(); err == nil {
		conn.Notify("textDocument/didClose", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		})
	}
}

// ---- features ----

func docPos(uri string, pos Position) map[string]any {
	return map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     pos,
	}
}

func (c *Client) Completion(ctx context.Context, uri string, pos Position) ([]CompletionItem, error) {
	conn, err := c.readyConn()
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := conn.Call(ctx, "textDocument/completion", docPos(uri, pos), &raw); err != nil {
		return nil, err
	}
	var list struct {
		Items []CompletionItem `json:"items"`
	}
	if json.Unmarshal(raw, &list) == nil && list.Items != nil {
		return list.Items, nil
	}
	var items []CompletionItem
	json.Unmarshal(raw, &items)
	return items, nil
}

func (c *Client) Hover(ctx context.Context, uri string, pos Position) (string, error) {
	conn, err := c.readyConn()
	if err != nil {
		return "", err
	}
	var res struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := conn.Call(ctx, "textDocument/hover", docPos(uri, pos), &res); err != nil {
		return "", err
	}
	return hoverText(res.Contents), nil
}

// hoverText flattens the spec's contents zoo (MarkupContent | MarkedString |
// []MarkedString) into plain text.
func hoverText(raw json.RawMessage) string {
	var mc struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &mc) == nil && mc.Value != "" {
		return mc.Value
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		out := ""
		for _, el := range arr {
			if t := hoverText(el); t != "" {
				if out != "" {
					out += "\n"
				}
				out += t
			}
		}
		return out
	}
	return ""
}

func (c *Client) Definition(ctx context.Context, uri string, pos Position) ([]Location, error) {
	return c.locations(ctx, "textDocument/definition", docPos(uri, pos))
}

func (c *Client) References(ctx context.Context, uri string, pos Position) ([]Location, error) {
	params := docPos(uri, pos)
	params["context"] = map[string]any{"includeDeclaration": true}
	return c.locations(ctx, "textDocument/references", params)
}

func (c *Client) locations(ctx context.Context, method string, params any) ([]Location, error) {
	conn, err := c.readyConn()
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := conn.Call(ctx, method, params, &raw); err != nil {
		return nil, err
	}
	var locs []Location
	if json.Unmarshal(raw, &locs) == nil && locs != nil {
		return locs, nil
	}
	var one Location
	if json.Unmarshal(raw, &one) == nil && one.URI != "" {
		return []Location{one}, nil
	}
	var links []struct {
		TargetURI   string `json:"targetUri"`
		TargetRange Range  `json:"targetSelectionRange"`
	}
	if json.Unmarshal(raw, &links) == nil {
		for _, l := range links {
			locs = append(locs, Location{URI: l.TargetURI, Range: l.TargetRange})
		}
	}
	return locs, nil
}

// DocumentSymbols returns the file's outline. Servers reply with either
// hierarchical DocumentSymbol[] or flat SymbolInformation[]; the flat form
// (spotted by its "location" field) is folded into the hierarchical one.
func (c *Client) DocumentSymbols(ctx context.Context, uri string) ([]DocumentSymbol, error) {
	conn, err := c.readyConn()
	if err != nil {
		return nil, err
	}
	params := map[string]any{"textDocument": map[string]any{"uri": uri}}
	var nodes []struct {
		DocumentSymbol
		Location *Location `json:"location"`
	}
	if err := conn.Call(ctx, "textDocument/documentSymbol", params, &nodes); err != nil {
		return nil, err
	}
	syms := make([]DocumentSymbol, 0, len(nodes))
	for _, n := range nodes {
		if n.Location != nil {
			n.SelectionRange = n.Location.Range
			n.Children = nil // SymbolInformation is flat
		}
		syms = append(syms, n.DocumentSymbol)
	}
	return syms, nil
}

func (c *Client) Rename(ctx context.Context, uri string, pos Position, newName string) (*WorkspaceEdit, error) {
	conn, err := c.readyConn()
	if err != nil {
		return nil, err
	}
	params := docPos(uri, pos)
	params["newName"] = newName
	var we WorkspaceEdit
	if err := conn.Call(ctx, "textDocument/rename", params, &we); err != nil {
		return nil, err
	}
	we.Normalize()
	return &we, nil
}

func (c *Client) Format(ctx context.Context, uri string) ([]TextEdit, error) {
	conn, err := c.readyConn()
	if err != nil {
		return nil, err
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"options":      map[string]any{"tabSize": 4, "insertSpaces": false},
	}
	var edits []TextEdit
	if err := conn.Call(ctx, "textDocument/formatting", params, &edits); err != nil {
		return nil, err
	}
	return edits, nil
}

// Kill terminates the server process (shutdown handshake skipped on quit).
func (c *Client) Kill() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	c.state = "dead"
}
