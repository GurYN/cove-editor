package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/ai"
	"github.com/GurYN/cove-editor/internal/config"
	"github.com/GurYN/cove-editor/internal/lsp"
)

// AI inline completion (ghost text). The flow: an edit lands → flushChange
// schedules an aiTickMsg after the debounce → the tick fires one HTTP
// request (canceling any in-flight one) → the result becomes editor ghost
// text if the buffer hasn't moved since. Everything runs in tea.Cmds; the
// keystroke→frame path never touches the network.

type aiTickMsg struct{ gen int }

type aiComplMsg struct {
	text   string
	path   string
	rev    int // editor revision at request time; stale results drop
	off    int // cursor offset at request time
	gen    int
	manual bool // explicit ai.trigger: errors and misses get surfaced
	err    error
}

// aiCompleter is what the app needs from the backend; tests stub it.
type aiCompleter interface {
	Complete(ctx context.Context, req ai.Request) (string, error)
}

type aiState struct {
	client   aiCompleter // nil = not configured
	enabled  bool        // runtime toggle (ai.toggle)
	manual   bool        // [ai] manual: suggest only on ai.trigger, never on typing pause
	broken   bool        // [ai] enabled in config but unusable (bad config, empty key)
	debounce time.Duration
	gen      int                // bumped per schedule; stale ticks/results drop
	busy     int                // gen of the in-flight request (0 = idle); drives the status glyph
	cancel   context.CancelFunc // in-flight request, canceled on the next one
}

// aiSeg renders the AI segment of the status bar: ai… request in flight,
// ai✓ ready, ai· toggled off. All three cells are the same width so the
// segments left of it never shift.
func (m Model) aiSeg() string {
	if m.ai.client == nil {
		if m.ai.broken { // configured but unusable: stay visible, don't vanish
			return "ai✗  "
		}
		return ""
	}
	glyph := "✓"
	switch {
	case !m.ai.enabled:
		glyph = "·"
	case m.ai.busy != 0:
		glyph = "…"
	}
	return "ai" + glyph + "  "
}

// configureAI wires the [ai] config block; returns warnings for cfgWarns.
func (m *Model) configureAI(cfg *config.Config) []string {
	m.ai.debounce = 250 * time.Millisecond
	if cfg.AI.DebounceMS > 0 {
		m.ai.debounce = time.Duration(cfg.AI.DebounceMS) * time.Millisecond
	}
	if !cfg.AI.Enabled {
		return nil
	}
	key := cfg.AI.APIKey
	if cfg.AI.APIKeyEnv != "" {
		key = os.Getenv(cfg.AI.APIKeyEnv)
		if key == "" {
			m.ai.broken = true // keep an ai✗ in the status bar, not a silent absence
			return []string{"ai: $" + cfg.AI.APIKeyEnv + " is empty — export it in the shell that launches Cove"}
		}
	}
	c, err := ai.New(ai.Config{
		Protocol:  cfg.AI.Protocol,
		BaseURL:   cfg.AI.BaseURL,
		Model:     cfg.AI.Model,
		Key:       key,
		MaxTokens: cfg.AI.MaxTokens,
	})
	if err != nil {
		m.ai.broken = true
		return []string{err.Error()}
	}
	m.ai.client = c
	m.ai.enabled = true
	m.ai.manual = cfg.AI.Manual
	return nil
}

// scheduleAI arms the completion debounce; rides flushChange so it only
// fires after typing has already paused for the LSP sync window.
func (m *Model) scheduleAI() tea.Cmd {
	if m.ai.client == nil || !m.ai.enabled || m.ai.manual || m.focus != paneEditor {
		return nil
	}
	d := m.doc()
	if d == nil || d.virtual || d.ed.ReadOnly {
		return nil
	}
	if !d.ed.GhostVisible() {
		d.ed.SetGhost("", 0) // drop stale state so it can't resurface
	}
	m.ai.gen++
	g := m.ai.gen
	return tea.Tick(m.ai.debounce, func(time.Time) tea.Msg { return aiTickMsg{g} })
}

// aiRequest fires one completion request for the current cursor position.
// gen must be current (a newer edit rescheduled past this tick).
func (m *Model) aiRequest(gen int, manual bool) tea.Cmd {
	if m.ai.client == nil || !m.ai.enabled || gen != m.ai.gen || m.compl.active {
		return nil
	}
	d := m.doc()
	if d == nil || d.virtual || d.ed.ReadOnly || m.focus != paneEditor || d.ed.CursorCount() != 1 {
		return nil
	}
	line, col := d.ed.Cursor()
	if col != d.ed.Buf.LineLen(line) {
		if manual {
			m.notify("ai: suggestions show at end of line")
		}
		return nil
	}
	req := m.aiRequestAt(d, line, col)
	path, rev, off := d.path, d.ed.Rev, d.ed.Buf.Offset(line, col)
	if m.ai.cancel != nil {
		m.ai.cancel() // one request in flight, ever
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	m.ai.cancel = cancel
	m.ai.busy = gen
	c := m.ai.client
	return func() tea.Msg {
		defer cancel()
		text, err := c.Complete(ctx, req)
		return aiComplMsg{text: text, path: path, rev: rev, off: off, gen: gen, manual: manual, err: err}
	}
}

// aiRequestAt builds the prompt window: up to ~120 lines / 8KB before the
// cursor, ~120 lines / 6KB after. The suffix matters as much as the prefix —
// helpers defined below the cursor must be visible or the model re-implements
// them inline.
func (m *Model) aiRequestAt(d *doc, line, col int) ai.Request {
	buf := d.ed.Buf
	off := buf.Offset(line, col)
	lo := buf.Offset(max(0, line-120), 0)
	lo = max(lo, off-8192)
	endLine := min(buf.LineCount()-1, line+120)
	hi := buf.Offset(endLine, buf.LineLen(endLine))
	hi = min(hi, off+6144)
	prefix := buf.Slice(lo, off)
	for len(prefix) > 0 && prefix[0]&0xC0 == 0x80 {
		prefix = prefix[1:] // byte cap can land mid-rune
	}
	lang := lsp.LangFor(d.path)
	if lang == "" {
		lang = strings.TrimPrefix(filepath.Ext(d.path), ".")
	}
	return ai.Request{Language: lang, Prefix: string(prefix), Suffix: string(buf.Slice(off, hi))}
}

// handleAIResult installs the ghost if the buffer hasn't moved since the
// request. Background errors stay silent — ambient help must not nag.
func (m *Model) handleAIResult(msg aiComplMsg) {
	if msg.gen == m.ai.busy { // this request is the one the glyph tracks
		m.ai.busy = 0
		m.ai.cancel = nil
	}
	if msg.gen != m.ai.gen {
		return
	}
	if msg.err != nil {
		if msg.manual {
			m.notifyErr("ai: " + msg.err.Error())
		}
		return
	}
	d := m.doc()
	if d == nil || !same(d.path, msg.path) || d.ed.Rev != msg.rev {
		return
	}
	if msg.text == "" {
		if msg.manual {
			m.notify("ai: no suggestion")
		}
		return
	}
	// A wall of ghost text is noise even when it's right — small models
	// happily dump 40 lines. Keep the head; the user can re-trigger at the
	// new cursor for the rest.
	// ponytail: hard cap, no config knob until someone wants one.
	if lines := strings.Split(msg.text, "\n"); len(lines) > aiMaxGhostLines {
		msg.text = strings.Join(lines[:aiMaxGhostLines], "\n")
	}
	d.ed.SetGhost(msg.text, msg.off)
}

// aiMaxGhostLines caps how many lines a suggestion may occupy on screen.
const aiMaxGhostLines = 8

// handleGhostKey intercepts Tab (accept) and Esc (dismiss) while a ghost is
// visible. handled=false lets the key flow on normally.
func (m *Model) handleGhostKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	d := m.doc()
	if d == nil || m.focus != paneEditor || !d.ed.GhostVisible() {
		return nil, false
	}
	switch msg.Type {
	case tea.KeyTab:
		d.ed.AcceptGhost()
		return m.syncLSP(), true
	case tea.KeyEscape:
		d.ed.SetGhost("", 0)
		return nil, true
	}
	return nil, false
}
