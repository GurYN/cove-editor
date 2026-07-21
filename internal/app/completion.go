package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/lsp"
)

var (
	complBoxStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("62"))
	complSelStyle = lipgloss.NewStyle().Reverse(true)
	complDimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	hoverStyle    = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("62")).Padding(0, 1)
)

const complRows = 8

type complState struct {
	active bool
	items  []lsp.CompletionItem
	filter string // characters typed since the menu opened
	sel    int
}

func (c *complState) filtered() []int {
	out := make([]int, 0, len(c.items))
	f := strings.ToLower(c.filter)
	for i, it := range c.items {
		if f == "" || strings.HasPrefix(strings.ToLower(it.Label), f) {
			out = append(out, i)
		}
	}
	if len(out) == 0 { // fall back to substring match
		for i, it := range c.items {
			if strings.Contains(strings.ToLower(it.Label), f) {
				out = append(out, i)
			}
		}
	}
	return out
}

// handleComplKey intercepts keys while the menu is open. handled=false
// means the caller should dispatch the key normally.
func (m Model) handleComplKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	c := &m.compl
	switch k.Type {
	case tea.KeyEscape:
		c.active = false
		return m, nil, true
	case tea.KeyUp:
		c.sel = max(0, c.sel-1)
		return m, nil, true
	case tea.KeyDown:
		c.sel = min(len(c.filtered())-1, c.sel+1)
		return m, nil, true
	case tea.KeyEnter, tea.KeyTab:
		m.acceptCompletion()
		return m, m.syncLSP(), true
	case tea.KeyBackspace:
		if c.filter == "" {
			c.active = false
			return m, nil, false
		}
		r := []rune(c.filter)
		c.filter = string(r[:len(r)-1])
		c.sel = 0
		return m, nil, false // also delete in the editor
	case tea.KeyRunes:
		if !k.Alt && len(k.Runes) == 1 && isIdentRune(k.Runes[0]) {
			c.filter += string(k.Runes)
			c.sel = 0
			return m, nil, false // also insert into the editor
		}
	}
	c.active = false
	return m, nil, false
}

func isIdentRune(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func (m *Model) acceptCompletion() {
	d := m.doc()
	idx := m.compl.filtered()
	if d == nil || len(idx) == 0 {
		m.compl.active = false
		return
	}
	it := m.compl.items[idx[m.compl.sel]]
	m.compl.active = false

	newText := it.InsertText
	if newText == "" {
		newText = it.Label
	}
	var start, end int
	if it.TextEdit != nil {
		// Positions were captured at request time; the filter characters
		// typed since sit between them and the cursor.
		start = offsetOf(d.ed.Buf, it.TextEdit.Range.Start)
		end = offsetOf(d.ed.Buf, it.TextEdit.Range.End) + len(m.compl.filter)
		newText = it.TextEdit.NewText
	} else {
		// Replace the identifier fragment before the cursor.
		line, col := d.ed.Cursor()
		lineBytes := d.ed.Buf.Line(line)
		s := col
		for s > 0 && isIdentRune(rune(lineBytes[s-1])) {
			s--
		}
		start = d.ed.Buf.Offset(line, s)
		end = d.ed.Buf.Offset(line, col)
	}
	end = min(end, d.ed.Buf.Len())
	if start > end {
		start = end
	}
	// Some servers (vscode-html-languageserver's close-tag completion) send
	// snippet syntax like "$0</html>" even though we declare snippetSupport
	// false. Strip tab stops/placeholders; caret is where the cursor lands.
	caret := -1
	if it.InsertTextFormat == 2 || strings.Contains(newText, "$0") {
		newText, caret = stripSnippet(newText)
	}
	d.ed.ApplyEdits([]editor.Edit{{Off: start, Old: append([]byte(nil), d.ed.Buf.Slice(start, end)...), New: []byte(newText)}})
	if caret >= 0 {
		line, col := d.ed.Buf.Pos(start + caret)
		d.ed.Go(line, col)
	}
}

// stripSnippet removes LSP snippet syntax ($1, ${1}, ${1:placeholder} —
// placeholder text is kept) and returns the byte offset of the first real
// tab stop (else $0) as the caret position, -1 if there is none.
// ponytail: no nested placeholders or choice syntax; extend if a server sends them.
func stripSnippet(s string) (string, int) {
	var out strings.Builder
	first, zeroPos := -1, -1
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) { // \$ etc. — unescape
			out.WriteByte(s[i+1])
			i += 2
			continue
		}
		if s[i] != '$' || i+1 >= len(s) {
			out.WriteByte(s[i])
			i++
			continue
		}
		j, zero := i+1, false
		if s[j] == '{' {
			j++
		}
		n := j
		for n < len(s) && s[n] >= '0' && s[n] <= '9' {
			n++
		}
		if n == j { // lone $, not a tab stop
			out.WriteByte(s[i])
			i++
			continue
		}
		zero = s[j:n] == "0"
		if s[i+1] == '{' {
			if n < len(s) && s[n] == ':' { // ${1:placeholder}
				end := strings.IndexByte(s[n:], '}')
				if end < 0 {
					out.WriteString(s[i:])
					break
				}
				if zero {
					zeroPos = out.Len()
				} else if first < 0 {
					first = out.Len()
				}
				out.WriteString(s[n+1 : n+end])
				i = n + end + 1
				continue
			}
			if n < len(s) && s[n] == '}' {
				n++
			}
		}
		if zero {
			zeroPos = out.Len()
		} else if first < 0 {
			first = out.Len()
		}
		i = n
	}
	if first < 0 {
		first = zeroPos
	}
	return out.String(), first
}

// renderCompl renders the completion menu box (no positioning).
func (m Model) renderCompl() string {
	idx := m.compl.filtered()
	if len(idx) == 0 {
		return complBoxStyle.Render(complDimStyle.Render(" no matches "))
	}
	first := max(0, min(m.compl.sel-complRows/2, len(idx)-complRows))
	last := min(len(idx), first+complRows)
	w := 0
	for _, i := range idx[first:last] {
		it := m.compl.items[i]
		w = max(w, len(it.Label)+len(firstLine(it.Detail))+3)
	}
	w = min(w, 60)
	var rows []string
	for n := first; n < last; n++ {
		it := m.compl.items[idx[n]]
		row := " " + it.Label
		if det := firstLine(it.Detail); det != "" {
			pad := max(1, w-len(it.Label)-len(det)-2)
			row += strings.Repeat(" ", pad) + complDimStyle.Render(det)
		}
		row += " "
		if n == m.compl.sel {
			row = complSelStyle.Render(" " + it.Label + " ")
			if det := firstLine(it.Detail); det != "" {
				row = complSelStyle.Render(" " + it.Label + strings.Repeat(" ", max(1, w-len(it.Label)-len(det)-2)) + det + " ")
			}
		}
		rows = append(rows, row)
	}
	return complBoxStyle.Render(strings.Join(rows, "\n"))
}

// renderToast renders the diagnostic-under-cursor card, colored by
// severity, shown bottom-right above the status bar.
func (m Model) renderToast(d editor.DiagSpan) string {
	color, badge := "81", "○ info"
	switch d.Severity {
	case 1:
		color, badge = "203", "● error"
	case 2:
		color, badge = "214", "▲ warning"
	}
	w := min(60, max(24, m.width/3))
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(color)).
		Padding(0, 1).
		Width(w)
	title := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(badge)
	msg := d.Message
	if len(msg) > 300 {
		msg = msg[:297] + "…"
	}
	return style.Render(title + "\n" + msg)
}

func (m Model) renderHover() string {
	lines := strings.Split(m.hoverText, "\n")
	if len(lines) > 12 {
		lines = append(lines[:12], "…")
	}
	for i, l := range lines {
		if len(l) > 76 {
			lines[i] = l[:76] + "…"
		}
	}
	return hoverStyle.Render(strings.Join(lines, "\n"))
}
