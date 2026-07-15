// Package sidebar is the file-tree pane: lazy-loaded directories,
// keyboard and mouse navigation, create/rename/delete.
package sidebar

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	dirStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("74")).Bold(true)
	selStyle  = lipgloss.NewStyle().Reverse(true)
	headStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	// git status tints — same defaults as the git panel.
	addStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	modStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))
	conStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("176"))
)

// ApplyTheme recolors the git tints from the same color map as the editor.
func ApplyTheme(colors map[string]string) {
	set := func(dst *lipgloss.Style, key string) {
		if c := colors[key]; c != "" {
			*dst = lipgloss.NewStyle().Foreground(lipgloss.Color(c))
		}
	}
	set(&addStyle, "git.added")
	set(&modStyle, "git.modified")
	set(&conStyle, "git.conflict")
}

type node struct {
	name     string
	path     string
	isDir    bool
	expanded bool
	children []*node // nil = not loaded yet
}

type Model struct {
	Root   string
	Width  int
	Height int
	root   *node
	rows   []*node // flattened visible nodes
	depths []int
	sel    int
	top    int
	err    string
	gitFiles map[string]byte // abs path -> 'A' new, 'M' modified, '!' conflict
	gitDirs  map[string]bool // dirs containing a changed file
}

// SetGitStatus installs the change markers shown in the tree: abs file path
// -> 'A' (new/untracked), 'M' (modified), '!' (conflict). Ancestor dirs of
// each changed file get a dot marker.
func (m *Model) SetGitStatus(files map[string]byte) {
	m.gitFiles = files
	m.gitDirs = make(map[string]bool, len(files))
	for p := range files {
		for d := filepath.Dir(p); strings.HasPrefix(d, m.Root); d = filepath.Dir(d) {
			if m.gitDirs[d] || d == filepath.Dir(d) {
				break
			}
			m.gitDirs[d] = true
		}
	}
}

func New(root string) Model {
	abs, _ := filepath.Abs(root)
	m := Model{Root: abs, root: &node{name: filepath.Base(abs), path: abs, isDir: true, expanded: true}}
	m.load(m.root)
	m.flatten()
	return m
}

func (n *node) load() {
	entries, err := os.ReadDir(n.path)
	if err != nil {
		n.children = []*node{}
		return
	}
	n.children = make([]*node, 0, len(entries))
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		n.children = append(n.children, &node{
			name:  e.Name(),
			path:  filepath.Join(n.path, e.Name()),
			isDir: e.IsDir(),
		})
	}
	sort.Slice(n.children, func(i, j int) bool {
		a, b := n.children[i], n.children[j]
		if a.isDir != b.isDir {
			return a.isDir // directories first
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
}

func (m *Model) load(n *node) {
	if n.children == nil {
		n.load()
	}
}

func (m *Model) flatten() {
	m.rows = m.rows[:0]
	m.depths = m.depths[:0]
	var walk func(n *node, depth int)
	walk = func(n *node, depth int) {
		for _, c := range n.children {
			m.rows = append(m.rows, c)
			m.depths = append(m.depths, depth)
			if c.isDir && c.expanded {
				m.load(c)
				walk(c, depth+1)
			}
		}
	}
	walk(m.root, 0)
	m.sel = clamp(m.sel, 0, max(0, len(m.rows)-1))
}

// Refresh re-reads every expanded directory (after create/rename/delete or
// external changes).
func (m *Model) Refresh() {
	var walk func(n *node)
	walk = func(n *node) {
		if !n.isDir || n.children == nil {
			return
		}
		old := make(map[string]*node, len(n.children))
		for _, c := range n.children {
			old[c.path] = c
		}
		n.load()
		for _, c := range n.children {
			// Graft the old subtree back so expansion survives at every
			// depth, then re-read the expanded part of it.
			if o := old[c.path]; o != nil && o.isDir == c.isDir {
				c.expanded = o.expanded
				c.children = o.children
				if c.expanded {
					walk(c)
				}
			}
		}
	}
	walk(m.root)
	m.flatten()
}

// Selected returns the selected node's path and whether it is a directory.
// ok is false when the tree is empty.
func (m *Model) Selected() (path string, isDir, ok bool) {
	if len(m.rows) == 0 {
		return "", false, false
	}
	n := m.rows[m.sel]
	return n.path, n.isDir, true
}

// SelectedDir returns the directory the selection lives in — the node
// itself if a directory, else its parent. Falls back to the root.
func (m *Model) SelectedDir() string {
	path, isDir, ok := m.Selected()
	if !ok {
		return m.Root
	}
	if isDir {
		return path
	}
	return filepath.Dir(path)
}

func (m *Model) Move(delta int) {
	m.sel = clamp(m.sel+delta, 0, max(0, len(m.rows)-1))
	m.scroll()
}

// Toggle expands/collapses the selected directory. Returns the file path
// to open when the selection is a file.
func (m *Model) Toggle() (openFile string) {
	if len(m.rows) == 0 {
		return ""
	}
	n := m.rows[m.sel]
	if !n.isDir {
		return n.path
	}
	n.expanded = !n.expanded
	if n.expanded {
		m.load(n)
	}
	m.flatten()
	return ""
}

// Collapse collapses the selection (or jumps to top of subtree).
func (m *Model) Collapse() {
	if len(m.rows) == 0 {
		return
	}
	if n := m.rows[m.sel]; n.isDir && n.expanded {
		n.expanded = false
		m.flatten()
	}
}

func (m *Model) Expand() {
	if len(m.rows) == 0 {
		return
	}
	if n := m.rows[m.sel]; n.isDir && !n.expanded {
		n.expanded = true
		m.load(n)
		m.flatten()
	}
}

// Click selects the row at tree line y (header already subtracted by the
// caller). A file click opens it; a directory click toggles it.
func (m *Model) Click(y int) (openFile string) {
	i := m.top + y
	if i < 0 || i >= len(m.rows) {
		return ""
	}
	m.sel = i
	return m.Toggle()
}

func (m *Model) Wheel(delta int) {
	m.top = clamp(m.top+delta, 0, max(0, len(m.rows)-m.treeHeight()))
}

func (m *Model) scroll() {
	h := m.treeHeight()
	if m.sel < m.top {
		m.top = m.sel
	}
	if m.sel >= m.top+h {
		m.top = m.sel - h + 1
	}
}

func (m *Model) treeHeight() int { return max(1, m.Height-1) } // minus header

func (m *Model) SetError(s string) { m.err = s }

func (m Model) View(focused bool) string {
	var sb strings.Builder
	head := " " + m.root.name
	if m.err != "" {
		head = " " + m.err
	}
	sb.WriteString(headStyle.Render(pad(head, m.Width)))
	h := m.treeHeight()
	for i := m.top; i < m.top+h; i++ {
		sb.WriteByte('\n')
		if i >= len(m.rows) {
			sb.WriteString(strings.Repeat(" ", m.Width))
			continue
		}
		n := m.rows[i]
		icon := "  "
		if n.isDir {
			icon = "▸ "
			if n.expanded {
				icon = "▾ "
			}
		}
		row := strings.Repeat("  ", m.depths[i]) + icon + n.name
		st := m.gitFiles[n.path]
		if n.isDir && m.gitDirs[n.path] {
			row += " •"
		}
		row = pad(" "+row, m.Width)
		switch {
		case i == m.sel && focused:
			row = selStyle.Render(row)
		case i == m.sel:
			row = selStyle.Faint(true).Render(row)
		case st == '!':
			row = conStyle.Render(row)
		case st == 'A':
			row = addStyle.Render(row)
		case st == 'M':
			row = modStyle.Render(row)
		case n.isDir:
			row = dirStyle.Render(row)
		}
		sb.WriteString(row)
	}
	return sb.String()
}

// pad clips or pads s to exactly w cells so rows never bleed into the
// editor pane. ponytail: rune==cell assumption, same as the renderer.
func pad(s string, w int) string {
	r := []rune(s)
	if len(r) >= w {
		return string(r[:w])
	}
	return s + strings.Repeat(" ", w-len(r))
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }
