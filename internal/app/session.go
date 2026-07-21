package app

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"

	"github.com/GurYN/cove-editor/internal/config"
)

// Session persistence: on quit the open tabs, cursors, split, and sidebar
// width are written to a per-workspace JSON file; launching Cove on the
// same directory (or with no argument) restores them. Opening an explicit
// file skips restore — `cove main.go` should mean exactly that.

type sessDoc struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

type sessionData struct {
	Files      []sessDoc `json:"files"`
	Active     int       `json:"active"`
	Split      bool      `json:"split,omitempty"`
	SplitRight bool      `json:"split_right,omitempty"`
	Other      int       `json:"other,omitempty"`
	SplitW     int       `json:"split_w,omitempty"`
	SidebarW   int       `json:"sidebar_w,omitempty"`
}

// sessionPath keys sessions by workspace root, stored next to config.toml
// so COVE_CONFIG keeps tests (and alternate profiles) isolated.
func sessionPath(root string) string {
	abs, _ := filepath.Abs(root)
	h := fnv.New64a()
	h.Write([]byte(abs))
	return filepath.Join(filepath.Dir(config.Path()), "sessions", fmt.Sprintf("%x.json", h.Sum64()))
}

// saveSession is best-effort: a failed write must never block quitting.
func (m *Model) saveSession() {
	var s sessionData
	idx := map[int]int{} // doc index → Files index (virtual tabs drop out)
	for i, d := range m.docs {
		if d.virtual {
			continue
		}
		line, col := d.ed.Cursor()
		abs, _ := filepath.Abs(d.path)
		idx[i] = len(s.Files)
		s.Files = append(s.Files, sessDoc{Path: abs, Line: line, Col: col})
	}
	p := sessionPath(m.side.Root)
	if len(s.Files) == 0 {
		os.Remove(p)
		return
	}
	if a, ok := idx[m.active]; ok {
		s.Active = a
	}
	if o, ok := idx[m.other]; m.split && ok && o != s.Active {
		s.Split, s.SplitRight, s.Other, s.SplitW = true, m.splitRight, o, m.splitW
	}
	s.SidebarW = m.sidebarW
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, data, 0o644)
}

func (m *Model) restoreSession() {
	data, err := os.ReadFile(sessionPath(m.side.Root))
	if err != nil {
		return
	}
	var s sessionData
	if json.Unmarshal(data, &s) != nil {
		return
	}
	for _, f := range s.Files {
		if _, err := os.Stat(f.Path); err != nil {
			continue // deleted since last session; a phantom empty tab helps no one
		}
		before := len(m.docs)
		m.openFile(f.Path)
		if len(m.docs) == before+1 { // actually opened (not a dupe)
			d := m.docs[len(m.docs)-1]
			d.ed.Go(f.Line, f.Col)
		}
	}
	m.lastMsg = "" // suppress transient open noise from restore
	if s.Active >= 0 && s.Active < len(m.docs) {
		m.active = s.Active
	}
	if s.Split && s.Other >= 0 && s.Other < len(m.docs) && s.Other != m.active {
		m.split, m.splitRight, m.other, m.splitW = true, s.SplitRight, s.Other, s.SplitW
	}
	if s.SidebarW >= 12 {
		m.sidebarW = s.SidebarW
	}
	if len(m.docs) > 0 {
		m.focus = paneEditor
	}
}
