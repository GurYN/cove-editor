// Package overlay is the shared fuzzy-picker component behind the command
// palette and the file finder: an input line over a filtered, scrollable
// list. The host supplies items and receives the chosen index.
package overlay

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

// Item is one pickable row. Label is fuzzy-matched; Detail is dimmed,
// right-aligned (a keybinding, a directory).
type Item struct {
	Label  string
	Detail string
}

var (
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(0, 1)
	selStyle    = lipgloss.NewStyle().Reverse(true)
	detailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	matchStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	titleStyle  = lipgloss.NewStyle().Bold(true)
)

const maxRows = 12

type Model struct {
	Title   string
	items   []Item
	query   string
	matches []fuzzy.Match // filtered view; empty query → all items
	sel     int
	width   int
}

func New(title string, items []Item, width int) Model {
	m := Model{Title: title, items: items, width: width}
	m.filter()
	return m
}

// Query returns the current input text.
func (m Model) Query() string { return m.query }

// Selected returns the highlighted item's index into the original items,
// or -1 when the filter has no matches.
func (m Model) Selected() int {
	if len(m.matches) == 0 || m.sel >= len(m.matches) {
		return -1
	}
	return m.matches[m.sel].Index
}

type source []Item

func (s source) String(i int) string { return s[i].Label }
func (s source) Len() int            { return len(s) }

func (m *Model) filter() {
	m.sel = 0
	if m.query == "" {
		m.matches = m.matches[:0]
		for i := range m.items {
			m.matches = append(m.matches, fuzzy.Match{Index: i})
		}
		return
	}
	m.matches = fuzzy.FindFrom(m.query, source(m.items))
}

// Update handles a key. done=true means the overlay is finished: chosen is
// the picked item's index into the original items, or -1 on cancel.
func (m Model) Update(k tea.KeyMsg) (Model, int, bool) {
	switch k.Type {
	case tea.KeyEscape:
		return m, -1, true
	case tea.KeyEnter:
		if len(m.matches) == 0 {
			return m, -1, true
		}
		return m, m.matches[m.sel].Index, true
	case tea.KeyUp:
		m.sel = max(0, m.sel-1)
	case tea.KeyDown:
		m.sel = min(len(m.matches)-1, m.sel+1)
	case tea.KeyPgUp:
		m.sel = max(0, m.sel-maxRows)
	case tea.KeyPgDown:
		m.sel = min(len(m.matches)-1, m.sel+maxRows)
	case tea.KeyBackspace:
		if len(m.query) > 0 {
			r := []rune(m.query)
			m.query = string(r[:len(r)-1])
			m.filter()
		}
	case tea.KeySpace:
		m.query += " "
		m.filter()
	case tea.KeyRunes:
		if !k.Alt {
			m.query += string(k.Runes)
			m.filter()
		}
	}
	return m, -1, false
}

// View renders the overlay box; the app centers it over the editor.
func (m Model) View() string {
	w := min(m.width-6, 72) // content cells per row inside the box
	var sb strings.Builder
	sb.WriteString(titleStyle.Render(m.Title) + "  " + m.query + "█")

	first := max(0, min(m.sel-maxRows/2, len(m.matches)-maxRows))
	last := min(len(m.matches), first+maxRows)
	if len(m.matches) == 0 {
		sb.WriteString("\n" + detailStyle.Render("no matches"))
	}
	for i := first; i < last; i++ {
		sb.WriteByte('\n')
		sb.WriteString(m.renderRow(i, w))
	}
	return boxStyle.Render(sb.String())
}

// renderRow builds one exactly-w-cell row: " label……pad……detail ". Rows
// that fit the box exactly never wrap, which is what keeps one item on
// one line.
func (m Model) renderRow(i, w int) string {
	it := m.items[m.matches[i].Index]
	label, detail := []rune(it.Label), []rune(it.Detail)
	avail := w - 2 // leading/trailing space
	if len(detail) > avail/2 {
		detail = append(detail[:max(0, avail/2-1)], '…')
	}
	maxLabel := avail - len(detail) - 1
	clipped := false
	if len(label) > maxLabel {
		label, clipped = label[:max(1, maxLabel-1)], true
	}
	pad := max(1, avail-len(label)-len(detail))
	if clipped {
		label = append(label, '…')
		pad--
	}
	if i == m.sel {
		return selStyle.Render(" " + string(label) + strings.Repeat(" ", pad) + string(detail) + " ")
	}
	lit := highlight(string(label), m.matches[i].MatchedIndexes)
	return " " + lit + strings.Repeat(" ", pad) + detailStyle.Render(string(detail)) + " "
}

// highlight bolds the fuzzy-matched byte positions.
func highlight(s string, idx []int) string {
	if len(idx) == 0 {
		return s
	}
	set := map[int]bool{}
	for _, i := range idx {
		set[i] = true
	}
	var sb strings.Builder
	for i, r := range s { // i is the byte index, matching fuzzy's indexes
		if set[i] {
			sb.WriteString(matchStyle.Render(string(r)))
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
