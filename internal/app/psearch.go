package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/sidebar"
)

// Project-wide search and replace. Search walks the workspace (same file
// list as the fuzzy finder: .gitignore honored, 20k-file cap) and lists
// hits grouped by file in the search panel (sidebar slot, VSCode-style):
// click or Enter jumps to the hit, i/x set include/exclude glob filters.
// Replace is exact-case and literal, applied undoably to open docs and
// directly on disk for the rest.

const (
	maxSearchHits = 2000
	maxSearchFile = 1 << 20 // skip files over 1 MB — logs, bundles, minified junk
)

type psearchHit struct {
	ref  problemRef
	text string
}

type psearchMsg struct {
	query     string
	hits      []psearchHit
	truncated bool
}

// searchRow is one panel line: a file header (header != "") or a hit.
type searchRow struct {
	header string // workspace-relative path; "" = hit row
	ref    problemRef
	text   string
}

// searchPanel is the project-search results panel occupying the sidebar
// slot. inc/exc are comma-separated glob filters applied to workspace-
// relative paths on the next search.
type searchPanel struct {
	view      bool
	query     string
	inc, exc  string
	rows      []searchRow
	sel, top  int
	hits      int
	truncated bool
}

func (p *searchPanel) scroll(h int) {
	if p.sel < p.top {
		p.top = p.sel
	}
	if p.sel >= p.top+h {
		p.top = p.sel - h + 1
	}
}

// move moves the selection to the next hit row, skipping file headers.
func (p *searchPanel) move(d, h int) {
	for i := p.sel + d; i >= 0 && i < len(p.rows); i += d {
		if p.rows[i].header == "" {
			p.sel = i
			p.scroll(h)
			return
		}
	}
}

func (p *searchPanel) wheel(delta, h int) {
	p.top = clampInt(p.top+delta, 0, max(0, len(p.rows)-h))
}

func (p *searchPanel) selected() (searchRow, bool) {
	if p.sel < len(p.rows) && p.rows[p.sel].header == "" {
		return p.rows[p.sel], true
	}
	return searchRow{}, false
}

// splitGlobs parses a comma-separated filter string into patterns.
func splitGlobs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(p), "/")); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// matchGlob reports whether pattern p matches the workspace-relative path.
// Matching is per path segment and unanchored (like .gitignore): the
// pattern may match anywhere in the path, "*" stays within one segment,
// and "**" crosses directories — "*.c", "vendor", "syntax/*", and
// "**/syntax/**" all do what a VSCode user expects.
func matchGlob(p, rel string) bool {
	psegs := append([]string{"**"}, strings.Split(strings.Trim(p, "/"), "/")...)
	psegs = append(psegs, "**")
	return matchSegs(psegs, strings.Split(rel, string(filepath.Separator)))
}

// matchSegs matches pattern segments against path segments; "**" matches
// any run of segments, including none.
func matchSegs(psegs, segs []string) bool {
	if len(psegs) == 0 {
		return len(segs) == 0
	}
	if psegs[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegs(psegs[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, _ := filepath.Match(psegs[0], segs[0]); !ok {
		return false
	}
	return matchSegs(psegs[1:], segs[1:])
}

func matchGlobs(pats []string, rel string) bool {
	for _, p := range pats {
		if matchGlob(p, rel) {
			return true
		}
	}
	return false
}

// bufOverrides snapshots open (non-virtual) doc buffers keyed by absolute
// path, so search and replace see unsaved edits, not stale disk state.
func (m *Model) bufOverrides() map[string][]byte {
	out := map[string][]byte{}
	for _, d := range m.docs {
		if d.virtual {
			continue
		}
		if abs, err := filepath.Abs(d.path); err == nil {
			out[abs] = d.ed.Buf.Bytes()
		}
	}
	return out
}

// projectSearchCmd greps the workspace off the UI thread. Smart-case: an
// all-lowercase query matches case-insensitively. The panel's include/
// exclude globs gate which files are searched.
func (m *Model) projectSearchCmd(query string) tea.Cmd {
	root := m.side.Root
	overrides := m.bufOverrides()
	inc, exc := splitGlobs(m.search.inc), splitGlobs(m.search.exc)
	return func() tea.Msg {
		fold := query == strings.ToLower(query)
		q := []byte(query)
		if fold {
			q = bytes.ToLower(q)
		}
		var hits []psearchHit
		truncated := false
		for _, rel := range listFiles(root) {
			if len(inc) > 0 && !matchGlobs(inc, rel) {
				continue
			}
			if matchGlobs(exc, rel) {
				continue
			}
			abs, _ := filepath.Abs(filepath.Join(root, rel))
			content, ok := overrides[abs]
			if !ok {
				fi, err := os.Stat(abs)
				if err != nil || fi.Size() > maxSearchFile {
					continue
				}
				if content, err = os.ReadFile(abs); err != nil {
					continue
				}
				if isBinary(content) {
					continue
				}
			}
			for i, line := range bytes.Split(content, []byte("\n")) {
				hay := line
				if fold {
					hay = bytes.ToLower(line)
				}
				col := bytes.Index(hay, q)
				if col < 0 {
					continue
				}
				if len(hits) >= maxSearchHits {
					truncated = true
					break
				}
				hits = append(hits, psearchHit{
					ref:  problemRef{path: abs, line: i, col: col},
					text: strings.TrimSpace(string(line)),
				})
			}
			if truncated {
				break
			}
		}
		return psearchMsg{query: query, hits: hits, truncated: truncated}
	}
}

// openProjectSearch fills the search panel with the hits, grouped by file,
// and shows it in the sidebar slot.
func (m Model) openProjectSearch(msg psearchMsg) Model {
	p := &m.search
	p.query, p.hits, p.truncated = msg.query, len(msg.hits), msg.truncated
	p.rows, p.sel, p.top = p.rows[:0], 0, 0
	last := ""
	for _, h := range msg.hits {
		if h.ref.path != last {
			last = h.ref.path
			p.rows = append(p.rows, searchRow{header: rel(m.side.Root, h.ref.path)})
		}
		p.rows = append(p.rows, searchRow{ref: h.ref, text: h.text})
	}
	m.showSearchPanel()
	p.move(+1, m.searchHeight()) // land on the first hit, not its file header
	if msg.truncated {
		m.notify(fmt.Sprintf("showing first %d hits — narrow the search", maxSearchHits))
	} else if len(msg.hits) == 0 {
		m.notify(fmt.Sprintf("no matches for %q", msg.query))
	}
	return m
}

// showSearchPanel swaps the search panel into the sidebar slot and focuses it.
func (m *Model) showSearchPanel() {
	m.search.view, m.git.view, m.sidebarOpen = true, false, true
	m.focus = paneSearch
	m.layout()
}

// searchHeight is the panel's list height; the filter line, when shown,
// takes one row.
func (m *Model) searchHeight() int {
	h := m.gitHeight()
	if m.search.inc != "" || m.search.exc != "" {
		h--
	}
	return max(1, h)
}

// searchOpenSel jumps to the selected hit. A click previews (focus stays on
// the panel, like the git panel); Enter moves into the editor.
func (m *Model) searchOpenSel(focusEditor bool) {
	r, ok := m.search.selected()
	if !ok {
		return
	}
	m.pushJump()
	m.openFile(r.ref.path)
	if d := m.doc(); d != nil && same(d.path, r.ref.path) {
		d.ed.Go(r.ref.line, r.ref.col)
		d.ed.Center()
	}
	m.layout()
	if !focusEditor {
		m.focus = paneSearch
	}
}

// searchClick handles a left click at panel row y (0 = first list row).
func (m *Model) searchClick(y int) {
	i := m.search.top + y
	if i < 0 || i >= len(m.search.rows) || m.search.rows[i].header != "" {
		return
	}
	m.search.sel = i
	m.searchOpenSel(false)
}

// searchFilterPrompt prompts for the include or exclude glob list and
// re-runs the current search under the new filters. The target field is
// selected by flag, not pointer — the Model is copied between the prompt
// opening and its callback firing, so a captured field pointer would write
// into a dead copy.
func (m *Model) searchFilterPrompt(label string, exclude bool) {
	initial := m.search.inc
	if exclude {
		initial = m.search.exc
	}
	*m = m.prompt(label, initial, func(m *Model, text string) {
		if exclude {
			m.search.exc = strings.TrimSpace(text)
		} else {
			m.search.inc = strings.TrimSpace(text)
		}
		m.showSearchPanel()
		if m.search.query != "" {
			m.lastMsg = "searching…"
			m.deferred = m.projectSearchCmd(m.search.query)
		}
	})
}

// searchPanelView renders the panel in the sidebar slot, every row exactly
// side.Width cells.
func (m Model) searchPanelView() string {
	w := m.side.Width
	var sb strings.Builder
	head := " Search"
	if m.search.query != "" {
		head = fmt.Sprintf(" Search: %s (%d)", m.search.query, m.search.hits)
	}
	sb.WriteString(gitHeadStyle.Render(sidebar.Pad(head, w)))
	if m.search.inc != "" || m.search.exc != "" {
		f := ""
		if m.search.inc != "" {
			f = " in:" + m.search.inc
		}
		if m.search.exc != "" {
			f += " not:" + m.search.exc
		}
		sb.WriteByte('\n')
		sb.WriteString(gitSectionStyle.Render(sidebar.Pad(f, w)))
	}
	h := m.searchHeight()
	for i := m.search.top; i < m.search.top+h; i++ {
		sb.WriteByte('\n')
		if i >= len(m.search.rows) {
			if i == 0 {
				hint := " F7 to search · i/x filters"
				if m.search.query != "" {
					hint = " no matches"
				}
				sb.WriteString(sidebar.Pad(hint, w))
			} else {
				sb.WriteString(strings.Repeat(" ", max(0, w)))
			}
			continue
		}
		r := m.search.rows[i]
		if r.header != "" {
			sb.WriteString(gitSectionStyle.Render(sidebar.Pad(" "+r.header, w)))
			continue
		}
		plain := sidebar.Pad(fmt.Sprintf(" %d: %s", r.ref.line+1, r.text), w)
		switch {
		case i == m.search.sel && m.focus == paneSearch:
			sb.WriteString(gitSelStyle.Render(plain))
		case i == m.search.sel:
			sb.WriteString(gitSelStyle.Faint(true).Render(plain))
		default:
			sb.WriteString(plain)
		}
	}
	return sb.String()
}

// ---- replace ----

// replaceProjectPrompt chains find → with → confirm prompts, then applies.
func (m *Model) replaceProjectPrompt() tea.Cmd {
	initial := ""
	if d := m.doc(); d != nil {
		if sel := d.ed.Selection(); len(sel) > 0 && !bytes.Contains(sel, []byte("\n")) {
			initial = string(sel)
		}
	}
	*m = m.prompt("Replace in project — find (exact case):", initial, func(m *Model, query string) {
		if query == "" {
			return
		}
		*m = m.prompt(fmt.Sprintf("Replace %q with:", query), "", func(m *Model, repl string) {
			count, files := m.countProject(query)
			if count == 0 {
				m.notify(fmt.Sprintf("no matches for %q", query))
				return
			}
			*m = m.prompt(fmt.Sprintf("Replace %d occurrence(s) in %d file(s)? y/n:", count, files), "",
				func(m *Model, text string) {
					if !strings.EqualFold(text, "y") {
						return
					}
					count, files := m.applyProjectReplace(query, repl)
					m.notify(fmt.Sprintf("replaced %d occurrence(s) in %d file(s)", count, files))
					m.refreshGit()
				})
		})
	})
	return nil
}

// forEachProjectFile walks the workspace's text files, preferring open-doc
// buffers over disk content. ponytail: synchronous — bounded by the
// finder's 20k-file cap, and replace is a deliberate bulk operation.
func (m *Model) forEachProjectFile(f func(abs string, content []byte, d *doc)) {
	overrides := m.bufOverrides()
	for _, rl := range listFiles(m.side.Root) {
		abs, _ := filepath.Abs(filepath.Join(m.side.Root, rl))
		if content, ok := overrides[abs]; ok {
			f(abs, content, m.docByPath(abs))
			continue
		}
		fi, err := os.Stat(abs)
		if err != nil || fi.Size() > maxSearchFile {
			continue
		}
		content, err := os.ReadFile(abs)
		if err != nil || isBinary(content) {
			continue
		}
		f(abs, content, nil)
	}
}

func (m *Model) countProject(query string) (count, files int) {
	q := []byte(query)
	m.forEachProjectFile(func(abs string, content []byte, d *doc) {
		if n := bytes.Count(content, q); n > 0 {
			count += n
			files++
		}
	})
	return count, files
}

// applyProjectReplace performs the replacement: open docs get one undoable
// transaction each (then save); closed files are rewritten in place.
func (m *Model) applyProjectReplace(query, repl string) (count, files int) {
	q, r := []byte(query), []byte(repl)
	m.forEachProjectFile(func(abs string, content []byte, d *doc) {
		n := bytes.Count(content, q)
		if n == 0 {
			return
		}
		count += n
		files++
		if d != nil {
			var edits []editor.Edit
			for i := 0; ; {
				j := bytes.Index(content[i:], q)
				if j < 0 {
					break
				}
				off := i + j
				edits = append(edits, editor.Edit{Off: off, Old: append([]byte(nil), q...), New: r})
				i = off + len(q)
			}
			d.ed.ApplyEdits(edits)
			if s := d.save(); s != "saved" {
				m.notifyErr(filepath.Base(d.path) + ": " + s)
			}
			m.lspm.Change(d.path, d.ed.Rev, d.ed.Buf.Bytes())
			d.sentRev = d.ed.Rev
			m.updateSigns(d)
			return
		}
		mode := os.FileMode(0o644)
		if fi, err := os.Stat(abs); err == nil {
			mode = fi.Mode()
		}
		if err := os.WriteFile(abs, bytes.ReplaceAll(content, q, r), mode); err != nil {
			m.notifyErr(err.Error())
		}
	})
	return count, files
}
