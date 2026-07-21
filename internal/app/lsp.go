package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
type wsSymsMsg struct{ syms []lsp.WorkspaceSym }
type actionsMsg struct {
	acts []lsp.CodeAction
	rev  int // editor revision at request time
}
type wsEditMsg struct {
	edit *lsp.WorkspaceEdit
	revs map[string]int // open-doc revisions at request time
}
type fmtMsg struct {
	path  string
	edits []lsp.TextEdit
	rev   int // editor revision at request time
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
	case "applyEdit":
		// Edits were computed against the last-synced text; a rev snapshot
		// taken now always matches, so only that 150ms sync window can skew.
		revs := make(map[string]int, len(m.docs))
		for _, d := range m.docs {
			revs[d.path] = d.ed.Rev
		}
		files, _ := m.applyWorkspaceEdit(ev.Edit, revs)
		m.lastMsg = fmt.Sprintf("applied edit in %d file(s)", files)
	case "showDocument":
		// gopls "browse …" actions send URLs; file-creating ones (add test,
		// extract to new file) send the file to reveal.
		if strings.HasPrefix(ev.URI, "file://") {
			path := lsp.URIToPath(ev.URI)
			m.openFile(path)
			if d := m.doc(); ev.Sel != nil && d != nil && same(d.path, path) {
				line := min(ev.Sel.Start.Line, d.ed.Buf.LineCount()-1)
				d.ed.Go(line, lsp.ByteCol(d.ed.Buf.Line(line), ev.Sel.Start.Character))
				d.ed.Center()
			}
		} else {
			openBrowser(ev.URI)
			m.lastMsg = "opened in browser"
		}
	}
}

// openBrowser hands a URL to the OS opener; unsupported platforms just
// leave the URL in the footer via the caller's message.
func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
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

// cmdCodeActions asks for fixes at the cursor, passing along any published
// diagnostics touching the cursor's line — servers key quickfixes off them.
func cmdCodeActions(m *Model) tea.Cmd {
	d := m.doc()
	if d == nil {
		return nil
	}
	rev := d.ed.Rev
	line, _ := d.ed.Cursor()
	buf := d.ed.Buf
	posOf := func(off int) lsp.Position {
		l, c := buf.Pos(min(off, buf.Len()))
		return lsp.Position{Line: l, Character: lsp.UTF16Col(buf.Line(l), c)}
	}
	var diags []lsp.Diagnostic
	for _, sp := range d.ed.Diags {
		if l, _ := buf.Pos(min(sp.Start, buf.Len())); l == line {
			diags = append(diags, lsp.Diagnostic{
				Range:    lsp.Range{Start: posOf(sp.Start), End: posOf(sp.End)},
				Severity: sp.Severity,
				Message:  sp.Message,
			})
		}
	}
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		acts, err := c.CodeActions(ctx, uri, lsp.Range{Start: pos, End: pos}, diags)
		if err != nil {
			return lspErrMsg{err}
		}
		return actionsMsg{acts: acts, rev: rev}
	})
}

func cmdExecuteCommand(m *Model, act lsp.CodeAction) tea.Cmd {
	d := m.doc()
	if d == nil {
		return nil
	}
	c := m.lspm.Client(d.path)
	if c == nil {
		return nil
	}
	name, args, ok := act.Cmd()
	if !ok {
		m.lastMsg = "code action has no edit or command"
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		if err := c.ExecuteCommand(ctx, name, args); err != nil {
			return lspErrMsg{err}
		}
		return nil
	}
}

func cmdWorkspaceSymbols(m *Model, query string) tea.Cmd {
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		syms, err := c.WorkspaceSymbols(ctx, query)
		if err != nil {
			return lspErrMsg{err}
		}
		return wsSymsMsg{syms}
	})
}

func cmdRename(m *Model, newName string) tea.Cmd {
	// Snapshot open-doc revisions: edits computed now must not land on
	// buffers the user has since typed in (stale offsets corrupt text).
	revs := make(map[string]int, len(m.docs))
	for _, d := range m.docs {
		revs[d.path] = d.ed.Rev
	}
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		we, err := c.Rename(ctx, uri, pos, newName)
		if err != nil {
			return lspErrMsg{err}
		}
		return wsEditMsg{edit: we, revs: revs}
	})
}

func cmdFormat(m *Model) tea.Cmd {
	d := m.doc()
	if d == nil {
		return nil
	}
	path, rev := d.path, d.ed.Rev
	return m.lspCmd(func(c *lsp.Client, uri string, pos lsp.Position) tea.Msg {
		ctx, cancel := lsp.Ctx()
		defer cancel()
		edits, err := c.Format(ctx, uri)
		if err != nil {
			return lspErrMsg{err}
		}
		return fmtMsg{path: path, edits: edits, rev: rev}
	})
}

// ---- applying results ----

func (m *Model) jumpTo(loc lsp.Location) {
	path := lsp.URIToPath(loc.URI)
	m.pushJump()
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

// applyWorkspaceEdit applies a workspace edit (rename, code action): open
// docs get an undoable transaction; closed files are edited on disk. Docs
// edited since the request (rev mismatch) are skipped — the offsets no
// longer fit. Callers compose the status message from the counts.
func (m *Model) applyWorkspaceEdit(we *lsp.WorkspaceEdit, revs map[string]int) (files, stale int) {
	if we == nil {
		return 0, 0
	}
	var created []string
	for uri, edits := range we.Changes {
		if len(edits) == 0 {
			continue
		}
		path := lsp.URIToPath(uri)
		if d := m.docByPath(path); d != nil {
			if rev, ok := revs[d.path]; !ok || rev != d.ed.Rev {
				stale++
				continue
			}
			files++
			d.ed.ApplyEdits(toEditorEdits(d.ed.Buf, edits))
			m.lastMsg = d.save()
			continue
		}
		files++
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			m.lastMsg = err.Error()
			continue
		}
		if os.IsNotExist(err) { // server creating a file (add test, extract)
			data = nil
			os.MkdirAll(filepath.Dir(path), 0o755)
			created = append(created, path)
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
	if len(created) > 0 { // reveal what the server just made
		m.side.Refresh()
		m.openFile(created[0])
	}
	return files, stale
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

// openWorkspaceSymbols lists project-wide symbol matches; rows reuse the
// references overlay plumbing: pick → jump (which records a jump-list entry).
func (m Model) openWorkspaceSymbols(syms []lsp.WorkspaceSym) Model {
	if len(syms) == 0 {
		m.lastMsg = "no matching symbols"
		return m
	}
	m.ovKind = overlayRefs
	m.ovRefs = m.ovRefs[:0]
	items := make([]overlay.Item, len(syms))
	for i, s := range syms {
		m.ovRefs = append(m.ovRefs, s.Location)
		path := lsp.URIToPath(s.Location.URI)
		items[i] = overlay.Item{
			Label:  symbolGlyph(s.Kind) + " " + s.Name,
			Detail: fmt.Sprintf("%s:%d", filepath.Base(path), s.Location.Range.Start.Line+1),
		}
	}
	m.ov = overlay.New("Symbol:", items, m.width)
	return m
}

// openCodeActions shows the fixes/refactors the server offered at the cursor.
func (m Model) openCodeActions(msg actionsMsg) Model {
	if len(msg.acts) == 0 {
		m.lastMsg = "no code actions here"
		return m
	}
	m.ovKind = overlayActions
	m.ovActs = msg.acts
	m.ovActsRev = msg.rev
	items := make([]overlay.Item, len(msg.acts))
	for i, a := range msg.acts {
		items[i] = overlay.Item{Label: a.Title, Detail: a.Kind}
	}
	m.ov = overlay.New("Fix:", items, m.width)
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
