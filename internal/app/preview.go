package app

// Markdown preview: renders the active .md buffer into a styled read-only
// virtual tab (same mechanism as the git graph). Static snapshot — rerun
// the action to refresh; reopening replaces the tab in place.
// ponytail: line/regex renderer, no goldmark dep; swap in a real parser if
// nested emphasis or tables ever matter.

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/GurYN/cove-editor/internal/editor"
)

// staticSyntax serves spans precomputed at render time. The buffer is
// read-only, so Edit never fires.
type staticSyntax struct{ spans []editor.HLSpan }

func (staticSyntax) Edit(int, int, int, [2]int, [2]int, [2]int)    {}
func (staticSyntax) Expand([]byte, int, int) (lo, hi int, ok bool) { return 0, 0, false }

func (s staticSyntax) Spans(_ []byte, startOff, endOff int) []editor.HLSpan {
	var out []editor.HLSpan
	for _, sp := range s.spans {
		if sp.End > startOff && sp.Start < endOff {
			out = append(out, sp)
		}
	}
	return out
}

func (m *Model) markdownPreview() {
	d := m.doc()
	if d == nil {
		return
	}
	switch strings.ToLower(filepath.Ext(d.path)) {
	case ".md", ".markdown":
		if d.virtual { // the preview tab itself, or a git view of one
			return
		}
	default:
		m.notify("markdown preview: not a markdown file")
		return
	}
	text, spans := renderMarkdown(d.ed.Buf.Bytes())
	m.openVirtualSyn("Preview: "+filepath.Base(d.path), text, staticSyntax{spans: spans})
}

var (
	mdHeadRe   = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdHRRe     = regexp.MustCompile(`^\s*(-{3,}|\*{3,}|_{3,})\s*$`)
	mdBulletRe = regexp.MustCompile(`^(\s*)[-*+]\s+`)
	mdQuoteRe  = regexp.MustCompile(`^\s*>\s?`)
	// One alternation, matched left to right: code first so backticks
	// protect their content from the emphasis/link rules.
	mdInlineRe = regexp.MustCompile("`[^`]+`" +
		`|\*\*[^*]+\*\*|__[^_]+__` +
		`|\*[^*\s][^*]*\*|_[^_\s][^_]*_` +
		`|!?\[[^\]]*\]\([^)]*\)`)
)

// mdRender accumulates the rendered text and its highlight spans.
type mdRender struct {
	b     strings.Builder
	spans []editor.HLSpan
}

// mark adds a span from start to the current end of the text.
func (r *mdRender) mark(start, class int) {
	if r.b.Len() > start {
		r.spans = append(r.spans, editor.HLSpan{Start: start, End: r.b.Len(), Class: class})
	}
}

// put writes s styled with class.
func (r *mdRender) put(s string, class int) {
	start := r.b.Len()
	r.b.WriteString(s)
	r.mark(start, class)
}

// renderMarkdown converts markdown source to display text plus highlight
// spans (byte offsets into the returned text, sorted by Start).
func renderMarkdown(src []byte) (string, []editor.HLSpan) {
	r := &mdRender{}
	lines := strings.Split(string(src), "\n")
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") { // fenced code → bordered box
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
				j++
			}
			r.codeBlock(lines[i+1 : j])
			i = min(j+1, len(lines)) // skip the closing fence
			continue
		}
		if strings.HasPrefix(trimmed, "|") { // ≥2 pipe lines → aligned table
			j := i
			for j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|") {
				j++
			}
			if j-i >= 2 {
				r.table(lines[i:j])
				i = j
				continue
			}
		}
		switch {
		case mdHeadRe.MatchString(line):
			g := mdHeadRe.FindStringSubmatch(line)
			r.put(g[2], editor.ClassFunction)
		case mdHRRe.MatchString(line):
			r.put(strings.Repeat("─", 40), editor.ClassComment)
		default:
			rest := line
			if q := mdQuoteRe.FindString(line); q != "" {
				r.put("│ ", editor.ClassComment)
				rest = line[len(q):]
			} else if g := mdBulletRe.FindStringSubmatch(line); g != nil {
				r.b.WriteString(g[1])
				r.put("• ", editor.ClassOperator)
				rest = line[len(g[0]):]
			}
			r.inline(rest)
		}
		r.b.WriteString("\n")
		i++
	}
	return strings.TrimSuffix(r.b.String(), "\n"), r.spans
}

// codeBlock draws the fence content in a rounded box sized to its longest
// line — the visual separation a background color would give, without a
// new theme slot.
func (r *mdRender) codeBlock(body []string) {
	w := 0
	for _, l := range body {
		w = max(w, lipgloss.Width(l))
	}
	r.put("╭"+strings.Repeat("─", w+2)+"╮", editor.ClassComment)
	r.b.WriteString("\n")
	for _, l := range body {
		r.put("│ ", editor.ClassComment)
		r.put(l, editor.ClassString)
		r.put(strings.Repeat(" ", w-lipgloss.Width(l))+" │", editor.ClassComment)
		r.b.WriteString("\n")
	}
	r.put("╰"+strings.Repeat("─", w+2)+"╯", editor.ClassComment)
	r.b.WriteString("\n")
}

// mdSepCellRe matches a table separator cell (---, :--:, …).
var mdSepCellRe = regexp.MustCompile(`^:?-+:?$`)

// table re-emits pipe rows with columns padded to a shared width. Cells are
// inline-rendered first so stripped markers don't skew the alignment.
func (r *mdRender) table(rows []string) {
	type cell struct {
		text  string
		spans []editor.HLSpan // relative to text
	}
	var grid [][]cell
	var widths []int
	sep := map[int]bool{}
	for ri, row := range rows {
		var cs []cell
		for ci, raw := range strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|") {
			raw = strings.TrimSpace(raw)
			if mdSepCellRe.MatchString(raw) {
				sep[ri] = true
				cs = append(cs, cell{})
				continue
			}
			txt, sp := renderInlineStr(raw)
			cs = append(cs, cell{text: txt, spans: sp})
			for len(widths) <= ci {
				widths = append(widths, 0)
			}
			widths[ci] = max(widths[ci], lipgloss.Width(txt))
		}
		grid = append(grid, cs)
	}
	rule := func(l, m, x string) {
		parts := make([]string, 0, len(widths))
		for _, w := range widths {
			parts = append(parts, strings.Repeat("─", w+2))
		}
		r.put(l+strings.Join(parts, m)+x, editor.ClassComment)
		r.b.WriteString("\n")
	}
	rule("┌", "┬", "┐")
	for ri, cs := range grid {
		if sep[ri] {
			rule("├", "┼", "┤")
			continue
		}
		for ci := 0; ci < len(widths); ci++ {
			r.put("│ ", editor.ClassComment)
			var c cell
			if ci < len(cs) {
				c = cs[ci]
			}
			off := r.b.Len()
			r.b.WriteString(c.text)
			for _, sp := range c.spans {
				r.spans = append(r.spans, editor.HLSpan{Start: off + sp.Start, End: off + sp.End, Class: sp.Class})
			}
			r.b.WriteString(strings.Repeat(" ", widths[ci]-lipgloss.Width(c.text)+1))
		}
		r.put("│", editor.ClassComment)
		r.b.WriteString("\n")
	}
	rule("└", "┴", "┘")
}

// inline strips inline markers, writing the visible text into r.
func (r *mdRender) inline(s string) {
	txt, sp := renderInlineStr(s)
	off := r.b.Len()
	r.b.WriteString(txt)
	for _, x := range sp {
		r.spans = append(r.spans, editor.HLSpan{Start: off + x.Start, End: off + x.End, Class: x.Class})
	}
}

// renderInlineStr strips inline markers (`code`, **bold**, *italic*, links),
// returning the visible text and a span per construct (offsets into text).
func renderInlineStr(s string) (string, []editor.HLSpan) {
	var b strings.Builder
	var spans []editor.HLSpan
	last := 0
	for _, loc := range mdInlineRe.FindAllStringIndex(s, -1) {
		b.WriteString(s[last:loc[0]])
		tok := s[loc[0]:loc[1]]
		start := b.Len()
		var class int
		switch {
		case tok[0] == '`':
			b.WriteString(tok[1 : len(tok)-1])
			class = editor.ClassString
		case strings.HasPrefix(tok, "**") || strings.HasPrefix(tok, "__"):
			b.WriteString(tok[2 : len(tok)-2])
			class = editor.ClassConstant
		case tok[0] == '*' || tok[0] == '_':
			b.WriteString(tok[1 : len(tok)-1])
			class = editor.ClassKeyword
		default: // link or image: keep the [text], drop the (url)
			txt := tok[strings.IndexByte(tok, '[')+1 : strings.IndexByte(tok, ']')]
			b.WriteString(txt)
			class = editor.ClassProperty
		}
		if b.Len() > start {
			spans = append(spans, editor.HLSpan{Start: start, End: b.Len(), Class: class})
		}
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String(), spans
}
