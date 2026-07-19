package lsp

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServerDef declares how to launch a language server. Resolve, when set,
// picks the argv at first use (and nil means "no usable server").
// ponytail: hardcoded defaults; TOML registration lands in Phase 4.
type ServerDef struct {
	Argv    []string
	Resolve func() []string
	LangID  string
}

var defaultServers = map[string]ServerDef{
	"go":     {Argv: []string{"gopls"}, LangID: "go"},
	"python": {Argv: []string{"pyright-langserver", "--stdio"}, LangID: "python"},
	// TypeScript 7+ (the native compiler) serves LSP itself (pull-diagnostics
	// model); TS5 setups fall back to typescript-language-server. A
	// config.toml [lsp.typescript] override replaces the probe entirely.
	"typescript": {Resolve: resolveTypescript, LangID: "typescript"},
	"rust":       {Argv: []string{"rust-analyzer"}, LangID: "rust"},
	// html + css ship together in `npm i -g vscode-langservers-extracted`.
	"html": {Argv: []string{"vscode-html-language-server", "--stdio"}, LangID: "html"},
	"css":  {Argv: []string{"vscode-css-language-server", "--stdio"}, LangID: "css"},
}

// resolveTypescript probes once, on the first ts/js file open: tsc 7+ →
// its native `tsc --lsp`; otherwise typescript-language-server (the TS5
// wrapper). The ~150ms `tsc --version` exec runs only when tsc is present.
var (
	tsOnce sync.Once
	tsArgv []string
)

func resolveTypescript() []string {
	tsOnce.Do(func() {
		if _, err := exec.LookPath("tsc"); err == nil {
			if out, err := exec.Command("tsc", "--version").Output(); err == nil && tsMajor(string(out)) >= 7 {
				tsArgv = []string{"tsc", "--lsp", "--stdio"}
				return
			}
		}
		if _, err := exec.LookPath("typescript-language-server"); err == nil {
			tsArgv = []string{"typescript-language-server", "--stdio"}
		}
	})
	return tsArgv
}

// tsMajor parses `tsc --version` output ("Version 7.0.2") into 7; 0 = unknown.
func tsMajor(out string) int {
	v, ok := strings.CutPrefix(strings.TrimSpace(out), "Version ")
	if !ok {
		return 0
	}
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return n
}

var extLang = map[string]string{
	".go": "go", ".py": "python", ".rs": "rust",
	".ts": "typescript", ".tsx": "typescript",
	".js": "typescript", ".jsx": "typescript", ".mjs": "typescript", ".cjs": "typescript",
	".html": "html", ".htm": "html", ".css": "css",
}

// Configure registers or overrides a language server (from TOML config).
// Startup-only: not safe once managers are running.
func Configure(lang string, argv, exts []string, langID string) {
	if len(argv) == 0 {
		return
	}
	if langID == "" {
		langID = lang
	}
	defaultServers[lang] = ServerDef{Argv: argv, LangID: langID}
	for _, e := range exts {
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extLang[strings.ToLower(e)] = lang
	}
}

// Manager lazily starts one client per language and fans all their events
// into a single channel for the UI loop.
type Manager struct {
	root   string
	events chan Event

	mu       sync.Mutex
	clients  map[string]*Client
	restarts map[string]int
}

func NewManager(root string) *Manager {
	abs, _ := filepath.Abs(root)
	return &Manager{
		root:     abs,
		events:   make(chan Event, 64),
		clients:  map[string]*Client{},
		restarts: map[string]int{},
	}
}

func (m *Manager) Events() <-chan Event { return m.events }

// LangFor returns the language for a path, "" if unsupported.
func LangFor(path string) string {
	return extLang[strings.ToLower(filepath.Ext(path))]
}

// clientFor returns (spawning if needed) the client for path's language.
// nil when the language is unsupported, the binary is missing, or the
// server crashed too many times.
func (m *Manager) clientFor(path string) *Client {
	lang := LangFor(path)
	if lang == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.clients[lang]; c != nil {
		c.mu.Lock()
		dead := c.state == "dead"
		c.mu.Unlock()
		if !dead {
			return c
		}
		if m.restarts[lang] >= 3 {
			return nil
		}
		m.restarts[lang]++
	}
	def := defaultServers[lang]
	argv := def.Argv
	if def.Resolve != nil {
		argv = def.Resolve()
	}
	if len(argv) == 0 {
		return nil
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return nil
	}
	c := newClient(lang, argv, m.root, m.events)
	m.clients[lang] = c
	return c
}

// Open announces a file to its language server. Returns false when no
// server handles it.
func (m *Manager) Open(path string, text []byte, version int) bool {
	c := m.clientFor(path)
	if c == nil {
		return false
	}
	c.DidOpen(PathToURI(path), defaultServers[c.lang].LangID, string(text), version)
	return true
}

// Change sends a full-text didChange. Debouncing is the caller's job — the
// app loop already coalesces edits (syncLSP); a second timer here just added
// latency and a duplicate interval to keep in sync.
func (m *Manager) Change(path string, version int, text []byte) {
	if c := m.clientFor(path); c != nil {
		c.DidChange(PathToURI(path), version, string(text))
	}
}

func (m *Manager) Save(path string) {
	if c := m.clientFor(path); c != nil {
		c.DidSave(PathToURI(path))
	}
}

func (m *Manager) Close(path string) {
	if c := m.clientFor(path); c != nil {
		c.DidClose(PathToURI(path))
	}
}

// Client exposes the feature API for a path, nil when unavailable.
func (m *Manager) Client(path string) *Client { return m.clientFor(path) }

// Shutdown kills every server (on quit).
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		c.Kill()
	}
}

// Ctx returns the standard request context for interactive features.
func Ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 4*time.Second)
}
