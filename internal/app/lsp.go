package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/buffer"
	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/lsp"
	"github.com/GurYN/cove-editor/internal/overlay"
)

// ---- messages flowing back into the bubbletea loop ----

type lspEventMsg lsp.Event
type changeTickMsg struct{}

type defMsg struct{ locs []lsp.Location }
type refsMsg struct{ locs []lsp.Location }
type hoverMsg struct{ text string }
type complMsg struct {
	items []lsp.CompletionItem
	rev   int // editor revision at request time
}
type symsMsg struct{ syms []lsp.DocumentSymbol }
type wsEditMsg struct{ edit *lsp.WorkspaceEdit }
type fmtMsg struct {
	path  string
	edits []lsp.TextEdit
}
type lspErrMsg struct{ err error }

// listenLSP pumps one manager event into the update loop; the handler
// re-issues it.
func listenLSP(m *lsp.Manager) tea.Cmd {
	return func() tea.Msg { return lspEventMsg(<-m.Events()) }
}

func (m *Model) handleLSPEvent(ev lsp.Event) {
	switch ev.Kind {
	case "status":
		m.lspStatus[ev.Lang] = ev.Status
	case "diagnostics":
		path := lsp.URIToPath(ev.URI)
		for _, d := range m.docs {
			if same(d.path, path) {
				d.ed.Diags = toDiagSpans(d.ed.Buf, ev.Diagnostics)
			}
		}
	}
}

// ---- position conversion ----

func toDiagSpans(buf *buffer.Buffer, diags []lsp.Diagnostic) []editor.DiagSpan {
	out := make([]editor.DiagSpan, 0, len(diags))
	for _, d := range diags {
		out = append(out, editor.DiagSpan{
			Start:    offsetOf(buf, d.Range.Start),
			End:      offsetOf(buf, d.Range.End),
			Severity: d.Severity,
			Message:  d.Message,
		})
	}
	return out
}

func offsetOf(buf *buffer.Buffer, p lsp.Position) int {
	line := min(p.Line, buf.LineCount()-1)
	return buf.Offset(line, lsp.ByteCol(buf.Line(line), p.Character))
}

func (m *Model) cursorPosition() (string, lsp.Position, bool) {
	d := m.doc()
	if d == nil {
		return "", lsp.Position{}, false
	}
	line, col := d.ed.Cursor()
	p := lsp.Position{Line: line, Character: lsp.UTF16Col(d.ed.Buf.Line(line), col)}
	return lsp.PathToURI(d.path), p, true
}

// ---- didChange debounce (main-loop side) ----

// syncLSP schedules a change tick if the buffer moved since the last sync.
func (m *Model) syncLSP() tea.Cmd {
	d := m.doc()
	if d == nil || m.changePending || d.ed.Rev == d.sentRev {
		return nil
	}
	m.changePending = true
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return changeTickMsg{} })
}

func (m *Model) flushChange() tea.Cmd {
	m.changePending = false
	d := m.doc()
	if d == nil || d.ed.Rev == d.sentRev {
		return nil
	}
	d.sentRev = d.ed.Rev
	m.lspm.Change(d.path, d.sentRev, d.ed.Buf.Bytes())
	m.updateSigns(d)   // git gutter rides the same debounce
	return m.syncLSP() // buffer may already have moved again
}

// ---- feature commands ----

func (m *Model) lspCmd(f func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg) tea.Cmd {
	uri, pos, ok := m.cursorPosition()
	if !ok {
		return nil
	}
	c := m.lspm.Client(m.doc().path)
	if c == nil {
		m.lastMsg = "no language server for this file"
		return nil
	}
	return func() tea.Msg { return f(c, uri, pos) }
}

func cmdDefinition(m *Model) tea.Cmd {
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		locs, err := c.Definition(ctx, uri, pos)
		if err != nil {
			return lspErrMsg{err}
		}
		return defMsg{locs}
	})
}

func cmdReferences(m *Model) tea.Cmd {
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		locs, err := c.References(ctx, uri, pos)
		if err != nil {
			return lspErrMsg{err}
		}
		return refsMsg{locs}
	})
}

func cmdHover(m *Model) tea.Cmd {
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		text, err := c.Hover(ctx, uri, pos)
		if err != nil {
			return lspErrMsg{err}
		}
		return hoverMsg{text}
	})
}

func cmdCompletion(m *Model) tea.Cmd {
	rev := 0
	if d := m.doc(); d != nil {
		rev = d.ed.Rev
	}
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		items, err := c.Completion(ctx, uri, pos)
		if err != nil {
			return lspErrMsg{err}
		}
		return complMsg{items: items, rev: rev}
	})
}

func cmdSymbols(m *Model) tea.Cmd {
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		syms, err := c.DocumentSymbols(ctx, uri)
		if err != nil {
			return lspErrMsg{err}
		}
		return symsMsg{syms}
	})
}

func cmdRename(m *Model, newName string) tea.Cmd {
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		we, err := c.Rename(ctx, uri, pos, newName)
		if err != nil {
			return lspErrMsg{err}
		}
		return wsEditMsg{we}
	})
}

func cmdFormat(m *Model) tea.Cmd {
	d := m.doc()
	if d == nil {
		return nil
	}
	path := d.path
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		edits, err := c.Format(ctx, uri)
		if err != nil {
			return lspErrMsg{err}
		}
		return fmtMsg{path: path, edits: edits}
	})
}

// ---- applying results ----

func (m *Model) jumpTo(loc lsp.Location) {
	path := lsp.URIToPath(loc.URI)
	m.openFile(path)
	d := m.doc()
	if d == nil || !same(d.path, path) {
		return
	}
	line := min(loc.Range.Start.Line, d.ed.Buf.LineCount()-1)
	col := lsp.ByteCol(d.ed.Buf.Line(line), loc.Range.Start.Character)
	d.ed.Go(line, col)
	d.ed.Center()
}

// toEditorEdits converts LSP edits into ascending, offset-based editor
// edits against buf.
func toEditorEdits(buf *buffer.Buffer, edits []lsp.TextEdit) []editor.Edit {
	out := make([]editor.Edit, 0, len(edits))
	for _, e := range edits {
		start := offsetOf(buf, e.Range.Start)
		end := offsetOf(buf, e.Range.End)
		out = append(out, editor.Edit{
			Off: start,
			Old: append([]byte(nil), buf.Slice(start, end)...),
			New: []byte(e.NewText),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	return out
}

// applyWorkspaceEdit applies a rename result: open docs get an undoable
// transaction; closed files are edited on disk.
func (m *Model) applyWorkspaceEdit(we *lsp.WorkspaceEdit) {
	if we == nil {
		return
	}
	files := 0
	for uri, edits := range we.Changes {
		if len(edits) == 0 {
			continue
		}
		files++
		path := lsp.URIToPath(uri)
		if d := m.docByPath(path); d != nil {
			d.ed.ApplyEdits(toEditorEdits(d.ed.Buf, edits))
			m.lastMsg = d.save()
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			m.lastMsg = err.Error()
			continue
		}
		buf := buffer.New(data)
		ee := toEditorEdits(buf, edits)
		for _, e := range slices.Backward(ee) {
			buf.Delete(e.Off, e.Off+len(e.Old))
			buf.Insert(e.Off, e.New)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			m.lastMsg = err.Error()
		}
	}
	m.lastMsg = fmt.Sprintf("renamed in %d file(s)", files)
}

func (m *Model) docByPath(path string) *doc {
	for _, d := range m.docs {
		if same(d.path, path) {
			return d
		}
	}
	return nil
}

// ---- references overlay ----

func (m Model) openReferences(locs []lsp.Location) Model {
	if len(locs) == 0 {
		m.lastMsg = "no references"
		return m
	}
	m.ovKind = overlayRefs
	m.ovRefs = locs
	items := make([]overlay.Item, len(locs))
	for i, l := range locs {
		label, detail := refLabel(m.side.Root, l)
		items[i] = overlay.Item{Label: label, Detail: detail}
	}
	m.ov = overlay.New("References:", items, m.width)
	return m
}

// lspStatusLine renders the language-server segment of the status bar.
func (m Model) lspStatusLine(d *doc) string {
	lang := lsp.LangFor(d.path)
	if lang == "" {
		return ""
	}
	glyph := map[string]string{"starting": "…", "ready": "✓", "dead": "✗"}[m.lspStatus[lang]]
	if glyph == "" {
		glyph = "·"
	}
	e, w, i := d.ed.DiagCounts()
	s := fmt.Sprintf("%s%s", lang, glyph)
	if e > 0 {
		s += fmt.Sprintf(" %d●", e)
	}
	if w > 0 {
		s += fmt.Sprintf(" %d▲", w)
	}
	if i > 0 {
		s += fmt.Sprintf(" %d○", i)
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:57] + "…"
	}
	return s
}

func refLabel(root string, l lsp.Location) (string, string) {
	path := lsp.URIToPath(l.URI)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	return fmt.Sprintf("%s:%d", filepath.Base(path), l.Range.Start.Line+1), filepath.Dir(rel)
}
