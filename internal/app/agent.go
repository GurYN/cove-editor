package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/term"
)

// The agent is the [apps.*] entry flagged agent = true (Claude Code, aider,
// codex, …): the terminal-panel instance the "Agent: Send …" actions paste
// into. Cove writes the text and focuses the panel; the user reads it and
// presses Enter, so nothing runs behind their back. The format is plain
// path:line text plus a fenced block — every agent understands it and
// nothing depends on one tool's @-mention syntax.

// agentPasteMsg delivers text to the agent instance after a fresh spawn
// has had time to come up (see sendToAgent).
type agentPasteMsg struct {
	text string
	gen  int // spawn generation: a paste for an instance that died is dropped
}

// agentSpawnDelay is how long a just-launched agent gets before the first
// paste. Pasting into a PTY nobody reads yet works at the kernel level, but
// the app hasn't enabled bracketed paste, so the newlines would submit
// prematurely once it starts reading. ponytail: fixed delay; a slow
// machine that takes longer to start the agent gets a raw paste.
var agentSpawnDelay = 1500 * time.Millisecond

// agentTerm returns the running agent instance, nil if none.
func (m *Model) agentTerm() *term.Term {
	if m.agentApp == "" {
		return nil
	}
	for _, t := range m.terms {
		if t.Label == m.agentApp {
			return t
		}
	}
	return nil
}

// sendToAgent pastes text into the agent instance, launching it first when
// it isn't running, and leaves the panel focused so Enter goes to the agent.
func (m *Model) sendToAgent(text string) tea.Cmd {
	if m.agentApp == "" {
		m.notifyErr("no agent configured: set agent = true on an [apps.*] entry (Open Settings)")
		return nil
	}
	if t := m.agentTerm(); t != nil {
		for i, tt := range m.terms {
			if tt == t {
				m.termActive = i
			}
		}
		m.termOpen = true
		m.focus = paneTerminal
		t.Paste(text)
		return nil
	}
	cmd := m.openApp(m.agentApp, m.agentArgv) // spawns + checkpoints
	if cmd == nil {
		return nil // spawnTerm already toasted the PTY error
	}
	gen := m.agentGen
	return tea.Batch(cmd, tea.Tick(agentSpawnDelay, func(time.Time) tea.Msg {
		return agentPasteMsg{text: text, gen: gen}
	}))
}

// agentLaunchCheckpoint takes the review baseline when the agent starts —
// unless a review with pending hunks is in progress, which a relaunch must
// not wipe.
func (m *Model) agentLaunchCheckpoint() tea.Cmd {
	if !m.agentCheckpoint || (m.review.active() && len(m.review.rows) > 0) {
		return nil
	}
	m.discoverRepos()
	if len(m.git.repos) == 0 {
		return nil // not a git workspace: nothing to review against, and no nagging
	}
	return m.reviewCheckpointCmd()
}

// agentPaste completes a deferred send once the freshly launched agent is up.
func (m *Model) agentPaste(msg agentPasteMsg) {
	t := m.agentTerm()
	if t == nil || msg.gen != m.agentGen {
		return // the agent exited before the paste landed: nothing to send to
	}
	t.Paste(msg.text)
}

// agentContext is what the send actions read from the active document.
type agentContext struct {
	rel       string // workspace-relative path
	line, col int    // 0-based cursor
	selStart  int    // 0-based first selected line
	selEnd    int    // 0-based last selected line (inclusive)
	selected  string // selection text; "" = none
	diag      string // diagnostic message under the cursor; "" = none
}

func (m *Model) agentContext() (agentContext, bool) {
	d := m.doc()
	if d == nil || d.virtual {
		return agentContext{}, false
	}
	c := agentContext{rel: filepath.ToSlash(rel(m.side.Root, d.path))}
	c.line, c.col = d.ed.Cursor()
	lo, hi := d.ed.SelectionRange()
	if lo != hi {
		c.selected = string(d.ed.Buf.Slice(lo, hi))
		c.selStart, _ = d.ed.Buf.Pos(lo)
		// A selection ending at column 0 doesn't include that line.
		endLine, endCol := d.ed.Buf.Pos(hi)
		if endCol == 0 && endLine > c.selStart {
			endLine--
		}
		c.selEnd = endLine
	}
	if sp, ok := d.ed.DiagUnderCursor(); ok {
		c.diag = sp.Message
	}
	return c, true
}

// fenceLang is the code-fence info string for a path: the extension, which
// is what markdown renderers and agents key on.
func fenceLang(rel string) string {
	base := filepath.Base(rel)
	switch base {
	case "Dockerfile", "Containerfile":
		return "dockerfile"
	case "Makefile":
		return "make"
	}
	return strings.TrimPrefix(filepath.Ext(base), ".")
}

// agentSelectionText formats a selection as a reference line plus a fenced
// block; with no selection it falls back to a cursor reference.
func agentSelectionText(c agentContext) string {
	if c.selected == "" {
		return agentRefText(c)
	}
	ref := fmt.Sprintf("%s:%d", c.rel, c.selStart+1)
	if c.selEnd > c.selStart {
		ref += fmt.Sprintf("-%d", c.selEnd+1)
	}
	code := strings.TrimSuffix(c.selected, "\n")
	// Longer fence than any run of backticks inside, so the block can't be
	// closed early by code that contains ```.
	fence := "```"
	for strings.Contains(code, fence) {
		fence += "`"
	}
	return ref + "\n" + fence + fenceLang(c.rel) + "\n" + code + "\n" + fence + "\n"
}

// agentRefText is the cursor as a path:line reference.
func agentRefText(c agentContext) string {
	return fmt.Sprintf("%s:%d ", c.rel, c.line+1)
}

// agentDiagText is the diagnostic under the cursor as path:line:col: message.
func agentDiagText(c agentContext) string {
	return fmt.Sprintf("%s:%d:%d: %s\n", c.rel, c.line+1, c.col+1, firstLine(c.diag))
}

// ---- actions ----

func (m *Model) agentSendSelection() tea.Cmd {
	c, ok := m.agentContext()
	if !ok {
		m.notify("no file to send from")
		return nil
	}
	return m.sendToAgent(agentSelectionText(c))
}

func (m *Model) agentSendRef() tea.Cmd {
	c, ok := m.agentContext()
	if !ok {
		m.notify("no file to send from")
		return nil
	}
	return m.sendToAgent(agentRefText(c))
}

func (m *Model) agentSendDiag() tea.Cmd {
	c, ok := m.agentContext()
	if !ok {
		m.notify("no file to send from")
		return nil
	}
	if c.diag == "" {
		m.notify("no diagnostic under the cursor (Alt+N jumps to the next one)")
		return nil
	}
	return m.sendToAgent(agentDiagText(c))
}

// agentOpen focuses the agent, launching it when it isn't running.
func (m *Model) agentOpen() tea.Cmd {
	if m.agentApp == "" {
		m.notifyErr("no agent configured: set agent = true on an [apps.*] entry (Open Settings)")
		return nil
	}
	return m.openApp(m.agentApp, m.agentArgv)
}
