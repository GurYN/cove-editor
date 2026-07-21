package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

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

// repoState is one discovered repository: identity plus its latest status
// snapshot. Pointers are stable across refreshes — rows, virtual docs and
// pickers hold them.
type repoState struct {
	top  string // absolute repo top-level dir
	name string // display name: base(top)
	snap git.Snapshot
	err  string
}

// gitRow is one line of the panel: a repo header, a section header, or a file.
type gitRow struct {
	repo     *repoState
	header   string // header text; "" = file row
	repoHead bool   // header is a repo title (multi-repo panel only)
	fs       git.FileStatus
	staged   bool // row lives in the Staged section
}

// gitPanel is the git panel's state — the sidebar-slot component showing
// staged/changed files, plus the blame toggle it shares a subsystem with.
// Several repos happen when the opened folder holds multiple checkouts
// (root docs + per-project repos): each renders as its own section group.
type gitPanel struct {
	view    bool // left pane shows the git panel instead of the file tree
	repos   []*repoState
	dirTop  map[string]string // file-dir → repo top cache ("" = not in a repo)
	rows    []gitRow
	sel     int
	top     int
	err     string // "not a git repository" when nothing was found
	busy    string // "push"/"pull" while one is in flight
	blameOn bool   // inline blame for the cursor line (git.blame toggle)
}

func (p *gitPanel) multi() bool { return len(p.repos) > 1 }

func (p *gitPanel) byTop(top string) *repoState {
	if top == "" {
		return nil
	}
	for _, r := range p.repos {
		if r.top == top {
			return r
		}
	}
	return nil
}

// walkTop finds the repo top containing dir by statting .git upward — no
// exec, cheap enough for a render-path cache miss. "" = not in a repo.
// Symlinks are resolved so the result compares equal to git's own
// --show-toplevel (macOS: /var/… vs /private/var/…).
func walkTop(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// discoverRepos finds the repos the panel manages: the opened folder's own
// (or enclosing) repo plus depth-1 children that are repo tops. Existing
// repoState pointers are reused so held references stay valid.
// ponytail: depth-1 scan; deeper repos join lazily when a file of theirs opens.
func (m *Model) discoverRepos() {
	prev := m.git.repos
	var repos []*repoState
	seen := map[string]bool{}
	add := func(top string) {
		if r, err := filepath.EvalSymlinks(top); err == nil {
			top = r // match git.Top's physical paths (macOS symlinked /tmp)
		}
		if top == "" || seen[top] {
			return
		}
		seen[top] = true
		r := m.git.byTop(top)
		if r == nil {
			r = &repoState{top: top, name: filepath.Base(top)}
		}
		repos = append(repos, r)
	}
	root, _ := filepath.Abs(m.side.Root)
	if top, err := git.Top(root); err == nil {
		add(top)
	}
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		if e.IsDir() && e.Name() != ".git" {
			child := filepath.Join(root, e.Name())
			if _, err := os.Stat(filepath.Join(child, ".git")); err == nil {
				add(child)
			}
		}
	}
	for _, r := range prev { // lazily-joined repos outside the scan survive
		add(r.top)
	}
	m.git.repos = repos
}

// repoOf resolves which known repo contains path. Read-only (View-safe):
// unknown repos are not joined — see repoForDoc.
func (m Model) repoOf(path string) *repoState {
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(abs)
	top, ok := m.git.dirTop[dir]
	if !ok {
		top = walkTop(dir)
		if m.git.dirTop != nil {
			m.git.dirTop[dir] = top
		}
	}
	return m.git.byTop(top)
}

// repoForDoc is repoOf plus lazy join: a file opened from a repo the depth-1
// scan didn't see adds that repo to the panel.
func (m *Model) repoForDoc(path string) *repoState {
	if r := m.repoOf(path); r != nil {
		return r
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	top := walkTop(filepath.Dir(abs))
	if top == "" {
		return nil
	}
	snap, err := git.Status(top)
	if err != nil {
		return nil // stray .git that isn't a working repo
	}
	r := &repoState{top: top, name: filepath.Base(top), snap: snap}
	m.git.repos = append(m.git.repos, r)
	m.git.err = ""
	m.git.build(m.gitHeight())
	m.syncTreeGit()
	return r
}

// curRepo is the one target-resolution rule: panel focused → repo of the row
// under the cursor; else the active tab's repo (virtual git tabs remember
// theirs); else the only repo. Nil = ambiguous — withRepo asks via a picker.
func (m Model) curRepo() *repoState {
	if m.focus == paneGit && m.git.sel < len(m.git.rows) {
		if r := m.git.rows[m.git.sel].repo; r != nil {
			return r
		}
	}
	if d := m.doc(); d != nil {
		if d.repo != nil {
			return d.repo
		}
		if !d.virtual {
			if r := m.repoOf(d.path); r != nil {
				return r
			}
		}
	}
	if len(m.git.repos) == 1 {
		return m.git.repos[0]
	}
	return nil
}

// withRepo runs do against the resolved target repo, or pops a one-shot
// repo picker (same overlay as the branch picker) when the target is
// genuinely ambiguous: several repos and no cursor/file anchor.
func (m *Model) withRepo(do func(*Model, *repoState) tea.Cmd) tea.Cmd {
	if !m.gitRepo() {
		return nil
	}
	if r := m.curRepo(); r != nil {
		return do(m, r)
	}
	m.ovKind = overlayRepos
	m.ovRepoDo = do
	items := make([]overlay.Item, len(m.git.repos))
	for i, r := range m.git.repos {
		items[i] = overlay.Item{Label: r.name, Detail: r.snap.Branch}
	}
	m.ov = overlay.New("Repository:", items, m.width)
	return nil
}

// repoMsg prefixes a status message with the repo name when several repos
// are in play, so every toast names its target.
func (m *Model) repoMsg(r *repoState, s string) string {
	if m.git.multi() && r != nil {
		return r.name + ": " + s
	}
	return s
}

// refreshGit re-discovers repos and re-reads their status synchronously.
// ponytail: one status exec per repo per refresh (~10ms each); a background
// watcher if it ever shows up in lastCost.
func (m *Model) refreshGit() {
	m.git.err = ""
	m.git.dirTop = map[string]string{} // re-resolve: a fresh `git init` must be seen
	m.discoverRepos()
	if len(m.git.repos) == 0 {
		m.git.rows = nil
		m.git.err = "not a git repository"
		return
	}
	headMoved := false
	for _, r := range m.git.repos {
		snap, err := git.Status(r.top)
		r.err = ""
		if err != nil {
			r.err = err.Error()
		}
		if snap.Oid != r.snap.Oid {
			headMoved = true
		}
		r.snap = snap
	}
	m.git.build(m.gitHeight())
	m.syncTreeGit()
	if headMoved { // commit/checkout/pull: gutter baselines are stale
		for _, d := range m.docs {
			m.loadGitHead(d)
		}
	}
}

// syncTreeGit mirrors every repo's change markers into the file tree.
func (m *Model) syncTreeGit() {
	st := map[string]byte{}
	for _, r := range m.git.repos {
		for _, f := range r.snap.Files {
			abs := filepath.Join(r.top, filepath.FromSlash(f.Path))
			switch {
			case f.Conflict():
				st[abs] = '!'
			case f.Untracked():
				st[abs] = 'A'
			default:
				st[abs] = 'M'
			}
		}
	}
	m.side.SetGitStatus(st)
}

func (m *Model) gitRepo() bool {
	if len(m.git.repos) == 0 {
		m.refreshGit()
	}
	if len(m.git.repos) == 0 {
		m.lastMsg = "not a git repository"
		return false
	}
	return true
}

// build regenerates the row list from the repo snapshots; h is the visible
// height. Single repo renders exactly like the classic panel; several repos
// get a bold name·branch header above each one's sections.
func (p *gitPanel) build(h int) {
	p.rows = p.rows[:0]
	for _, r := range p.repos {
		var conflicts, staged, changed []git.FileStatus
		for _, f := range r.snap.Files {
			if f.Conflict() { // own section — mid-merge these must not drown in Changes
				conflicts = append(conflicts, f)
				continue
			}
			if f.Staged() {
				staged = append(staged, f)
			}
			if f.Unstaged() {
				changed = append(changed, f)
			}
		}
		if p.multi() {
			head := r.name
			if r.snap.Branch != "" {
				head += " · " + r.snap.Branch
			}
			if n := len(r.snap.Files); n > 0 {
				head += fmt.Sprintf(" ±%d", n)
			}
			p.rows = append(p.rows, gitRow{repo: r, header: head, repoHead: true})
			if r.err != "" {
				p.rows = append(p.rows, gitRow{repo: r, header: "  " + r.err})
			}
		}
		if r.snap.Merging && len(conflicts) == 0 {
			// All conflicts resolved (possibly to exactly HEAD, so no file
			// rows either) — without this banner the panel reads "no changes"
			// while pull/push stay blocked on the unconcluded merge.
			p.rows = append(p.rows, gitRow{repo: r, header: "Merging — press c to commit & conclude"})
		}
		if len(conflicts) > 0 {
			p.rows = append(p.rows, gitRow{repo: r, header: fmt.Sprintf("Conflicts (%d) — o: ours · t: theirs", len(conflicts))})
			for _, f := range conflicts {
				p.rows = append(p.rows, gitRow{repo: r, fs: f})
			}
		}
		if len(staged) > 0 {
			p.rows = append(p.rows, gitRow{repo: r, header: fmt.Sprintf("Staged (%d)", len(staged))})
			for _, f := range staged {
				p.rows = append(p.rows, gitRow{repo: r, fs: f, staged: true})
			}
		}
		if len(changed) > 0 {
			p.rows = append(p.rows, gitRow{repo: r, header: fmt.Sprintf("Changes (%d)", len(changed))})
			for _, f := range changed {
				p.rows = append(p.rows, gitRow{repo: r, fs: f})
			}
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
		abs := filepath.Join(r.repo.top, filepath.FromSlash(r.fs.Path))
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
		} else if err := git.Restore(r.repo.top, r.fs.Path); err != nil {
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
		err = git.Unstage(r.repo.top, r.fs.Path)
	} else {
		err = git.Stage(r.repo.top, r.fs.Path)
	}
	if err != nil {
		m.lastMsg = err.Error()
	}
	m.refreshGit()
}

// gitResolveSide settles the selected conflicted file by taking one side
// wholesale ("ours" = current branch, "theirs" = the merged branch), stages
// it, and reloads any open tab. Hand-editing the markers then pressing
// space (stage) is the other resolution path.
// ponytail: file-level ours/theirs only; a 3-way hunk picker is v2.
func (m *Model) gitResolveSide(theirs bool) {
	r, ok := m.git.selected()
	if !ok || !r.fs.Conflict() {
		m.lastMsg = "select a conflicted file first"
		return
	}
	side, do := "ours", git.ResolveOurs
	if theirs {
		side, do = "theirs", git.ResolveTheirs
	}
	if err := do(r.repo.top, r.fs.Path); err != nil {
		m.lastMsg = err.Error()
	} else {
		m.lastMsg = m.repoMsg(r.repo, "kept "+side+": "+r.fs.Path)
		m.reloadDoc(filepath.Join(r.repo.top, filepath.FromSlash(r.fs.Path)))
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
	if r.fs.Conflict() { // markers live in the working file: open it to resolve
		m.openFile(filepath.Join(r.repo.top, filepath.FromSlash(r.fs.Path)))
		m.mergeNext() // land on the first block; the hint names the accept commands
		return
	}
	var text string
	var err error
	if r.fs.Untracked() {
		text = git.DiffUntracked(r.repo.top, r.fs.Path)
	} else {
		text, err = git.Diff(r.repo.top, r.fs.Path, r.staged)
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

func gitHasStaged(r *repoState) bool {
	for _, f := range r.snap.Files {
		if f.Staged() {
			return true
		}
	}
	return false
}

// gitCommitPrompt asks for a message and commits the target repo's staged
// files. With several repos the prompt names its target — a wrong-repo
// commit should be visibly wrong before the message is typed.
func (m *Model) gitCommitPrompt() tea.Cmd {
	return m.withRepo(func(m *Model, r *repoState) tea.Cmd {
		// Mid-merge, committing is allowed with nothing staged: a resolution
		// identical to HEAD leaves no changes, yet the merge needs the commit.
		if !gitHasStaged(r) && !r.snap.Merging {
			m.lastMsg = m.repoMsg(r, "nothing staged — stage files first (space in the git panel)")
			return nil
		}
		label := "Commit message:"
		initial := ""
		if m.git.multi() {
			label = fmt.Sprintf("Commit to %s (%s):", r.name, r.snap.Branch)
		}
		if r.snap.Merging {
			label = "Conclude merge — commit message:"
			initial = git.MergeMsg(r.top)
		}
		*m = m.prompt(label, initial, func(m *Model, msg string) {
			if strings.TrimSpace(msg) == "" {
				return
			}
			out, err := git.Commit(r.top, msg)
			if err != nil {
				m.lastMsg = err.Error()
			} else {
				m.lastMsg = m.repoMsg(r, firstLine(out))
			}
			m.refreshGit()
		})
		return nil
	})
}

// gitUndoCommitPrompt un-commits HEAD (soft reset) after a y/n confirm —
// the "committed on the wrong branch" escape hatch: undo, switch, recommit.
func (m *Model) gitUndoCommitPrompt() tea.Cmd {
	return m.withRepo(func(m *Model, r *repoState) tea.Cmd {
		head, err := git.HeadSummary(r.top)
		if err != nil {
			m.lastMsg = m.repoMsg(r, "nothing to undo — no commits yet")
			return nil
		}
		*m = m.prompt(fmt.Sprintf("Undo commit %q? Changes stay staged — y/n:", head), "", func(m *Model, text string) {
			if !strings.EqualFold(text, "y") {
				return
			}
			if err := git.UndoCommit(r.top); err != nil {
				m.lastMsg = err.Error()
			} else {
				m.lastMsg = m.repoMsg(r, "commit undone — changes are staged")
			}
			m.refreshGit()
		})
		return nil
	})
}

// gitFetchThen fetches in the background so remote-tracking refs are
// current, then runs do on the UI thread — the branch picker and the
// created-name-exists-on-remote check see the remote's real state, not the
// last fetch's. A failed fetch (offline) still runs do: local knowledge
// beats a dead picker. The busy flag doubles as the status-bar indicator.
type gitFetchDoneMsg struct{ do func(*Model) }

func (m *Model) gitFetchThen(r *repoState, do func(*Model)) tea.Cmd {
	if m.git.busy != "" { // another op in flight: act on what we have
		do(m)
		return nil
	}
	m.git.busy = "fetch"
	top := r.top
	return func() tea.Msg {
		git.Fetch(top)
		return gitFetchDoneMsg{do: do}
	}
}

func (m *Model) gitBranchPrompt() tea.Cmd {
	return m.withRepo(func(m *Model, r *repoState) tea.Cmd {
		*m = m.prompt("New branch name:", "", func(m *Model, name string) {
			if name = strings.TrimSpace(name); name != "" {
				m.deferred = m.gitFetchThen(r, func(m *Model) { m.gitCreateBranch(r, name) })
			}
		})
		return nil
	})
}

// gitCreateBranch makes name the current branch. A same-named branch on a
// remote is almost certainly the intent — check it out tracking the remote
// instead of forking an unrelated branch off HEAD (which would collide on
// the first push). Only remotes already fetched are visible here.
func (m *Model) gitCreateBranch(r *repoState, name string) {
	bs, _ := git.Branches(r.top)
	for _, b := range bs {
		if _, tail, _ := strings.Cut(b.Name, "/"); b.Remote && tail == name {
			if local, err := git.CheckoutRemote(r.top, b.Name); err != nil {
				m.lastMsg = err.Error()
			} else {
				m.lastMsg = m.repoMsg(r, "branch existed on remote — switched to "+local+" (tracking "+b.Name+")")
			}
			m.refreshGit()
			m.side.Refresh() // checkout swaps working-tree files
			return
		}
	}
	if err := git.CreateBranch(r.top, name); err != nil {
		m.lastMsg = err.Error()
	} else {
		m.lastMsg = m.repoMsg(r, "on new branch "+name)
	}
	m.refreshGit()
}

// openBranchPicker lists the target repo's local branches in the fuzzy
// overlay; ovRepo remembers which repo the checkout applies to.
func (m *Model) openBranchPicker(r *repoState) {
	branches, err := git.Branches(r.top)
	if err != nil {
		m.lastMsg = err.Error()
		return
	}
	if len(branches) == 0 {
		m.lastMsg = m.repoMsg(r, "no branches yet — commit first")
		return
	}
	m.ovKind = overlayBranches
	m.ovBranches = branches
	m.ovRepo = r
	items := make([]overlay.Item, len(branches))
	for i, b := range branches {
		detail := ""
		switch {
		case b.Remote:
			detail = "remote"
		case b.Name == r.snap.Branch:
			detail = "current"
		}
		items[i] = overlay.Item{Label: b.Name, Detail: detail}
	}
	title := "Branch:"
	if m.git.multi() {
		title = "Branch (" + r.name + "):"
	}
	m.ov = overlay.New(title, items, m.width)
}

// openHistoryPicker lists the target repo's recent commits; Enter opens the
// commit's full diff in a read-only tab.
// ponytail: last 200 commits, one sync exec (~10ms); paging if anyone asks.
func (m *Model) openHistoryPicker(r *repoState) {
	commits, err := git.Log(r.top, 200)
	if err != nil {
		m.lastMsg = err.Error()
		return
	}
	if len(commits) == 0 {
		m.lastMsg = m.repoMsg(r, "no commits yet")
		return
	}
	m.ovKind = overlayHistory
	m.ovCommits = commits
	m.ovRepo = r
	items := make([]overlay.Item, len(commits))
	for i, c := range commits {
		items[i] = overlay.Item{
			Label:  c.SHA + " " + c.Subject,
			Detail: c.Author + ", " + age(c.Time),
		}
	}
	title := "Commit:"
	if m.git.multi() {
		title = "Commit (" + r.name + "):"
	}
	m.ov = overlay.New(title, items, m.width)
}

// ---- commit graph (git draws it; we just show the text) ----

const gitGraphTitle = "Git Graph"

// gitOpenGraph shows `git log --graph --all` in a read-only tab. Enter on a
// line opens that commit's diff (see the dispatchKey hook). The tab
// remembers its repo so that Enter targets the right one.
func (m *Model) gitOpenGraph() tea.Cmd {
	return m.withRepo(func(m *Model, r *repoState) tea.Cmd {
		cs, err := git.GraphLog(r.top, 500)
		if err != nil {
			m.lastMsg = err.Error()
			return nil
		}
		if len(cs) == 0 {
			m.lastMsg = m.repoMsg(r, "no commits yet")
			return nil
		}
		text := renderGraph(cs)
		title := gitGraphTitle
		if m.git.multi() {
			title += " · " + r.name
		}
		m.openVirtualSyn(title, text, logSyntax{})
		if d := m.doc(); d != nil {
			d.repo = r
			for i, ln := range strings.Split(text, "\n") { // land past the branch-name header
				if shaRe.MatchString(ln) {
					d.ed.Go(i, 0)
					break
				}
			}
		}
		m.lastMsg = "enter: open commit"
		return nil
	})
}

var shaRe = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

// gitOpenCommitAtCursor opens the commit named on the graph line under the
// cursor. Graph-only lines ("|/", "| *") have no sha and are ignored.
func (m *Model) gitOpenCommitAtCursor(d *doc) {
	line, _ := d.ed.Cursor()
	sha := shaRe.FindString(string(d.ed.Buf.Line(line)))
	r := d.repo
	if sha == "" || r == nil {
		return
	}
	text, err := git.ShowCommit(r.top, sha)
	if err != nil {
		m.lastMsg = err.Error()
		return
	}
	m.openVirtual(sha+" (commit)", text)
	if cd := m.doc(); cd != nil {
		cd.repo = r
	}
}

// ---- push / pull (async: exec runs in the tea.Cmd goroutine) ----

type gitOpMsg struct {
	repo    *repoState
	op, out string
	err     error
}

func (m *Model) gitOp(op string) tea.Cmd {
	return m.withRepo(func(m *Model, r *repoState) tea.Cmd { return m.gitOpRepo(r, op) })
}

func (m *Model) gitOpRepo(r *repoState, op string) tea.Cmd {
	if m.git.busy != "" {
		m.lastMsg = "git " + m.git.busy + " already in progress"
		return nil
	}
	m.git.busy = op
	top := r.top
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
		return gitOpMsg{repo: r, op: op, out: out, err: err}
	}
}

func (m Model) handleGitOp(msg gitOpMsg) (Model, tea.Cmd) {
	m.git.busy = ""
	if msg.err != nil {
		// Translate git's two everyday walls into the way out.
		e := msg.err.Error()
		switch {
		case strings.Contains(e, "MERGE_HEAD exists"):
			m.lastMsg = m.repoMsg(msg.repo, "merge in progress — commit (c in git panel) to conclude it")
		case strings.Contains(e, "[rejected]") || strings.Contains(e, "non-fast-forward") || strings.Contains(e, "fetch first"):
			m.lastMsg = m.repoMsg(msg.repo, "push rejected — remote has new commits: pull, resolve, then push")
		default:
			m.lastMsg = e
		}
	} else {
		// git's raw chatter ("remote:", progress lines) makes a poor status
		// message — say what happened, and to which repo, instead.
		low := strings.ToLower(msg.out)
		branch := msg.repo.snap.Branch
		var s string
		switch {
		case msg.op == "fetch":
			s = "fetched"
		case strings.Contains(low, "up to date") || strings.Contains(low, "up-to-date"):
			s = "already up to date"
		case strings.Contains(low, "set up to track"):
			s = "published branch " + branch + " to origin"
		case msg.op == "push":
			s = "pushed " + branch
		default:
			s = "pulled " + branch
		}
		m.lastMsg = m.repoMsg(msg.repo, s)
	}
	m.refreshGit()
	if msg.op == "pull" {
		m.side.Refresh() // pull may create/delete files
		// A conflicted merge exits non-zero with unhelpful first-line chatter;
		// the post-refresh status is the reliable signal. Point at the fix.
		for _, r := range m.git.repos {
			if r.top != msg.repo.top {
				continue
			}
			n := 0
			for _, f := range r.snap.Files {
				if f.Conflict() {
					n++
				}
			}
			if n > 0 {
				m.lastMsg = m.repoMsg(r, fmt.Sprintf("merge conflict in %d file(s) — o: keep ours, t: keep theirs, or edit + stage", n))
			}
		}
	}
	return m, nil
}

// ---- gutter signs ----

// loadGitHead fetches the doc's HEAD baseline and recomputes its signs.
// nil baseline (untracked file, no repo, virtual tab) = no signs. The repo
// is resolved per file — each doc diffs against its own repo's HEAD.
func (m *Model) loadGitHead(d *doc) {
	d.head = nil
	if !d.virtual {
		if r := m.repoForDoc(d.path); r != nil {
			if rel := gitRelPath(r.top, d.path); !strings.HasPrefix(rel, "..") {
				if b, err := git.Show(r.top, rel); err == nil {
					d.head = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
				}
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
	if !m.git.blameOn || d == nil || d.virtual || d.blame != nil || d.blameBusy || d.head == nil {
		return nil
	}
	r := m.repoOf(d.path)
	if r == nil {
		return nil
	}
	d.blameBusy = true
	top, path := r.top, gitRelPath(r.top, d.path)
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
	head := " Git"
	if len(m.git.repos) == 1 && m.git.repos[0].snap.Branch != "" {
		head = " Git: " + m.git.repos[0].snap.Branch
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
		if r.repoHead {
			sb.WriteString(gitHeadStyle.Render(sidebar.Pad(" "+r.header, w)))
			continue
		}
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

// gitSeg is the status-bar segment: branch, ahead/behind, change count —
// for the current target repo (active file's repo; panel cursor's when the
// panel is focused). With several repos the name disambiguates the branch.
func (m Model) gitSeg() string {
	r := m.curRepo()
	if r == nil || r.snap.Branch == "" {
		return ""
	}
	s := "⎇ " + r.snap.Branch
	if m.git.multi() {
		s = "⎇ " + r.name + ":" + r.snap.Branch
	}
	if r.snap.Merging {
		s += " (merging)"
	}
	if r.snap.Ahead > 0 {
		s += fmt.Sprintf(" ↑%d", r.snap.Ahead)
	}
	if r.snap.Behind > 0 {
		s += fmt.Sprintf(" ↓%d", r.snap.Behind)
	}
	if n := len(r.snap.Files); n > 0 {
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
var logTailRe = regexp.MustCompile(` · [^·]*$`)

// laneClasses colors the graph's lane characters by column so each branch
// line keeps one color as it snakes down — the "which branch is this"
// readability fix. Lanes are two columns wide ("| | *").
var laneClasses = []int{
	editor.ClassKeyword, editor.ClassString, editor.ClassNumber,
	editor.ClassType, editor.ClassConstant, editor.ClassProperty,
}

func (logSyntax) Spans(src []byte, startOff, endOff int) []editor.HLSpan {
	var spans []editor.HLSpan
	forEachLine(src, startOff, endOff, func(lo, hi int, line []byte) {
		graphEnd := len(line)
		m := shaRe.FindIndex(line)
		if m != nil {
			graphEnd = m[0]
			spans = append(spans, editor.HLSpan{Start: lo + m[0], End: lo + m[1], Class: editor.ClassFunction})
			rest := line[m[1]:]
			if d := logDecoRe.FindIndex(rest); d != nil {
				spans = append(spans, editor.HLSpan{Start: lo + m[1] + d[0], End: lo + m[1] + d[1], Class: editor.ClassKeyword})
			}
			if t := logTailRe.FindIndex(rest); t != nil {
				spans = append(spans, editor.HLSpan{Start: lo + m[1] + t[0], End: lo + m[1] + t[1], Class: editor.ClassComment})
			}
		}
		// Box-drawing glyphs are multi-byte: color by visual column, not byte.
		header := m == nil // sha-less rows are the branch-name header
		nameLane := -1     // header row: ╭ marks the lane whose name follows
		for j, col := 0, 0; j < graphEnd; col++ {
			r, size := utf8.DecodeRune(line[j:])
			if header && r == '╭' {
				nameLane = col / 2
			} else if nameLane >= 0 && r != '─' && r != ' ' {
				// The branch name: one span in its lane's color.
				spans = append(spans, editor.HLSpan{Start: lo + j, End: lo + graphEnd, Class: laneClasses[nameLane%len(laneClasses)]})
				break
			}
			if r != ' ' && r != '\n' {
				spans = append(spans, editor.HLSpan{Start: lo + j, End: lo + j + size, Class: laneClasses[(col/2)%len(laneClasses)]})
			}
			j += size
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
