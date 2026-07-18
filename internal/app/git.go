package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GurYN/cove-editor/internal/buffer"
	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/git"
	"github.com/GurYN/cove-editor/internal/overlay"
	"github.com/GurYN/cove-editor/internal/sidebar"
)

var (
	gitHeadStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	gitSectionStyle  = lipgloss.NewStyle().Bold(true).Faint(true)
	gitSelStyle      = lipgloss.NewStyle().Reverse(true)
	gitAddStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	gitModStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))
	gitDelStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	gitConflictStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("176"))
)

// applyGitChrome themes the git panel from the same color map as the editor.
func applyGitChrome(colors map[string]string) {
	set := func(dst *lipgloss.Style, key string) {
		if c := colors[key]; c != "" {
			*dst = lipgloss.NewStyle().Foreground(lipgloss.Color(c))
		}
	}
	set(&gitAddStyle, "git.added")
	set(&gitModStyle, "git.modified")
	set(&gitDelStyle, "git.deleted")
	set(&gitConflictStyle, "git.conflict")
}

// gitRow is one line of the panel: a section header or a file.
type gitRow struct {
	header string // section title with count; "" = file row
	fs     git.FileStatus
	staged bool // row lives in the Staged section
}

// gitPanel is the git panel's state — the sidebar-slot component showing
// staged/changed files, plus the blame toggle it shares a subsystem with.
type gitPanel struct {
	view    bool // left pane shows the git panel instead of the file tree
	snap    git.Snapshot
	rows    []gitRow
	sel     int
	top     int
	err     string
	busy    string // "push"/"pull" while one is in flight
	blameOn bool   // inline blame for the cursor line (git.blame toggle)
}

// refreshGit re-reads repo status synchronously. ponytail: one exec per
// refresh (~10ms); a background watcher if it ever shows up in lastCost.
func (m *Model) refreshGit() {
	m.git.err = ""
	if m.git.snap.Top == "" {
		top, err := git.Top(m.side.Root)
		if err != nil {
			m.git.rows = nil
			m.git.err = "not a git repository"
			return
		}
		m.git.snap.Top = top
	}
	snap, err := git.Status(m.git.snap.Top)
	if err != nil {
		m.git.err = err.Error()
	}
	headMoved := snap.Oid != m.git.snap.Oid
	m.git.snap = snap
	m.git.build(m.gitHeight())
	m.syncTreeGit()
	if headMoved { // commit/checkout/pull: gutter baselines are stale
		for _, d := range m.docs {
			m.loadGitHead(d)
		}
	}
}

// syncTreeGit mirrors the snapshot's change markers into the file tree.
func (m *Model) syncTreeGit() {
	st := make(map[string]byte, len(m.git.snap.Files))
	for _, f := range m.git.snap.Files {
		abs := filepath.Join(m.git.snap.Top, filepath.FromSlash(f.Path))
		switch {
		case f.Conflict():
			st[abs] = '!'
		case f.Untracked():
			st[abs] = 'A'
		default:
			st[abs] = 'M'
		}
	}
	m.side.SetGitStatus(st)
}

func (m *Model) gitRepo() bool {
	if m.git.snap.Top == "" {
		m.refreshGit()
	}
	if m.git.snap.Top == "" {
		m.lastMsg = "not a git repository"
		return false
	}
	return true
}

// build regenerates the row list from the snapshot; h is the visible height.
func (p *gitPanel) build(h int) {
	p.rows = p.rows[:0]
	var staged, changed []git.FileStatus
	for _, f := range p.snap.Files {
		if f.Staged() {
			staged = append(staged, f)
		}
		if f.Unstaged() || f.Conflict() {
			changed = append(changed, f)
		}
	}
	if len(staged) > 0 {
		p.rows = append(p.rows, gitRow{header: fmt.Sprintf("Staged (%d)", len(staged))})
		for _, f := range staged {
			p.rows = append(p.rows, gitRow{fs: f, staged: true})
		}
	}
	if len(changed) > 0 {
		p.rows = append(p.rows, gitRow{header: fmt.Sprintf("Changes (%d)", len(changed))})
		for _, f := range changed {
			p.rows = append(p.rows, gitRow{fs: f})
		}
	}
	// keep the selection on a file row
	p.sel = clampInt(p.sel, 0, max(0, len(p.rows)-1))
	for p.sel < len(p.rows) && p.rows[p.sel].header != "" {
		p.sel++
	}
	if p.sel >= len(p.rows) {
		for p.sel = len(p.rows) - 1; p.sel > 0 && p.rows[p.sel].header != ""; p.sel-- {
		}
	}
	p.sel = max(0, p.sel)
	p.top = clampInt(p.top, 0, max(0, len(p.rows)-h))
}

// toggleGit shows the git panel in the sidebar slot (Zed's left dock).
// Open but unfocused (e.g. after Enter jumped to a diff tab): reclaim focus;
// only a focused panel closes on toggle.
func (m *Model) toggleGit() {
	if m.git.view && m.sidebarOpen && m.focus == paneGit {
		m.sidebarOpen, m.git.view = false, false
		m.focus = paneEditor
		return
	}
	m.git.view, m.sidebarOpen = true, true
	m.focus = paneGit
	m.refreshGit()
}

func (m *Model) gitHeight() int { return max(1, m.height-5) } // tab bar + header + switcher + spacer + bottom bar

// scroll keeps the selection visible within h rows.
func (p *gitPanel) scroll(h int) {
	if p.sel < p.top {
		p.top = p.sel
	}
	if p.sel >= p.top+h {
		p.top = p.sel - h + 1
	}
}

// move moves the selection to the next file row, skipping headers.
func (p *gitPanel) move(d, h int) {
	for i := p.sel + d; i >= 0 && i < len(p.rows); i += d {
		if p.rows[i].header == "" {
			p.sel = i
			p.scroll(h)
			return
		}
	}
}

func (p *gitPanel) wheel(delta, h int) {
	p.top = clampInt(p.top+delta, 0, max(0, len(p.rows)-h))
}

func (p *gitPanel) selected() (gitRow, bool) {
	if p.sel < len(p.rows) && p.rows[p.sel].header == "" {
		return p.rows[p.sel], true
	}
	return gitRow{}, false
}

// gitStageToggle stages/unstages the selected file (space, or a click on
// the status letter — Zed's checkbox).
// gitRestorePrompt discards the selected file's changes after a y/n
// confirm: tracked files go back to their HEAD content, untracked files are
// deleted. An open tab reloads (as one undoable edit — ctrl+z un-discards).
func (m *Model) gitRestorePrompt() {
	r, ok := m.git.selected()
	if !ok {
		return
	}
	verb := "Discard changes to "
	if r.fs.Untracked() {
		verb = "Delete untracked "
	}
	*m = m.prompt(verb+r.fs.Path+"? y/n:", "", func(m *Model, text string) {
		if !strings.EqualFold(text, "y") {
			return
		}
		abs := filepath.Join(m.git.snap.Top, filepath.FromSlash(r.fs.Path))
		if r.fs.Untracked() {
			if err := os.Remove(abs); err != nil {
				m.lastMsg = err.Error()
			} else {
				m.lastMsg = "deleted " + r.fs.Path
				for i, d := range m.docs {
					if same(d.path, abs) {
						m.active = i
						m.forceClose()
						break
					}
				}
			}
		} else if err := git.Restore(m.git.snap.Top, r.fs.Path); err != nil {
			m.lastMsg = err.Error()
		} else {
			m.lastMsg = "restored " + r.fs.Path
			m.reloadDoc(abs)
			m.deferred = m.syncLSP()
		}
		m.refreshGit()
		m.side.Refresh()
	})
}

// reloadDoc syncs an open tab with the on-disk content after a git restore.
func (m *Model) reloadDoc(abs string) {
	d := m.docByPath(abs)
	if d == nil || d.virtual {
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return
	}
	if d.crlf {
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	}
	if old := d.ed.Buf.Bytes(); !bytes.Equal(old, data) {
		line, col := d.ed.Cursor()
		d.ed.ApplyEdits([]editor.Edit{{Off: 0, Old: old, New: data}})
		d.ed.Go(line, col) // keep the cursor in place instead of jumping to EOF
	}
	d.ed.Dirty = false
	if fi, err := os.Stat(abs); err == nil {
		d.mtime = fi.ModTime()
	}
	m.loadGitHead(d)
}

func (m *Model) gitStageToggle() {
	r, ok := m.git.selected()
	if !ok {
		return
	}
	var err error
	if r.staged {
		err = git.Unstage(m.git.snap.Top, r.fs.Path)
	} else {
		err = git.Stage(m.git.snap.Top, r.fs.Path)
	}
	if err != nil {
		m.lastMsg = err.Error()
	}
	m.refreshGit()
}

// gitClick handles a left press at panel row y / column x (header already
// subtracted): the letter cell toggles staging, the rest opens the diff.
func (m *Model) gitClick(y, x int) {
	i := m.git.top + y
	if i < 0 || i >= len(m.git.rows) || m.git.rows[i].header != "" {
		return
	}
	m.git.sel = i
	if x <= 2 {
		m.gitStageToggle()
		return
	}
	m.gitOpenDiff(m.git.rows[i])
}

// gitOpenDiff shows the file's diff in a read-only tab.
func (m *Model) gitOpenDiff(r gitRow) {
	var text string
	var err error
	if r.fs.Untracked() {
		text = git.DiffUntracked(m.git.snap.Top, r.fs.Path)
	} else {
		text, err = git.Diff(m.git.snap.Top, r.fs.Path, r.staged)
	}
	if err != nil {
		m.lastMsg = err.Error()
		return
	}
	if strings.TrimSpace(text) == "" {
		m.lastMsg = "no changes to show"
		return
	}
	title := r.fs.Path + " (diff)"
	if r.staged {
		title = r.fs.Path + " (staged)"
	}
	m.openVirtual(title, text)
}

// openVirtual shows text in a read-only, diff-highlighted tab; reopening the
// same title replaces the content.
func (m *Model) openVirtual(title, text string) {
	m.openVirtualSyn(title, text, diffSyntax{})
}

func (m *Model) openVirtualSyn(title, text string, syn editor.Syntax) {
	ed := editor.New(buffer.New([]byte(text)))
	ed.ReadOnly = true
	ed.Syntax = syn
	for i, d := range m.docs {
		if d.virtual && d.path == title {
			d.ed = ed
			m.active = i
			m.focus = paneEditor
			m.layout()
			return
		}
	}
	m.docs = append(m.docs, &doc{path: title, virtual: true, ed: ed})
	m.active = len(m.docs) - 1
	m.focus = paneEditor
	m.layout()
}

func (m *Model) gitHasStaged() bool {
	for _, f := range m.git.snap.Files {
		if f.Staged() {
			return true
		}
	}
	return false
}

// gitCommitPrompt asks for a message and commits the staged files.
func (m *Model) gitCommitPrompt() {
	if !m.gitRepo() {
		return
	}
	if !m.gitHasStaged() {
		m.lastMsg = "nothing staged — stage files first (space in the git panel)"
		return
	}
	*m = m.prompt("Commit message:", "", func(m *Model, msg string) {
		if strings.TrimSpace(msg) == "" {
			return
		}
		out, err := git.Commit(m.git.snap.Top, msg)
		if err != nil {
			m.lastMsg = err.Error()
		} else {
			m.lastMsg = firstLine(out)
		}
		m.refreshGit()
	})
}

// gitUndoCommitPrompt un-commits HEAD (soft reset) after a y/n confirm —
// the "committed on the wrong branch" escape hatch: undo, switch, recommit.
func (m *Model) gitUndoCommitPrompt() {
	if !m.gitRepo() {
		return
	}
	head, err := git.HeadSummary(m.git.snap.Top)
	if err != nil {
		m.lastMsg = "nothing to undo — no commits yet"
		return
	}
	*m = m.prompt(fmt.Sprintf("Undo commit %q? Changes stay staged — y/n:", head), "", func(m *Model, text string) {
		if !strings.EqualFold(text, "y") {
			return
		}
		if err := git.UndoCommit(m.git.snap.Top); err != nil {
			m.lastMsg = err.Error()
		} else {
			m.lastMsg = "commit undone — changes are staged"
		}
		m.refreshGit()
	})
}

func (m *Model) gitBranchPrompt() {
	if !m.gitRepo() {
		return
	}
	*m = m.prompt("New branch name:", "", func(m *Model, name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if err := git.CreateBranch(m.git.snap.Top, name); err != nil {
			m.lastMsg = err.Error()
		} else {
			m.lastMsg = "on new branch " + name
		}
		m.refreshGit()
	})
}

// openBranchPicker lists local branches in the fuzzy overlay.
func (m Model) openBranchPicker() Model {
	if !m.gitRepo() {
		return m
	}
	branches, err := git.Branches(m.git.snap.Top)
	if err != nil {
		m.lastMsg = err.Error()
		return m
	}
	if len(branches) == 0 {
		m.lastMsg = "no branches yet — commit first"
		return m
	}
	m.ovKind = overlayBranches
	m.ovBranches = branches
	items := make([]overlay.Item, len(branches))
	for i, b := range branches {
		detail := ""
		if b == m.git.snap.Branch {
			detail = "current"
		}
		items[i] = overlay.Item{Label: b, Detail: detail}
	}
	m.ov = overlay.New("Branch:", items, m.width)
	return m
}

// openHistoryPicker lists recent commits in the fuzzy overlay; Enter opens
// the commit's full diff in a read-only tab.
// ponytail: last 200 commits, one sync exec (~10ms); paging if anyone asks.
func (m Model) openHistoryPicker() Model {
	if !m.gitRepo() {
		return m
	}
	commits, err := git.Log(m.git.snap.Top, 200)
	if err != nil {
		m.lastMsg = err.Error()
		return m
	}
	if len(commits) == 0 {
		m.lastMsg = "no commits yet"
		return m
	}
	m.ovKind = overlayHistory
	m.ovCommits = commits
	items := make([]overlay.Item, len(commits))
	for i, c := range commits {
		items[i] = overlay.Item{
			Label:  c.SHA + " " + c.Subject,
			Detail: c.Author + ", " + age(c.Time),
		}
	}
	m.ov = overlay.New("Commit:", items, m.width)
	return m
}

// ---- commit graph (git draws it; we just show the text) ----

const gitGraphTitle = "Git Graph"

// gitOpenGraph shows `git log --graph --all` in a read-only tab. Enter on a
// line opens that commit's diff (see the dispatchKey hook).
func (m *Model) gitOpenGraph() {
	if !m.gitRepo() {
		return
	}
	text, err := git.LogGraph(m.git.snap.Top, 500)
	if err != nil {
		m.lastMsg = err.Error()
		return
	}
	if strings.TrimSpace(text) == "" {
		m.lastMsg = "no commits yet"
		return
	}
	m.openVirtualSyn(gitGraphTitle, text, logSyntax{})
	m.lastMsg = "enter: open commit"
}

var shaRe = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

// gitOpenCommitAtCursor opens the commit named on the graph line under the
// cursor. Graph-only lines ("|/", "| *") have no sha and are ignored.
func (m *Model) gitOpenCommitAtCursor(d *doc) {
	line, _ := d.ed.Cursor()
	sha := shaRe.FindString(string(d.ed.Buf.Line(line)))
	if sha == "" {
		return
	}
	text, err := git.ShowCommit(m.git.snap.Top, sha)
	if err != nil {
		m.lastMsg = err.Error()
		return
	}
	m.openVirtual(sha+" (commit)", text)
}

// ---- push / pull (async: exec runs in the tea.Cmd goroutine) ----

type gitOpMsg struct {
	op, out string
	err     error
}

func (m *Model) gitOp(op string) tea.Cmd {
	if !m.gitRepo() {
		return nil
	}
	if m.git.busy != "" {
		m.lastMsg = "git " + m.git.busy + " already in progress"
		return nil
	}
	m.git.busy = op
	top := m.git.snap.Top
	return func() tea.Msg {
		var out string
		var err error
		switch op {
		case "push":
			out, err = git.Push(top)
		case "fetch":
			out, err = git.Fetch(top)
		default:
			out, err = git.Pull(top)
		}
		return gitOpMsg{op: op, out: out, err: err}
	}
}

func (m Model) handleGitOp(msg gitOpMsg) (Model, tea.Cmd) {
	m.git.busy = ""
	if msg.err != nil {
		m.lastMsg = msg.err.Error()
	} else {
		// git's raw chatter ("remote:", progress lines) makes a poor status
		// message — say what happened instead.
		low := strings.ToLower(msg.out)
		switch {
		case msg.op == "fetch":
			m.lastMsg = "fetched"
		case strings.Contains(low, "up to date") || strings.Contains(low, "up-to-date"):
			m.lastMsg = "already up to date"
		case strings.Contains(low, "set up to track"):
			m.lastMsg = "published branch " + m.git.snap.Branch + " to origin"
		case msg.op == "push":
			m.lastMsg = "pushed " + m.git.snap.Branch
		default:
			m.lastMsg = "pulled " + m.git.snap.Branch
		}
	}
	m.refreshGit()
	if msg.op == "pull" {
		m.side.Refresh() // pull may create/delete files
	}
	return m, nil
}

// ---- gutter signs ----

// loadGitHead fetches the doc's HEAD baseline and recomputes its signs.
// nil baseline (untracked file, no repo, virtual tab) = no signs.
func (m *Model) loadGitHead(d *doc) {
	d.head = nil
	if m.git.snap.Top != "" && !d.virtual {
		if rel := gitRelPath(m.git.snap.Top, d.path); !strings.HasPrefix(rel, "..") {
			if b, err := git.Show(m.git.snap.Top, rel); err == nil {
				d.head = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
			}
		}
	}
	d.blame = nil // baseline moved: stale, refetched lazily when needed
	m.updateSigns(d)
}

// updateSigns rediffs the buffer against the HEAD baseline. Runs on the
// didChange debounce tick, so it stays off the keystroke→frame path.
func (m *Model) updateSigns(d *doc) {
	if d.head == nil {
		d.ed.Signs, d.lineMap = nil, nil
		return
	}
	d.ed.Signs, d.lineMap = git.Align(d.head, d.ed.Buf.Bytes())
}

// ---- inline blame (current line, in the status bar's message slot) ----

type blameMsg struct {
	d     *doc
	lines []git.BlameLine
}

// blameCmdIfNeeded lazily fetches blame for the active doc — called once
// per Update, so tab switches and toggles need no extra plumbing.
func (m *Model) blameCmdIfNeeded() tea.Cmd {
	d := m.doc()
	if !m.git.blameOn || d == nil || d.virtual || d.blame != nil || d.blameBusy ||
		m.git.snap.Top == "" || d.head == nil {
		return nil
	}
	d.blameBusy = true
	top, path := m.git.snap.Top, gitRelPath(m.git.snap.Top, d.path)
	return func() tea.Msg {
		lines, err := git.Blame(top, path)
		if err != nil {
			lines = []git.BlameLine{} // non-nil sentinel: don't refetch-loop
		}
		return blameMsg{d: d, lines: lines}
	}
}

func (m Model) handleBlame(msg blameMsg) (Model, tea.Cmd) {
	msg.d.blameBusy = false
	msg.d.blame = msg.lines
	return m, nil
}

// blameSeg is the current line's annotation: "author, age · summary".
func (m Model) blameSeg(d *doc) string {
	if !m.git.blameOn || d == nil || d.blame == nil || len(d.blame) == 0 {
		return ""
	}
	line, _ := d.ed.Cursor()
	if line >= len(d.lineMap) {
		return ""
	}
	hl := d.lineMap[line]
	if hl < 0 {
		return "uncommitted changes"
	}
	if hl >= len(d.blame) || d.blame[hl].SHA == "" {
		return ""
	}
	b := d.blame[hl]
	return fmt.Sprintf("%s, %s · %s", b.Author, age(b.Time), b.Summary)
}

func age(unix int64) string {
	dt := time.Since(time.Unix(unix, 0))
	switch {
	case dt < time.Minute:
		return "just now"
	case dt < time.Hour:
		return fmt.Sprintf("%dm ago", int(dt.Minutes()))
	case dt < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(dt.Hours()))
	case dt < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(dt.Hours()/24))
	case dt < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(dt.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(dt.Hours()/(24*365)))
	}
}

// gitRelPath converts an editor path to a repo-top-relative slash path.
func gitRelPath(top, path string) string {
	abs, _ := filepath.Abs(path)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	rel, err := filepath.Rel(top, abs)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// ---- rendering ----

func gitLetter(r gitRow) (string, lipgloss.Style) {
	c := r.fs.Work
	if r.staged {
		c = r.fs.Index
	}
	switch {
	case r.fs.Conflict():
		return "!", gitConflictStyle
	case c == '?':
		return "U", gitAddStyle
	case c == 'A':
		return "A", gitAddStyle
	case c == 'D':
		return "D", gitDelStyle
	case c == 'R', c == 'C':
		return "R", gitModStyle
	default:
		return "M", gitModStyle
	}
}

// gitPanelView renders the panel in the sidebar slot, every row exactly
// side.Width cells.
func (m Model) gitPanelView() string {
	w := m.side.Width
	var sb strings.Builder
	head := " Git: " + m.git.snap.Branch
	if m.git.snap.Branch == "" {
		head = " Git"
	}
	sb.WriteString(gitHeadStyle.Render(sidebar.Pad(head, w)))
	h := m.gitHeight()
	for i := m.git.top; i < m.git.top+h; i++ {
		sb.WriteByte('\n')
		if i >= len(m.git.rows) {
			if i == 0 {
				msg := " no changes"
				if m.git.err != "" {
					msg = " " + m.git.err
				}
				sb.WriteString(sidebar.Pad(msg, w))
			} else {
				sb.WriteString(strings.Repeat(" ", max(0, w)))
			}
			continue
		}
		r := m.git.rows[i]
		if r.header != "" {
			sb.WriteString(gitSectionStyle.Render(sidebar.Pad(" "+r.header, w)))
			continue
		}
		letter, st := gitLetter(r)
		plain := sidebar.Pad(" "+letter+" "+r.fs.Path, w)
		switch {
		case i == m.git.sel && m.focus == paneGit:
			sb.WriteString(gitSelStyle.Render(plain))
		case i == m.git.sel:
			sb.WriteString(gitSelStyle.Faint(true).Render(plain))
		default:
			sb.WriteString(" ")
			sb.WriteString(st.Render(letter))
			sb.WriteString(sidebar.Pad(" "+r.fs.Path, w-2))
		}
	}
	return sb.String()
}

// gitSeg is the status-bar segment: branch, ahead/behind, change count.
func (m Model) gitSeg() string {
	if m.git.snap.Branch == "" {
		return ""
	}
	s := "⎇ " + m.git.snap.Branch
	if m.git.snap.Ahead > 0 {
		s += fmt.Sprintf(" ↑%d", m.git.snap.Ahead)
	}
	if m.git.snap.Behind > 0 {
		s += fmt.Sprintf(" ↓%d", m.git.snap.Behind)
	}
	if n := len(m.git.snap.Files); n > 0 {
		s += fmt.Sprintf(" ±%d", n)
	}
	if m.git.busy != "" {
		s = m.git.busy + "… " + s
	}
	return s + "  "
}

// forEachLine walks the lines of src intersecting [startOff, endOff) — the
// shared scaffold of the static highlighters below.
func forEachLine(src []byte, startOff, endOff int, f func(lo, hi int, line []byte)) {
	lo := bytes.LastIndexByte(src[:min(startOff, len(src))], '\n') + 1
	for lo < endOff && lo < len(src) {
		hi := bytes.IndexByte(src[lo:], '\n')
		if hi < 0 {
			hi = len(src)
		} else {
			hi += lo + 1
		}
		f(lo, hi, src[lo:hi])
		lo = hi
	}
}

// ---- log-graph highlighting: sha and (refs) per line ----

type logSyntax struct{}

func (logSyntax) Edit(int, int, int, [2]int, [2]int, [2]int)    {}
func (logSyntax) Expand([]byte, int, int) (lo, hi int, ok bool) { return 0, 0, false }

var logDecoRe = regexp.MustCompile(`\([^)]*\)`)

func (logSyntax) Spans(src []byte, startOff, endOff int) []editor.HLSpan {
	var spans []editor.HLSpan
	forEachLine(src, startOff, endOff, func(lo, hi int, line []byte) {
		if m := shaRe.FindIndex(line); m != nil {
			spans = append(spans, editor.HLSpan{Start: lo + m[0], End: lo + m[1], Class: editor.ClassFunction})
		}
		if m := logDecoRe.FindIndex(line); m != nil {
			spans = append(spans, editor.HLSpan{Start: lo + m[0], End: lo + m[1], Class: editor.ClassKeyword})
		}
	})
	return spans
}

// ---- diff highlighting (a static editor.Syntax over unified diff text) ----

type diffSyntax struct{}

func (diffSyntax) Edit(int, int, int, [2]int, [2]int, [2]int)    {}
func (diffSyntax) Expand([]byte, int, int) (lo, hi int, ok bool) { return 0, 0, false }

func (diffSyntax) Spans(src []byte, startOff, endOff int) []editor.HLSpan {
	var spans []editor.HLSpan
	forEachLine(src, startOff, endOff, func(lo, hi int, line []byte) {
		class := editor.ClassNone
		switch {
		case bytes.HasPrefix(line, []byte("+++")), bytes.HasPrefix(line, []byte("---")),
			bytes.HasPrefix(line, []byte("diff ")), bytes.HasPrefix(line, []byte("index ")),
			bytes.HasPrefix(line, []byte("new file")), bytes.HasPrefix(line, []byte("deleted file")):
			class = editor.ClassComment
		case bytes.HasPrefix(line, []byte("@@")):
			class = editor.ClassFunction
		case bytes.HasPrefix(line, []byte("+")):
			class = editor.ClassDiffAdd
		case bytes.HasPrefix(line, []byte("-")):
			class = editor.ClassDiffDel
		}
		if class != editor.ClassNone {
			spans = append(spans, editor.HLSpan{Start: lo, End: hi, Class: class})
		}
	})
	return spans
}
