package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GurYN/cove-editor/internal/term"
)

const termDefaultRows = 10

// termMsg reports output (alive) or shell exit (!alive) for one instance.
type termMsg struct {
	t     *term.Term
	alive bool
}

// listenTerm forwards one Notify ping into the Bubbletea loop (same
// listen-cmd pattern as listenLSP).
func listenTerm(t *term.Term) tea.Cmd {
	return func() tea.Msg {
		_, ok := <-t.Notify()
		return termMsg{t: t, alive: ok}
	}
}

func (m *Model) activeTerm() *term.Term {
	if len(m.terms) == 0 {
		return nil
	}
	return m.terms[m.termActive]
}

// termRows is the panel's content height, clamped so the editor keeps room.
func (m *Model) termRows() int {
	return clampInt(m.termH, 3, max(3, m.height-8))
}

// panelRows is the total height the panel occupies (content + title border),
// 0 when hidden.
func (m *Model) panelRows() int {
	if !m.termOpen {
		return 0
	}
	return m.termRows() + 1
}

// newTerm starts another shell instance and makes it active.
func (m *Model) newTerm() tea.Cmd { return m.spawnTerm(nil, "") }

// spawnTerm starts argv (nil = shell) as a new panel instance.
func (m *Model) spawnTerm(argv []string, label string) tea.Cmd {
	t, err := term.New(m.side.Root, argv, max(2, m.width-m.editorX()), m.termRows())
	if err != nil {
		m.lastMsg = "terminal: " + err.Error()
		return nil
	}
	t.Label = label
	m.terms = append(m.terms, t)
	m.termActive = len(m.terms) - 1
	m.termOpen = true
	m.focus = paneTerminal
	return listenTerm(t)
}

// openApp focuses the named [apps.*] instance if it's already running,
// otherwise launches it.
func (m *Model) openApp(name string, argv []string) tea.Cmd {
	for i, t := range m.terms {
		if t.Label == name {
			m.termActive = i
			m.termOpen = true
			m.focus = paneTerminal
			return nil
		}
	}
	return m.spawnTerm(argv, name)
}

// toggleTerm shows/hides the panel, starting the first shell on first open.
func (m *Model) toggleTerm() tea.Cmd {
	if m.termOpen {
		m.termOpen = false
		if m.focus == paneTerminal {
			m.focus = paneEditor
		}
		return nil
	}
	if len(m.terms) == 0 {
		// newTerm sets termOpen/focus only on success — a failed PTY spawn
		// must not leave an open-but-empty panel swallowing keystrokes.
		return m.newTerm()
	}
	m.termOpen = true
	m.focus = paneTerminal
	return nil
}

func (m Model) handleTermMsg(msg termMsg) (Model, tea.Cmd) {
	if msg.alive {
		return m, listenTerm(msg.t)
	}
	// Shell exited: drop that instance; drop the panel when none remain.
	msg.t.Close()
	for i, t := range m.terms {
		if t == msg.t {
			m.terms = append(m.terms[:i], m.terms[i+1:]...)
			break
		}
	}
	if m.termActive >= len(m.terms) {
		m.termActive = max(0, len(m.terms)-1)
	}
	if len(m.terms) == 0 {
		m.termOpen = false
		if m.focus == paneTerminal {
			m.focus = paneEditor
		}
	}
	m.layout()
	return m, nil
}

// termChips are the clickable instance tabs plus the trailing "+" button.
func (m Model) termChips() []string {
	chips := make([]string, 0, len(m.terms)+1)
	for i, t := range m.terms {
		if t.Label != "" {
			chips = append(chips, " "+t.Label+" ")
		} else {
			chips = append(chips, fmt.Sprintf(" %d ", i+1))
		}
	}
	return append(chips, " + ")
}

// termChipRanges returns each chip's [start, end) x-range relative to the
// editor pane's left edge, matching renderTermPanel exactly.
func (m Model) termChipRanges() []struct{ start, end int } {
	x := 2 + lipgloss.Width(m.termTitleLabel())
	chips := m.termChips()
	out := make([]struct{ start, end int }, len(chips))
	for i, c := range chips {
		w := lipgloss.Width(c)
		out[i] = struct{ start, end int }{x, x + w}
		x += w
	}
	return out
}

// termChipEnd is the x (editor-pane-relative) where the label+chips strip
// ends and the draggable border begins.
func (m Model) termChipEnd() int {
	r := m.termChipRanges()
	return r[len(r)-1].end
}

func (m Model) termTitleLabel() string {
	if t := m.activeTerm(); t != nil {
		if s := t.Scrolled(); s > 0 {
			return fmt.Sprintf(" Terminal ↑%d ", s)
		}
	}
	return " Terminal "
}

// renderTermPanel is the draggable title border (with instance chips and
// the "+" button) plus the active emulator screen, sized to the editor pane.
func (m Model) renderTermPanel() string {
	label := m.termTitleLabel()
	if m.focus == paneTerminal {
		label = tabActiveStyle.Render(label)
	}
	var chips strings.Builder
	for i, c := range m.termChips() {
		switch {
		case i == m.termActive && i < len(m.terms):
			chips.WriteString(tabActiveStyle.Render(c))
		default:
			chips.WriteString(tabStyle.Render(c))
		}
	}
	w := m.width - m.editorX()
	ranges := m.termChipRanges()
	rest := max(0, w-ranges[len(ranges)-1].end)
	title := borderStyle.Render("──") + label + chips.String() + borderStyle.Render(strings.Repeat("─", rest))
	return title + "\n" + m.activeTerm().View(m.focus == paneTerminal)
}
