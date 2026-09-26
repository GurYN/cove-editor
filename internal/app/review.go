package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/git"
	"github.com/GurYN/cove-editor/internal/sidebar"
)

// Agent review: every edit the agent makes on disk becomes a hunk the user
// accepts or reverts from inside Cove. A checkpoint (git.Checkpoint) is
// taken when the agent launches, on demand, and whenever the user accepts
// everything; the panel lists what changed since, grouped by file, and
// re-diffs whenever the terminal panel produces output. Reverting a hunk
// is an ordinary undoable edit on an open buffer (then saved), or a disk
// write for a file nobody has open. Follow mode opens whatever the agent
// just touched in a single reusable tab, without stealing focus.

type reviewFile struct {
	repo *repoState
	abs  string
	rel  string // workspace-relative, slash form
	diff git.FileDiff
}

// pseudo reports whether the file has no hunks to show line by line (new,
// deleted, or binary): one row stands for the whole file.
func (f reviewFile) pseudo() bool { return len(f.diff.Hunks) == 0 }

// signature identifies the file's current change set, for follow mode and
// the accepted set.
func (f reviewFile) signature() string {
	if f.pseudo() {
		return string(f.diff.Status)
	}
	var sb strings.Builder
	for _, h := range f.diff.Hunks {
		sb.WriteString(h.Signature())
		sb.WriteByte('\n')
	}
	return sb.String()
}

// reviewRow is one panel line: a file header (hunk < 0) or a hunk.
type reviewRow struct {
	file, hunk int
}

type reviewPanel struct {
	view     bool
	follow   bool
	cps      map[string]string // repo top → checkpoint commit
	at       time.Time         // when the checkpoint was taken
	files    []reviewFile
	rows     []reviewRow
	sel, top int
	accepted map[string]bool   // rel + "\x00" + hunk signature (or status for pseudo rows)
	seen     map[string]string // rel → signature at the last refresh (follow mode)
	followed string            // abs path of the tab follow mode opened last
	busy     bool              // a refresh is running
	again    bool              // terminal output arrived during the refresh: run once more
	gen      int               // checkpoint generation; stale refresh results are dropped
}

type checkpointMsg struct {
	cps  map[string]string
	errs []string
	gen  int
}

type reviewMsg struct {
	files []reviewFile
	err   error
	gen   int
}

func (p *reviewPanel) active() bool { return len(p.cps) > 0 }

func (p *reviewPanel) scroll(h int) {
	if p.sel < p.top {
		p.top = p.sel
	}
	if p.sel >= p.top+h {
		p.top = p.sel - h + 1
	}
}

// move steps the selection over hunk rows, skipping file headers.
func (p *reviewPanel) move(d, h int) {
	for i := p.sel + d; i >= 0 && i < len(p.rows); i += d {
		if p.rows[i].hunk >= 0 {
			p.sel = i
			p.scroll(h)
			return
		}
	}
}

func (p *reviewPanel) wheel(delta, h int) {
	p.top = clampInt(p.top+delta, 0, max(0, len(p.rows)-h))
}

func (p *reviewPanel) selected() (reviewFile, int, bool) {
	if p.sel < len(p.rows) && p.rows[p.sel].hunk >= 0 {
		r := p.rows[p.sel]
		return p.files[r.file], r.hunk, true
	}
	return reviewFile{}, 0, false
}

func (p *reviewPanel) rebuild() {
	p.rows = p.rows[:0]
	for i, f := range p.files {
		p.rows = append(p.rows, reviewRow{file: i, hunk: -1})
		if f.pseudo() {
			p.rows = append(p.rows, reviewRow{file: i, hunk: 0})
			continue
		}
		for j := range f.diff.Hunks {
			p.rows = append(p.rows, reviewRow{file: i, hunk: j})
		}
	}
	if p.sel >= len(p.rows) {
		p.sel = max(0, len(p.rows)-1)
	}
	if p.sel < len(p.rows) && p.rows[p.sel].hunk < 0 {
		p.move(+1, 1<<20)
	}
}

func (p *reviewPanel) hunkCount() int {
	n := 0
	for _, r := range p.rows {
		if r.hunk >= 0 {
			n++
		}
	}
	return n
}

// acceptedKey names a hunk (or whole-file row) in the accepted set.
func acceptedKey(f reviewFile, hunk int) string {
	if f.pseudo() {
		return f.rel + "\x00" + string(f.diff.Status)
	}
	return f.rel + "\x00" + f.diff.Hunks[hunk].Signature()
}

// ---- checkpoint & refresh ----

// reviewCheckpointCmd snapshots every repo in the workspace, off the UI
// thread, and starts a fresh review from it.
func (m *Model) reviewCheckpointCmd() tea.Cmd {
	m.discoverRepos()
	if len(m.git.repos) == 0 {
		m.notifyErr("agent review needs a git repository")
		return nil
	}
	tops := make([]string, 0, len(m.git.repos))
	for _, r := range m.git.repos {
		tops = append(tops, r.top)
	}
	m.review.gen++
	gen := m.review.gen
	return func() tea.Msg {
		msg := checkpointMsg{cps: map[string]string{}, gen: gen}
		for _, top := range tops {
			sha, err := git.Checkpoint(top)
			if err != nil {
				msg.errs = append(msg.errs, filepath.Base(top)+": "+err.Error())
				continue
			}
			msg.cps[top] = sha
		}
		return msg
	}
}

func (m *Model) handleCheckpoint(msg checkpointMsg) tea.Cmd {
	if msg.gen != m.review.gen {
		return nil
	}
	p := &m.review
	old := p.files
	p.cps, p.at = msg.cps, time.Now()
	p.files, p.rows, p.sel, p.top = nil, p.rows[:0], 0, 0
	p.accepted, p.seen = map[string]bool{}, map[string]string{}
	p.busy = false
	m.reviewReloadBaselines(old, nil)
	if len(msg.errs) > 0 {
		m.notifyErr("checkpoint: " + strings.Join(msg.errs, "; "))
	} else if len(msg.cps) > 0 {
		m.notify("checkpoint taken — the agent's changes from now on show in Review")
	}
	return nil
}

// reviewRefreshCmd re-diffs the worktree against the checkpoint. One at a
// time: a refresh requested while one runs is queued, not stacked.
func (m *Model) reviewRefreshCmd() tea.Cmd {
	p := &m.review
	if !p.active() {
		return nil
	}
	if p.busy {
		p.again = true
		return nil
	}
	p.busy = true
	root := m.side.Root
	type job struct {
		repo *repoState
		sha  string
	}
	var jobs []job
	for _, r := range m.git.repos {
		if sha, ok := p.cps[r.top]; ok {
			jobs = append(jobs, job{r, sha})
		}
	}
	gen := p.gen
	return func() tea.Msg {
		var files []reviewFile
		for _, j := range jobs {
			diffs, err := git.ChangedSince(j.repo.top, j.sha)
			if err != nil {
				return reviewMsg{err: err, gen: gen}
			}
			for _, d := range diffs {
				abs := filepath.Join(j.repo.top, filepath.FromSlash(d.Path))
				files = append(files, reviewFile{repo: j.repo, abs: abs, rel: filepath.ToSlash(rel(root, abs)), diff: d})
			}
		}
		sort.Slice(files, func(i, k int) bool { return files[i].rel < files[k].rel })
		return reviewMsg{files: files, gen: gen}
	}
}

func (m *Model) handleReviewMsg(msg reviewMsg) tea.Cmd {
	p := &m.review
	if msg.gen != p.gen {
		return nil
	}
	p.busy = false
	if msg.err != nil {
		m.notifyErr("review: " + msg.err.Error())
		return nil
	}
	old := p.files
	p.files = nil // fresh slice: old must survive for the baseline reload below
	for _, f := range msg.files {
		if f.pseudo() {
			if !p.accepted[acceptedKey(f, 0)] {
				p.files = append(p.files, f)
			}
			continue
		}
		kept := f.diff.Hunks[:0:0]
		for i, h := range f.diff.Hunks {
			if !p.accepted[acceptedKey(f, i)] {
				kept = append(kept, h)
			}
		}
		if len(kept) > 0 {
			f.diff.Hunks = kept
			p.files = append(p.files, f)
		}
	}
	p.rebuild()
	m.reviewReloadBaselines(old, p.files)
	var cmd tea.Cmd
	if p.follow {
		m.reviewFollow(msg.files)
	}
	for _, f := range msg.files {
		p.seen[f.rel] = f.signature()
	}
	if p.again {
		p.again = false
		cmd = m.reviewRefreshCmd()
	}
	return cmd
}

// reviewReloadBaselines re-derives gutter signs for open docs that entered
// or left the change set: inside the set they diff against the checkpoint,
// outside against HEAD.
func (m *Model) reviewReloadBaselines(old, cur []reviewFile) {
	in := map[string]bool{}
	for _, f := range old {
		in[f.abs] = true
	}
	for _, f := range cur {
		in[f.abs] = true
	}
	for _, d := range m.docs {
		if !d.virtual && in[d.abs()] {
			m.loadGitHead(d)
		}
	}
}

func (d *doc) abs() string {
	a, err := filepath.Abs(d.path)
	if err != nil {
		return d.path
	}
	return a
}

// reviewBase returns the checkpoint content for a file in the change set:
// the gutter baseline while the review is active. Empty content for a file
// the agent created, so every line shows as added.
func (m *Model) reviewBase(abs string) ([]byte, bool) {
	for _, f := range m.review.files {
		if !same(f.abs, abs) {
			continue
		}
		if f.diff.Status == 'A' {
			return []byte{}, true
		}
		b, err := git.ShowAt(f.repo.top, m.review.cps[f.repo.top], f.diff.Path)
		if err != nil {
			return nil, false
		}
		return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), true
	}
	return nil, false
}

// ---- follow mode ----

// reviewFollow opens the file the agent touched most recently in a single
// reusable tab and lands on its first hunk. It never takes focus from the
// terminal, and never touches the editor while the user is typing in it.
func (m *Model) reviewFollow(files []reviewFile) {
	p := &m.review
	if m.focus == paneEditor {
		return
	}
	var pick *reviewFile
	var pickT time.Time
	for i := range files {
		f := &files[i]
		if p.seen[f.rel] == f.signature() || f.diff.Status == 'D' {
			continue
		}
		fi, err := os.Stat(f.abs)
		if err != nil {
			continue
		}
		if pick == nil || fi.ModTime().After(pickT) {
			pick, pickT = f, fi.ModTime()
		}
	}
	if pick == nil {
		return
	}
	focus := m.focus
	defer func() { m.focus = focus }()
	// Retire the previous follow tab if it's still clean and not the pick.
	if p.followed != "" && !same(p.followed, pick.abs) {
		for i, d := range m.docs {
			if !d.virtual && same(d.path, p.followed) && !d.ed.Dirty {
				m.closeDocAt(i)
				break
			}
		}
	}
	m.openFile(pick.abs)
	if d := m.doc(); d != nil && same(d.path, pick.abs) {
		if !pick.pseudo() {
			d.ed.Go(pick.diff.Hunks[0].FirstChange()-1, 0)
			d.ed.Center()
		}
		p.followed = pick.abs
	}
	m.layout()
}

// closeDocAt closes tab i without prompting (callers check Dirty).
func (m *Model) closeDocAt(i int) {
	prev := m.active
	m.active = i
	m.forceClose()
	switch {
	case prev == i:
	case prev > i:
		m.active = prev - 1
	default:
		m.active = prev
	}
	if m.active >= len(m.docs) {
		m.active = max(0, len(m.docs)-1)
	}
}

// ---- panel actions ----

// showReviewPanel swaps the review panel into the sidebar slot; with no
// checkpoint yet it takes one, so opening the panel is the whole setup.
func (m *Model) showReviewPanel() tea.Cmd {
	m.review.view, m.git.view, m.search.view, m.sidebarOpen = true, false, false, true
	m.focus = paneReview
	m.layout()
	if !m.review.active() {
		return m.reviewCheckpointCmd()
	}
	return m.reviewRefreshCmd()
}

func (m *Model) reviewHeight() int { return max(1, m.gitHeight()-1) } // hint line

// reviewOpenSel opens the selected hunk's file at the hunk. A click
// previews (panel keeps focus); Enter moves into the editor.
func (m *Model) reviewOpenSel(focusEditor bool) {
	f, h, ok := m.review.selected()
	if !ok {
		return
	}
	if f.diff.Status == 'D' || f.diff.Binary {
		m.reviewSideDiff()
		if !focusEditor {
			m.focus = paneReview
		}
		return
	}
	m.pushJump()
	m.openFile(f.abs)
	if d := m.doc(); d != nil && same(d.path, f.abs) && !f.pseudo() {
		d.ed.Go(f.diff.Hunks[h].FirstChange()-1, 0)
		d.ed.Center()
	}
	m.layout()
	if !focusEditor {
		m.focus = paneReview
	}
}

func (m *Model) reviewClick(y int) {
	i := m.review.top + y
	if i < 0 || i >= len(m.review.rows) || m.review.rows[i].hunk < 0 {
		return
	}
	m.review.sel = i
	m.reviewOpenSel(false)
}

// reviewSideDiff opens the selected file as checkpoint │ now columns.
func (m *Model) reviewSideDiff() {
	f, _, ok := m.review.selected()
	if !ok {
		return
	}
	var oldB, newB []byte
	if f.diff.Status != 'A' {
		oldB, _ = git.ShowAt(f.repo.top, m.review.cps[f.repo.top], f.diff.Path)
	}
	if f.diff.Status != 'D' {
		newB, _ = os.ReadFile(f.abs)
	}
	if f.diff.Binary {
		m.notify("binary file — r restores the checkpoint version")
		return
	}
	m.openSideDiff(f.rel+" (review)", f.diff.Path, oldB, newB)
}

func (m *Model) reviewAcceptSel() {
	f, h, ok := m.review.selected()
	if !ok {
		return
	}
	m.review.accepted[acceptedKey(f, h)] = true
	p := &m.review
	if f.pseudo() || len(f.diff.Hunks) == 1 {
		p.files = append(p.files[:p.rows[p.sel].file], p.files[p.rows[p.sel].file+1:]...)
	} else {
		fi := p.rows[p.sel].file
		p.files[fi].diff.Hunks = append(p.files[fi].diff.Hunks[:h:h], p.files[fi].diff.Hunks[h+1:]...)
	}
	p.rebuild()
	if len(p.rows) == 0 {
		m.notify("all reviewed — A takes a new checkpoint")
	}
}

// reviewAcceptAll takes a new checkpoint: everything so far is the new baseline.
func (m *Model) reviewAcceptAll() tea.Cmd {
	return m.reviewCheckpointCmd()
}

// reviewRevertSel undoes the selected hunk (or whole-file change) on disk
// and in any open buffer, then re-diffs.
func (m *Model) reviewRevertSel() tea.Cmd {
	f, h, ok := m.review.selected()
	if !ok {
		return nil
	}
	if f.diff.Status == 'A' {
		*m = m.prompt(fmt.Sprintf("delete %s (created by the agent)? y/n:", f.rel), "", func(m *Model, text string) {
			if !strings.EqualFold(text, "y") {
				return
			}
			if d := m.docByPath(f.abs); d != nil {
				if d.ed.Dirty {
					m.notifyErr(f.rel + " has unsaved changes — save or close it first")
					return
				}
				for i, dd := range m.docs {
					if dd == d {
						m.closeDocAt(i)
						break
					}
				}
			}
			if err := os.Remove(f.abs); err != nil {
				m.notifyErr(err.Error())
				return
			}
			m.deferred = m.reviewAfterRevert("deleted " + f.rel)
		})
		return nil
	}
	if f.diff.Status == 'D' || f.diff.Binary {
		b, err := git.ShowAt(f.repo.top, m.review.cps[f.repo.top], f.diff.Path)
		if err != nil {
			m.notifyErr(err.Error())
			return nil
		}
		os.MkdirAll(filepath.Dir(f.abs), 0o755)
		if err := os.WriteFile(f.abs, b, 0o644); err != nil {
			m.notifyErr(err.Error())
			return nil
		}
		m.reloadDoc(f.abs)
		return m.reviewAfterRevert("restored " + f.rel)
	}
	hunk := f.diff.Hunks[h]
	if d := m.docByPath(f.abs); d != nil {
		if d.ed.Dirty {
			m.notifyErr(f.rel + " has unsaved changes — save it first, then revert")
			return nil
		}
		data := d.ed.Buf.Bytes()
		off, end, repl, err := revertHunk(data, hunk)
		if err != nil {
			m.notifyErr(err.Error())
			return nil
		}
		line, col := d.ed.Cursor()
		d.ed.ApplyEdits([]editor.Edit{{Off: off, Old: data[off:end], New: repl}})
		d.ed.Go(line, col)
		if s := d.save(); s != "saved" {
			m.notifyErr(s)
			return nil
		}
	} else {
		data, err := os.ReadFile(f.abs)
		if err != nil {
			m.notifyErr(err.Error())
			return nil
		}
		off, end, repl, err := revertHunk(data, hunk)
		if err != nil {
			m.notifyErr(err.Error())
			return nil
		}
		out := append(append(append([]byte{}, data[:off]...), repl...), data[end:]...)
		if err := os.WriteFile(f.abs, out, 0o644); err != nil {
			m.notifyErr(err.Error())
			return nil
		}
	}
	return m.reviewAfterRevert(fmt.Sprintf("reverted %s:%d", f.rel, hunk.FirstChange()))
}

// reviewAfterRevert resyncs everything that watches the worktree and
// re-diffs. The removed row is gone on the next refresh; the selection
// stays where it is so repeated r walks down the list.
func (m *Model) reviewAfterRevert(msg string) tea.Cmd {
	m.notify(msg)
	m.side.Refresh()
	m.refreshGit()
	m.syncWatched()
	return m.reviewRefreshCmd()
}

// revertHunk computes the edit that puts a hunk's old lines back: the byte
// range [off, end) of the current content holding the hunk's new lines,
// and the replacement. It refuses when the content no longer matches the
// hunk (the file moved on since the last refresh).
func revertHunk(data []byte, h git.Hunk) (off, end int, repl []byte, err error) {
	starts := []int{0}
	for i, b := range data {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		starts = append(starts, len(data))
	}
	nlines := len(starts) - 1
	startLine := h.NewStart - 1
	if h.NewLines == 0 { // pure deletion: put the lines back after line NewStart
		startLine = h.NewStart
	}
	endLine := startLine + h.NewLines
	if startLine < 0 || endLine > nlines {
		return 0, 0, nil, fmt.Errorf("hunk out of range — refresh (R) and retry")
	}
	off, end = starts[startLine], starts[endLine]
	want := strings.Join(h.New, "\n")
	if len(h.New) > 0 && !h.NewNoNL {
		want += "\n"
	}
	if string(data[off:end]) != want {
		return 0, 0, nil, fmt.Errorf("file changed since the last refresh — R to refresh")
	}
	s := strings.Join(h.Old, "\n")
	if len(h.Old) > 0 && !h.OldNoNL {
		s += "\n"
	}
	if off == len(data) && len(data) > 0 && data[len(data)-1] != '\n' && s != "" {
		s = "\n" + s // appending after a final line that has no newline
	}
	return off, end, []byte(s), nil
}

// ---- rendering ----

func statusGlyph(f reviewFile) string {
	switch {
	case f.diff.Status == 'A':
		return "A"
	case f.diff.Status == 'D':
		return "D"
	default:
		return "M"
	}
}

func hunkLabel(f reviewFile, h int) string {
	if f.pseudo() {
		switch {
		case f.diff.Binary:
			return "  binary file changed"
		case f.diff.Status == 'A':
			return "  new file"
		case f.diff.Status == 'D':
			return "  deleted"
		}
		return "  changed"
	}
	hk := f.diff.Hunks[h]
	s := fmt.Sprintf("  %d: +%d −%d", hk.FirstChange(), hk.Added, hk.Removed)
	if hk.Header != "" {
		s += "  " + hk.Header
	}
	return s
}

// reviewPanelView renders the panel in the sidebar slot, every row exactly
// side.Width cells: header, key hint, then the rows.
func (m Model) reviewPanelView() string {
	w := m.side.Width
	p := m.review
	var sb strings.Builder
	head := " Review"
	if p.active() {
		head = fmt.Sprintf(" Review · %d file(s) · %d hunk(s)", len(p.files), p.hunkCount())
		if p.busy {
			head += " …"
		}
	}
	sb.WriteString(gitHeadStyle.Render(sidebar.Pad(head, w)))
	hint := " space peek · r revert · a/A accept"
	if p.follow {
		hint = " f follow:on · r revert · a/A accept"
	}
	sb.WriteByte('\n')
	sb.WriteString(gitSectionStyle.Render(sidebar.Pad(hint, w)))
	h := m.reviewHeight()
	for i := p.top; i < p.top+h; i++ {
		sb.WriteByte('\n')
		if i >= len(p.rows) {
			if i == 0 {
				msg := " no changes since checkpoint"
				if !p.active() {
					msg = " taking a checkpoint…"
				} else if !p.at.IsZero() {
					msg += " (" + p.at.Format("15:04") + ")"
				}
				sb.WriteString(sidebar.Pad(msg, w))
			} else {
				sb.WriteString(strings.Repeat(" ", max(0, w)))
			}
			continue
		}
		r := p.rows[i]
		f := p.files[r.file]
		if r.hunk < 0 {
			sb.WriteString(gitSectionStyle.Render(sidebar.Pad(" "+statusGlyph(f)+" "+f.rel, w)))
			continue
		}
		plain := sidebar.Pad(hunkLabel(f, r.hunk), w)
		switch {
		case i == p.sel && m.focus == paneReview:
			sb.WriteString(gitSelStyle.Render(plain))
		case i == p.sel:
			sb.WriteString(gitSelStyle.Faint(true).Render(plain))
		default:
			sb.WriteString(plain)
		}
	}
	return sb.String()
}
