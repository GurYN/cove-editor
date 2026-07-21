package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/overlay"
)

// Project-wide search and replace. Search walks the workspace (same file
// list as the fuzzy finder: .gitignore honored, 20k-file cap) and lists
// hits in the picker overlay — type to filter, Enter to jump. Replace is
// exact-case and literal, applied undoably to open docs and directly on
// disk for the rest.

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
// all-lowercase query matches case-insensitively.
func (m *Model) projectSearchCmd(query string) tea.Cmd {
	root := m.side.Root
	overrides := m.bufOverrides()
	return func() tea.Msg {
		fold := query == strings.ToLower(query)
		q := []byte(query)
		if fold {
			q = bytes.ToLower(q)
		}
		var hits []psearchHit
		truncated := false
		for _, rel := range listFiles(root) {
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

// openProjectSearch lists the hits; rows reuse the Problems overlay
// plumbing: pick → jump (recording a jump-list entry).
func (m Model) openProjectSearch(msg psearchMsg) Model {
	if len(msg.hits) == 0 {
		m.lastMsg = fmt.Sprintf("no matches for %q", msg.query)
		return m
	}
	m.ovKind = overlayDiags
	m.ovDiags = m.ovDiags[:0]
	items := make([]overlay.Item, len(msg.hits))
	for i, h := range msg.hits {
		m.ovDiags = append(m.ovDiags, h.ref)
		items[i] = overlay.Item{
			Label:  h.text,
			Detail: fmt.Sprintf("%s:%d", rel(m.side.Root, h.ref.path), h.ref.line+1),
		}
	}
	m.ov = overlay.New(fmt.Sprintf("%d hits:", len(msg.hits)), items, m.width)
	if msg.truncated {
		m.lastMsg = fmt.Sprintf("showing first %d hits — narrow the search", maxSearchHits)
	}
	return m
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
				m.lastMsg = fmt.Sprintf("no matches for %q", query)
				return
			}
			*m = m.prompt(fmt.Sprintf("Replace %d occurrence(s) in %d file(s)? y/n:", count, files), "",
				func(m *Model, text string) {
					if !strings.EqualFold(text, "y") {
						return
					}
					count, files := m.applyProjectReplace(query, repl)
					m.lastMsg = fmt.Sprintf("replaced %d occurrence(s) in %d file(s)", count, files)
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
				m.lastMsg = filepath.Base(d.path) + ": " + s
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
			m.lastMsg = err.Error()
		}
	})
	return count, files
}
