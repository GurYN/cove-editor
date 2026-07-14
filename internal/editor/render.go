package editor

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// Style classes emitted by the highlighter; index into classStyles.
const (
	ClassNone = iota
	ClassKeyword
	ClassString
	ClassComment
	ClassNumber
	ClassFunction
	ClassType
	ClassConstant
	ClassProperty
	ClassOperator
	numClasses
)

// paint layers stacked on top of syntax classes, in priority order
const (
	paintInfo = numClasses + iota
	paintWarn
	paintErr
	paintMatch
	paintSelection
	paintCursor
)

// themeSlots maps theme entry names to paint indexes. Values are lipgloss
// colors (hex for truecolor, numbers for ANSI-256); lipgloss degrades
// automatically on lesser terminals.
var themeSlots = map[string]int{
	"keyword": ClassKeyword, "string": ClassString, "comment": ClassComment,
	"number": ClassNumber, "function": ClassFunction, "type": ClassType,
	"constant": ClassConstant, "property": ClassProperty, "operator": ClassOperator,
	"info": paintInfo, "warning": paintWarn, "error": paintErr,
	"match": paintMatch, "selection": paintSelection,
}

var paintStyles = make([]lipgloss.Style, paintCursor+1)

// Standalone default so the editor styles sensibly without the app's
// config layer (tests, embedding). The app overrides at startup.
func init() {
	ApplyTheme(map[string]string{
		"keyword": "176", "string": "108", "comment": "245", "number": "179",
		"function": "74", "type": "115", "constant": "173", "property": "152",
		"operator": "246", "info": "81", "warning": "214", "error": "203",
		"match": "58", "selection": "24",
	})
}

// ApplyTheme installs a name->color theme; entries it doesn't set keep
// their zero style. Called once at startup (and by tests).
func ApplyTheme(colors map[string]string) {
	s := make([]lipgloss.Style, paintCursor+1)
	for name, idx := range themeSlots {
		c, ok := colors[name]
		if !ok {
			continue
		}
		switch idx {
		case paintMatch, paintSelection:
			s[idx] = lipgloss.NewStyle().Background(lipgloss.Color(c))
		case paintInfo, paintWarn, paintErr:
			s[idx] = lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Underline(true)
		default:
			s[idx] = lipgloss.NewStyle().Foreground(lipgloss.Color(c))
		}
	}
	if colors["comment"] != "" {
		s[ClassComment] = s[ClassComment].Italic(true)
	}
	s[paintCursor] = lipgloss.NewStyle().Reverse(true)
	paintStyles = s
	if c := colors["gutter"]; c != "" {
		gutterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(c))
		gutterCurStyle = gutterStyle.Bold(true)
	}
	if c := colors["property"]; c != "" {
		gutterCurStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true)
	}
}

func diagPaint(severity int) int {
	switch severity {
	case 1:
		return paintErr
	case 2:
		return paintWarn
	default:
		return paintInfo
	}
}

// tabStop is set from config at startup (default 4).
var tabStop = 4

// SetTabStop configures tab width in cells.
func SetTabStop(n int) {
	if n > 0 && n <= 16 {
		tabStop = n
	}
}

var (
	showLineNumbers = true
	gutterStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	gutterCurStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Bold(true)
)

// SetLineNumbers toggles the line-number gutter (config + palette action).
func SetLineNumbers(on bool) { showLineNumbers = on }

// LineNumbersEnabled reports the current gutter setting.
func LineNumbersEnabled() bool { return showLineNumbers }

// gutterW is the gutter width in cells, 0 when disabled.
func (m Model) gutterW() int {
	if !showLineNumbers {
		return 0
	}
	digits := len(strconv.Itoa(m.Buf.LineCount()))
	return max(digits, 3) + 2 // " 123 "
}

// textWidth is the width left for buffer content.
func (m Model) textWidth() int { return max(1, m.Width-m.gutterW()) }

// cell is one screen cell: a rune, its screen x, and the byte column it
// came from. Tabs expand to several cells sharing one byte column.
type cell struct {
	r   rune
	x   int
	col int // byte column within the line
}

// lineCells decodes a line into screen cells, expanding tabs and mapping
// invalid UTF-8 to U+FFFD.
// ponytail: every rune is 1 cell wide; CJK/emoji double-width needs
// go-runewidth here and in hitTest.
func lineCells(line []byte) []cell { return lineCellsTo(line, 1<<30) }

// lineCellsTo stops decoding once x exceeds maxX, so multi-megabyte single
// lines cost O(visible width) to render, not O(line).
func lineCellsTo(line []byte, maxX int) []cell {
	cells := make([]cell, 0, min(len(line), maxX+8))
	x := 0
	for col := 0; col < len(line) && x <= maxX; {
		r, size := utf8.DecodeRune(line[col:])
		if r == '\t' {
			next := (x/tabStop + 1) * tabStop
			for ; x < next; x++ {
				cells = append(cells, cell{r: ' ', x: x, col: col})
			}
			col++
			continue
		}
		if r == utf8.RuneError && size == 1 {
			r = '�'
		}
		if r == 0x7f {
			r = '␡'
		} else if r < 0x20 {
			r = rune(0x2400 + r) // control picture ␀..␟, never raw
		}
		cells = append(cells, cell{r: r, x: x, col: col})
		x++
		col += size
	}
	return cells
}

// CursorScreen returns the primary cursor's position in pane cells
// (x, y relative to the viewport's top-left, gutter included).
func (m Model) CursorScreen() (int, int) {
	line, col := m.Buf.Pos(m.cursors[m.primary].Head)
	cx := (&m).cursorCellX(line, col)
	return cx - m.xoff + m.gutterW(), line - m.top
}

// View renders only the visible window: O(visible area), never O(file).
func (m Model) View() string {
	if m.Height <= 0 {
		return ""
	}
	m.keepCursorHVisible()
	last := min(m.top+m.Height, m.Buf.LineCount())
	startOff := m.Buf.Offset(m.top, 0)
	endOff := m.Buf.Len()
	if last < m.Buf.LineCount() {
		endOff = m.Buf.Offset(last, 0)
	}

	var spans []HLSpan
	if m.Syntax != nil {
		spans = m.Syntax.Spans(m.Buf.Bytes(), startOff, endOff)
	}
	matches := m.visibleMatches(startOff, endOff)

	curLine, _ := m.Buf.Pos(m.cursors[m.primary].Head)
	gw := m.gutterW()
	var sb strings.Builder
	for i := m.top; i < last; i++ {
		if i > m.top {
			sb.WriteByte('\n')
		}
		if gw > 0 {
			num := fmt.Sprintf(" %*d ", gw-2, i+1)
			if i == curLine {
				sb.WriteString(gutterCurStyle.Render(num))
			} else {
				sb.WriteString(gutterStyle.Render(num))
			}
		}
		m.renderLine(&sb, i, spans, matches)
	}
	for i := last; i < m.top+m.Height; i++ {
		if i > m.top {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// cursorCellX returns the screen x of the cursor by decoding only the
// line prefix before it — O(col), never O(line). It fetches exactly the
// prefix bytes from the rope so multi-megabyte lines cost nothing.
func (m *Model) cursorCellX(line, col int) int {
	start := m.Buf.Offset(line, 0)
	cells := lineCells(m.Buf.Slice(start, start+col))
	if len(cells) == 0 {
		return 0
	}
	return cells[len(cells)-1].x + 1
}

// keepCursorHVisible adjusts xoff so the primary cursor is on screen.
// Model is a value in View; the receiver is a pointer via a copy trick in
// callers — xoff drift across frames is fine because Update recomputes.
func (m *Model) keepCursorHVisible() {
	line, col := m.Buf.Pos(m.cursors[m.primary].Head)
	cx := m.cursorCellX(line, col)
	if cx < m.xoff {
		m.xoff = cx
	}
	if tw := m.textWidth(); m.Width > 0 && cx >= m.xoff+tw {
		m.xoff = cx - tw + 1
	}
}

func (m Model) renderLine(sb *strings.Builder, lineIdx int, spans []HLSpan, matches [][2]int) {
	lineStart := m.Buf.Offset(lineIdx, 0)
	ll := m.Buf.LineLen(lineIdx)
	// Fetch only the bytes the visible window can need (4 bytes/rune max);
	// Buffer.Line would gather the whole line from the rope.
	maxB := min(ll, (m.xoff+m.textWidth())*4+8)
	raw := m.Buf.Slice(lineStart, lineStart+maxB)
	cells := lineCellsTo(raw, m.xoff+m.textWidth())
	lineEnd := lineStart + ll

	// One paint id per visible cell, plus one slot for a cursor sitting at
	// end-of-line.
	width := m.textWidth()
	if m.Width <= 0 {
		width = len(cells) + 1
	}
	paint := make([]int, width+1)

	setRange := func(lo, hi, id int) { // byte offsets, absolute
		for i, c := range cells {
			x := c.x - m.xoff
			if x < 0 || x >= width {
				continue
			}
			off := lineStart + c.col
			if off >= lo && off < hi {
				paint[x] = id
			}
			_ = i
		}
	}

	for _, s := range spans {
		if s.End > lineStart && s.Start < lineEnd {
			setRange(s.Start, s.End, s.Class)
		}
	}
	for _, d := range m.Diags {
		end := min(d.End, m.Buf.Len())
		if end > lineStart && d.Start < lineEnd {
			setRange(d.Start, max(d.Start+1, end), diagPaint(d.Severity))
		}
	}
	for _, r := range matches {
		setRange(r[0], r[1], paintMatch)
	}
	for _, c := range m.cursors {
		lo, hi := c.sel()
		if hi > lineStart && lo < lineEnd+1 { // +1: selection may cover the newline
			setRange(lo, hi, paintSelection)
		}
	}
	// Cursor cells last, on top of everything.
	cursorAt := func(off int) int { // screen x of byte offset, -1 if hidden
		if off < lineStart || off > lineEnd {
			return -1
		}
		x := 0
		for _, c := range cells {
			if lineStart+c.col >= off {
				x = c.x
				break
			}
			x = c.x + 1
		}
		x -= m.xoff
		if x < 0 || x > width {
			return -1
		}
		return x
	}
	for _, c := range m.cursors {
		if x := cursorAt(c.Head); x >= 0 {
			paint[x] = paintCursor
		}
	}

	// Emit runs of equal paint id.
	runeAt := make([]rune, width+1)
	for i := range runeAt {
		runeAt[i] = ' '
	}
	visible := 0
	for _, c := range cells {
		x := c.x - m.xoff
		if x >= 0 && x < width {
			runeAt[x] = c.r
			if x+1 > visible {
				visible = x + 1
			}
		}
	}
	// Include a trailing cell if a cursor or selection sits at EOL.
	for i := visible; i <= width; i++ {
		if paint[i] != ClassNone {
			visible = i + 1
		}
	}
	for start := 0; start < visible; {
		id := paint[start]
		end := start + 1
		for end < visible && paint[end] == id {
			end++
		}
		chunk := string(runeAt[start:end])
		if id == ClassNone {
			sb.WriteString(chunk)
		} else {
			sb.WriteString(paintStyles[id].Render(chunk))
		}
		start = end
	}
}
