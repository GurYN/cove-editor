package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/editor"
)

func agentSetup(t *testing.T, cfg string) (Model, string) {
	t.Helper()
	root := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte(cfg), 0o644)
	t.Setenv("COVE_CONFIG", cfgPath)
	m := New(root, nil)
	src := filepath.Join(root, "main.go")
	os.WriteFile(src, []byte("package main\n\nfunc main() {\n\tprintln(1)\n}\n"), 0o644)
	m.openFile(src)
	return m, src
}

// TestAgentSelectionText: the paste is a path:line(-line) reference plus a
// fenced block in the file's language; a partial last line still counts,
// a selection ending at column 0 doesn't drag in the next line, and a
// selection containing ``` gets a longer fence.
func TestAgentSelectionText(t *testing.T) {
	c := agentContext{rel: "pkg/a.go", selStart: 2, selEnd: 3, selected: "func main() {\n\tprintln(1)\n"}
	want := "pkg/a.go:3-4\n```go\nfunc main() {\n\tprintln(1)\n```\n"
	if got := agentSelectionText(c); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	c = agentContext{rel: "README.md", selStart: 0, selEnd: 0, selected: "a ``` b"}
	if got := agentSelectionText(c); !strings.HasPrefix(got, "README.md:1\n````md\n") || !strings.HasSuffix(got, "\n````\n") {
		t.Fatalf("fence not lengthened: %q", got)
	}
	c = agentContext{rel: "x.py", line: 6, col: 2}
	if got := agentSelectionText(c); got != "x.py:7 " {
		t.Fatalf("no-selection fallback: %q", got)
	}
	if got := agentDiagText(agentContext{rel: "x.py", line: 6, col: 2, diag: "undefined: foo\nmore"}); got != "x.py:7:3: undefined: foo\n" {
		t.Fatalf("diag: %q", got)
	}
}

// TestAgentContextSelectionLines: a selection made in the real editor maps
// to the right 0-based line span, including the column-0 end rule.
func TestAgentContextSelectionLines(t *testing.T) {
	m, _ := agentSetup(t, "[apps.claude]\ncommand = [\"cat\"]\nagent = true\n")
	d := m.doc()
	d.ed.Go(2, 0)
	d.ed.MoveV(2, true) // select lines 2..3 fully, cursor at (4,0)
	c, ok := m.agentContext()
	if !ok || c.selected != "func main() {\n\tprintln(1)\n" || c.selStart != 2 || c.selEnd != 3 {
		t.Fatalf("ctx = %+v ok=%v", c, ok)
	}
	if c.rel != "main.go" {
		t.Fatalf("rel = %q", c.rel)
	}
	d.ed.Diags = []editor.DiagSpan{{Start: 0, End: 7, Severity: 1, Message: "boom"}}
	d.ed.Go(0, 3)
	c, _ = m.agentContext()
	if c.diag != "boom" {
		t.Fatalf("diag = %q", c.diag)
	}
}

// TestAgentNotConfigured: without an agent = true entry the send actions
// explain how to set one up instead of failing silently.
func TestAgentNotConfigured(t *testing.T) {
	m, _ := agentSetup(t, "[apps.lister]\ncommand = [\"ls\"]\n")
	if m.agentApp != "" {
		t.Fatalf("agentApp = %q", m.agentApp)
	}
	if cmd := m.reg.ByID("agent.sendSelection").Do(&m); cmd != nil {
		t.Fatal("send returned a cmd with no agent configured")
	}
	if !m.msgErr || !strings.Contains(m.lastMsg, "agent = true") {
		t.Fatalf("toast = %q err=%v", m.lastMsg, m.msgErr)
	}
	if len(m.terms) != 0 {
		t.Fatal("spawned a terminal without an agent")
	}
}

// TestAgentDuplicateFlagWarns: two agent = true entries pick the first by
// name and warn about the other.
func TestAgentDuplicateFlagWarns(t *testing.T) {
	m, _ := agentSetup(t, "[apps.zed]\ncommand = [\"cat\"]\nagent = true\n[apps.aider]\ncommand = [\"cat\"]\nagent = true\n")
	if m.agentApp != "aider" {
		t.Fatalf("agentApp = %q", m.agentApp)
	}
	if !strings.Contains(strings.Join(m.cfgWarns, "; "), "both set agent = true; using aider") {
		t.Fatalf("no duplicate warning: %q", m.cfgWarns)
	}
}

// TestAgentSendLaunchesAndPastes: with no agent running, sending spawns the
// configured instance, defers the paste until it's up, and focuses the
// panel; the text then shows in the agent's screen (cat echoes its input).
// A second send goes straight to the running instance.
func TestAgentSendLaunchesAndPastes(t *testing.T) {
	m, _ := agentSetup(t, "[apps.claude]\ncommand = [\"cat\"]\nagent = true\n")
	m.width, m.height = 100, 30
	m.layout()
	d := m.doc()
	d.ed.Go(3, 0)
	d.ed.MoveV(1, true) // select the println line

	agentSpawnDelay = 50 * time.Millisecond
	t.Cleanup(func() { agentSpawnDelay = 3 * time.Second })
	cmd := m.reg.ByID("agent.sendSelection").Do(&m)
	if cmd == nil {
		t.Skip("PTY unavailable")
	}
	defer m.terms[0].Close()
	if len(m.terms) != 1 || m.terms[0].Label != "claude" || m.focus != paneTerminal || !m.termOpen {
		t.Fatalf("terms=%d label=%q focus=%d open=%v", len(m.terms), m.terms[0].Label, m.focus, m.termOpen)
	}
	// Run the batch concurrently: the tick yields the deferred paste; the
	// terminal listener blocks until cat echoes something, so it can't run
	// inline.
	msgs := make(chan tea.Msg, 4)
	for _, c := range cmd().(tea.BatchMsg) {
		if c != nil {
			go func() { msgs <- c() }()
		}
	}
	var paste agentPasteMsg
	select {
	case msg := <-msgs:
		p, ok := msg.(agentPasteMsg)
		if !ok {
			t.Fatalf("first batch result = %T, want agentPasteMsg", msg)
		}
		paste = p
	case <-time.After(2 * time.Second):
		t.Fatal("no deferred paste in the batch")
	}
	m.agentPaste(paste)
	waitScreen(t, &m, "main.go:4")
	waitScreen(t, &m, "println(1)")

	// Already running: immediate paste, no second instance.
	d.ed.Go(0, 0)
	if cmd := m.reg.ByID("agent.sendRef").Do(&m); cmd != nil {
		t.Fatal("send to a running agent should paste synchronously")
	}
	if len(m.terms) != 1 {
		t.Fatalf("terms = %d", len(m.terms))
	}
	waitScreen(t, &m, "main.go:1")

	// A paste for a previous generation is dropped (the instance was
	// relaunched in between).
	m.agentGen++
	m.agentPaste(agentPasteMsg{text: "STALE", gen: paste.gen})
	m.agentPending = agentPasteMsg{text: "STALE", gen: paste.gen}
	m.agentPaste(agentPasteMsg{text: "STALE", gen: paste.gen})
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(m.terms[0].View(false), "STALE") {
		t.Fatal("stale-generation paste was delivered")
	}
}

func waitScreen(t *testing.T, m *Model, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(m.terms[0].View(false), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%q never appeared on the agent screen:\n%s", want, m.terms[0].View(false))
}

// TestAgentPasteAfterFirstOutput: an agent that prints a banner gets the
// paste shortly after that output, not after the long fallback — and the
// fallback, when it fires later, doesn't paste a second time.
func TestAgentPasteAfterFirstOutput(t *testing.T) {
	m, _ := agentSetup(t, "[apps.claude]\ncommand = [\"sh\", \"-c\", \"echo READY; cat\"]\nagent = true\n")
	m.width, m.height = 100, 30
	m.layout()
	agentPasteGrace = 20 * time.Millisecond
	t.Cleanup(func() { agentPasteGrace = 300 * time.Millisecond })
	cmd := m.reg.ByID("agent.sendRef").Do(&m)
	if cmd == nil {
		t.Skip("PTY unavailable")
	}
	defer m.terms[0].Close()
	fallback := m.agentPending // what the fallback timer will deliver
	waitScreen(t, &m, "READY")
	// The first output arrives as a termMsg: it arms the grace tick.
	next, c := m.handleTermMsg(termMsg{t: m.terms[0], alive: true})
	m = next
	if c == nil || !m.agentPending.armed {
		t.Fatal("first output did not arm the paste")
	}
	var got agentPasteMsg
	deadline := time.After(2 * time.Second)
	msgs := make(chan tea.Msg, 4)
	for _, cc := range c().(tea.BatchMsg) {
		if cc != nil {
			go func() { msgs <- cc() }()
		}
	}
	for got.text == "" {
		select {
		case msg := <-msgs:
			if p, ok := msg.(agentPasteMsg); ok {
				got = p
			}
		case <-deadline:
			t.Fatal("grace tick never delivered the paste")
		}
	}
	m.agentPaste(got)
	waitScreen(t, &m, "main.go:1")
	if m.agentPending.text != "" {
		t.Fatal("pending paste not cleared after delivery")
	}
	// The fallback fires later with the same text: nothing is pasted twice.
	m.agentPaste(agentPasteMsg{text: fallback.text, gen: fallback.gen})
	time.Sleep(100 * time.Millisecond)
	if n := strings.Count(m.terms[0].View(false), "main.go:1"); n != 1 {
		t.Fatalf("paste delivered %d times:\n%s", n, m.terms[0].View(false))
	}
	// A second termMsg (more output) must not re-arm anything.
	if _, c := m.handleTermMsg(termMsg{t: m.terms[0], alive: true}); c == nil {
		t.Fatal("listener not re-issued")
	}
}
