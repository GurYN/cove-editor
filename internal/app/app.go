// Package app is the Bubbletea root model: tabs, sidebar, overlays, and
// key/mouse routing live here; each editor pane manages itself.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/GurYN/cove-editor/internal/action"
	"github.com/GurYN/cove-editor/internal/config"
	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/git"
	"github.com/GurYN/cove-editor/internal/lsp"
	"github.com/GurYN/cove-editor/internal/overlay"
	"github.com/GurYN/cove-editor/internal/sidebar"
	"github.com/GurYN/cove-editor/internal/term"
)

var (
	statusStyle    = lipgloss.NewStyle().Reverse(true)
	promptStyle    = lipgloss.NewStyle().Bold(true)
	tabActiveStyle = lipgloss.NewStyle().Reverse(true).Bold(true)
	tabStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("236"))
	tabBarStyle    = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	borderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	welcomeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

// applyChrome themes the panel chrome (tab bar tint, pane border) from the
// same color map as the editor.
func applyChrome(colors map[string]string) {
	bg, fg, border := colors["ui.bg"], colors["ui.fg"], colors["ui.border"]
	if bg != "" {
		tabBarStyle = lipgloss.NewStyle().Background(lipgloss.Color(bg))
		tabStyle = lipgloss.NewStyle().Background(lipgloss.Color(bg))
		if fg != "" {
			tabStyle = tabStyle.Foreground(lipgloss.Color(fg))
		}
	}
	if border != "" {
		borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(border))
	}
	applyGitChrome(colors)
}

type mode int

const (
	modeEdit mode = iota
	modeFind
	modeReplace
	modePrompt
)

type pane int

const (
	paneEditor pane = iota
	paneSidebar
	paneTabs
	paneDivider // sidebar/editor border column: drag to resize
	paneTerminal
	panePanelDivider // terminal panel title row: drag to resize
	paneGit          // git panel occupying the sidebar slot
)

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayPalette
	overlayFinder
	overlayRefs
	overlayDiags
	overlayBranches
)

// problemRef is one row of the Problems list: where Enter should land.
type problemRef struct {
	path      string
	line, col int
}

const sidebarWidth = 30

type Model struct {
	reg    *action.Registry
	docs   []*doc
	active int

	side        sidebar.Model
	sidebarOpen bool
	sidebarW    int  // user-set width; divider drag adjusts it
	focus       pane // paneEditor or paneSidebar

	ovKind     overlayKind
	ov         overlay.Model
	ovActions  []action.Action // palette payload
	ovFiles    []string        // finder payload
	ovRefs     []lsp.Location  // references payload
	ovDiags    []problemRef    // problems payload
	ovBranches []string        // branch picker payload

	lspm          *lsp.Manager
	lspStatus     map[string]string
	changePending bool
	hoverText     string
	compl         complState

	mode     mode
	query    string
	repl     string
	useRegex bool

	// prompt (new file / rename / confirm delete)
	promptLabel string
	promptText  string
	promptDo    func(*Model, string)
	deferred    tea.Cmd // set by prompt callbacks that need a follow-up Cmd

	gitView bool // left pane shows the git panel instead of the file tree
	gitSnap git.Snapshot
	gitRows []gitRow
	gitSel  int
	gitTop  int
	gitErr  string
	gitBusy string // "push"/"pull" while one is in flight
	blameOn bool   // inline blame for the cursor line (git.blame toggle)

	terms      []*term.Term // shell instances; empty until first opened
	termActive int
	termOpen   bool
	termH      int // panel content rows

	mouseDown  pane      // pane that owns the current drag
	hoverShape string    // pointer shape currently set ("" = default)
	vim        *vimState // nil unless keymap = "vim"

	width, height int
	lastMsg       string
	lastCost      time.Duration
}

// New builds the app. path may be a file (opened as the first tab), a
// directory (becomes the workspace root), or empty (cwd).
func New(path string, data []byte) Model {
	cfg, cfgErr := config.Load()
	editor.ApplyTheme(cfg.ThemeColors())
	applyChrome(cfg.ThemeColors())
	editor.SetTabStop(cfg.Editor.TabSize)
	editor.SetLineNumbers(cfg.Editor.LineNumbers)
	for lang, s := range cfg.LSP {
		lsp.Configure(lang, s.Command, s.Extensions, s.LangID)
	}

	root := "."
	m := Model{sidebarOpen: true, focus: paneEditor, sidebarW: sidebarWidth, termH: termDefaultRows}
	if cfg.Keymap == "vim" {
		m.vim = &vimState{}
	}
	if cfgErr != nil {
		m.lastMsg = "config error: " + cfgErr.Error()
	}
	if path != "" {
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			root = path
		} else {
			root = filepath.Dir(path)
			m.docs = append(m.docs, newDoc(path, data))
		}
	}
	if len(m.docs) == 0 { // nothing to edit yet: start in the file tree
		m.focus = paneSidebar
	}
	m.side = sidebar.New(root)
	m.refreshGit() // branch segment in the status bar from the first frame
	m.gitErr = ""  // not being a repo is fine until the panel is opened
	m.reg = newRegistry()
	for id, key := range cfg.Keys {
		if !m.reg.Rebind(id, key) {
			m.lastMsg = "config: unknown action " + id
		}
	}
	m.lspm = lsp.NewManager(m.side.Root)
	m.lspStatus = map[string]string{}
	if d := m.doc(); d != nil {
		m.lspm.Open(d.path, d.ed.Buf.Bytes(), d.ed.Rev)
	}
	return m
}

func (m *Model) doc() *doc {
	if len(m.docs) == 0 {
		return nil
	}
	return m.docs[m.active]
}

func (m Model) Init() tea.Cmd { return listenLSP(m.lspm) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	start := time.Now()
	next, cmd := m.update(msg)
	// Single choke point for lazy blame: whatever changed the active doc
	// (toggle, tab switch, open), the fetch gets scheduled here.
	if bc := next.blameCmdIfNeeded(); bc != nil {
		cmd = tea.Batch(cmd, bc)
	}
	next.lastCost = time.Since(start)
	return next, cmd
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	keylog(msg)
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case lspEventMsg:
		m.handleLSPEvent(lsp.Event(msg))
		return m, listenLSP(m.lspm)
	case changeTickMsg:
		return m, m.flushChange()
	case termMsg:
		return m.handleTermMsg(msg)
	case gitOpMsg:
		return m.handleGitOp(msg)
	case blameMsg:
		return m.handleBlame(msg)
	case lspErrMsg:
		m.lastMsg = msg.err.Error()
		return m, nil
	case defMsg:
		if len(msg.locs) == 0 {
			m.lastMsg = "no definition found"
		} else {
			m.jumpTo(msg.locs[0])
			m.layout()
		}
		return m, nil
	case refsMsg:
		return m.openReferences(msg.locs), nil
	case symsMsg:
		return m.openSymbols(msg.syms), nil
	case hoverMsg:
		if msg.text == "" {
			m.lastMsg = "no documentation"
		}
		m.hoverText = msg.text
		return m, nil
	case wsEditMsg:
		m.applyWorkspaceEdit(msg.edit)
		return m, m.syncLSP()
	case fmtMsg:
		if d := m.docByPath(msg.path); d != nil && len(msg.edits) > 0 {
			d.ed.ApplyEdits(toEditorEdits(d.ed.Buf, msg.edits))
			m.lastMsg = "formatted"
		}
		return m, m.syncLSP()
	case complMsg:
		d := m.doc()
		if d != nil && len(msg.items) > 0 && d.ed.Rev == msg.rev {
			m.compl = complState{active: true, items: msg.items}
		}
		return m, nil

	case tea.KeyMsg:
		m.lastMsg = ""
		m.hoverText = "" // any key dismisses the hover card
		// The terminal never delivers a lone Esc: bubbletea's parser buffers
		// the ESC byte until the next key arrives and fuses them into an
		// alt-chord. Unfuse: treat as Esc, then the bare key. Deliberate
		// alt-chords (alt+up/down add-cursor, alt+enter replace-all) stay.
		if msg.Alt && msg.Type != tea.KeyUp && msg.Type != tea.KeyDown &&
			!(msg.Type == tea.KeyEnter && m.mode == modeReplace) {
			m, _ = m.dispatchKey(tea.KeyMsg{Type: tea.KeyEscape})
			msg.Alt = false
			return m.dispatchKey(msg)
		}
		return m.dispatchKey(msg)
	case tea.MouseMsg:
		return m.dispatchMouse(msg)
	}
	return m, nil
}

// editorX is the editor pane's left edge (sidebar + border column).
func (m *Model) editorX() int {
	if !m.sidebarOpen {
		return 0
	}
	return m.side.Width + 1
}

// contentRows is the height left for sidebar/editor: everything minus tab
// bar, bottom bar, and the terminal panel.
func (m *Model) contentRows() int {
	return m.height - 2 - m.panelRows()
}

func (m *Model) layout() {
	sw := 0
	if m.sidebarOpen {
		sw = min(m.sidebarW, m.width/2)
	}
	m.side.Width = sw
	m.side.Height = m.height - 4 // tab bar + bottom bar + switcher row + its spacer; panel sits under the editor only
	for _, d := range m.docs {
		d.ed.Width = m.width - m.editorX()
		d.ed.Height = m.contentRows()
	}
	if m.termOpen {
		for _, t := range m.terms {
			t.Resize(max(2, m.width-m.editorX()), m.termRows())
		}
	}
}

func (m Model) dispatchKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.ovKind != overlayNone {
		return m.updateOverlay(msg)
	}
	switch m.mode {
	case modeFind, modeReplace:
		mm, cmd := m.updateMinibar(msg)
		return mm, tea.Batch(cmd, mm.syncLSP())
	case modePrompt:
		return m.updatePrompt(msg)
	}
	if t := m.activeTerm(); m.focus == paneTerminal && t != nil {
		// Only scrollback, the panel toggle, and quit stay with Cove
		// (respecting rebinds); every other key goes to the shell.
		switch msg.String() {
		case "shift+pgup":
			t.Scroll(+m.termRows())
			return m, nil
		case "shift+pgdown":
			t.Scroll(-m.termRows())
			return m, nil
		}
		if act := m.reg.Lookup(action.Global, msg.String()); act != nil &&
			(act.ID == "term.toggle" || act.ID == "app.quit") {
			cmd := act.Do(&m)
			m.layout()
			return m, cmd
		}
		t.Send(msg)
		return m, nil
	}
	if m.compl.active && m.focus == paneEditor {
		mm, cmd, handled := m.handleComplKey(msg)
		m = mm
		if handled {
			return m, cmd
		}
	}
	if m.vim != nil && m.focus == paneEditor {
		mm, cmd, handled := m.handleVim(msg)
		if handled {
			return mm, cmd
		}
		m = mm
	}
	// Panels bind single letters, but the input reader can coalesce quickly
	// arriving runes (paste, PTY batching) into one KeyRunes msg. Replay
	// them one at a time so "c" still means Commit after " c" arrives fused.
	if (m.focus == paneSidebar || m.focus == paneGit) && msg.Type == tea.KeyRunes && !msg.Alt && len(msg.Runes) > 1 {
		var cmds []tea.Cmd
		for _, r := range msg.Runes {
			var cmd tea.Cmd
			m, cmd = m.dispatchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	ctx := action.Editor
	switch m.focus {
	case paneSidebar:
		ctx = action.Sidebar
	case paneGit:
		ctx = action.Git
	}
	if act := m.reg.Lookup(ctx, msg.String()); act != nil {
		cmd := act.Do(&m)
		m.layout() // actions may open/close panes
		return m, tea.Batch(cmd, m.syncLSP())
	}
	if m.focus == paneEditor {
		if d := m.doc(); d != nil {
			var cmd tea.Cmd
			d.ed, cmd = d.ed.Update(msg)
			// Auto-trigger completion after a member access dot.
			if msg.Type == tea.KeyRunes && !msg.Alt && strings.HasSuffix(string(msg.Runes), ".") &&
				lsp.LangFor(d.path) != "" && !m.compl.active {
				return m, tea.Batch(cmd, m.syncLSP(), cmdCompletion(&m))
			}
			return m, tea.Batch(cmd, m.syncLSP())
		}
	}
	return m, nil
}

// ---- overlays ----

func (m Model) openPalette() Model {
	m.ovKind = overlayPalette
	m.ovActions = m.reg.Palette()
	items := make([]overlay.Item, len(m.ovActions))
	for i, a := range m.ovActions {
		items[i] = overlay.Item{Label: a.Title, Detail: a.Key}
	}
	m.ov = overlay.New("Command:", items, m.width)
	return m
}

func (m Model) openFinder() Model {
	m.ovKind = overlayFinder
	m.ovFiles = listFiles(m.side.Root)
	items := make([]overlay.Item, len(m.ovFiles))
	for i, f := range m.ovFiles {
		items[i] = overlay.Item{Label: filepath.Base(f), Detail: filepath.Dir(f)}
	}
	m.ov = overlay.New("File:", items, m.width)
	return m
}

func (m Model) updateOverlay(k tea.KeyMsg) (Model, tea.Cmd) {
	ov, chosen, done := m.ov.Update(k)
	m.ov = ov
	if !done {
		return m, nil
	}
	kind := m.ovKind
	m.ovKind = overlayNone
	m.focus = paneEditor
	if chosen < 0 {
		return m, nil
	}
	switch kind {
	case overlayPalette:
		act := m.ovActions[chosen]
		cmd := act.Do(&m)
		m.layout()
		return m, tea.Batch(cmd, m.syncLSP())
	case overlayFinder:
		m.openFile(filepath.Join(m.side.Root, m.ovFiles[chosen]))
	case overlayRefs:
		m.jumpTo(m.ovRefs[chosen])
		m.layout()
	case overlayBranches:
		name := m.ovBranches[chosen]
		if err := git.Checkout(m.gitSnap.Top, name); err != nil {
			m.lastMsg = err.Error()
		} else {
			m.lastMsg = "switched to " + name
		}
		m.refreshGit()
		m.side.Refresh() // checkout swaps working-tree files
	case overlayDiags:
		ref := m.ovDiags[chosen]
		m.openFile(ref.path)
		if d := m.doc(); d != nil && same(d.path, ref.path) {
			d.ed.Go(ref.line, ref.col)
			d.ed.Center()
		}
		m.layout()
	}
	return m, nil
}

// openSymbols shows the active file's outline (functions, types, …) as a
// fuzzy picker. Rows reuse the Problems overlay plumbing: pick → jump.
func (m Model) openSymbols(syms []lsp.DocumentSymbol) Model {
	d := m.doc()
	if d == nil {
		return m
	}
	if len(syms) == 0 {
		m.lastMsg = "no symbols in this file"
		return m
	}
	m.ovKind = overlayDiags
	m.ovDiags = m.ovDiags[:0]
	var items []overlay.Item
	var walk func(s []lsp.DocumentSymbol, depth int)
	walk = func(s []lsp.DocumentSymbol, depth int) {
		for _, sym := range s {
			line, col := d.ed.Buf.Pos(offsetOf(d.ed.Buf, sym.SelectionRange.Start))
			m.ovDiags = append(m.ovDiags, problemRef{path: d.path, line: line, col: col})
			items = append(items, overlay.Item{
				Label:  strings.Repeat("  ", depth) + symbolGlyph(sym.Kind) + " " + sym.Name,
				Detail: fmt.Sprintf(":%d", line+1),
			})
			walk(sym.Children, depth+1)
		}
	}
	walk(syms, 0)
	m.ov = overlay.New("Symbol:", items, m.width)
	return m
}

// symbolGlyph maps the LSP SymbolKind enum to a one-cell marker.
func symbolGlyph(kind int) string {
	switch kind {
	case 5, 23: // class, struct
		return "◆"
	case 11: // interface
		return "◇"
	case 6, 9, 12: // method, constructor, function
		return "ƒ"
	case 10, 22: // enum, enum member
		return "≡"
	default: // fields, variables, constants, modules, …
		return "·"
	}
}

// openProblems lists every diagnostic across open tabs, errors first.
func (m Model) openProblems() Model {
	type row struct {
		ref      problemRef
		severity int
		msg      string
		base     string
	}
	var rows []row
	for _, d := range m.docs {
		for _, diag := range d.ed.Diags {
			line, col := d.ed.Buf.Pos(min(diag.Start, d.ed.Buf.Len()))
			rows = append(rows, row{
				ref:      problemRef{path: d.path, line: line, col: col},
				severity: diag.Severity,
				msg:      firstLine(diag.Message),
				base:     filepath.Base(d.path),
			})
		}
	}
	if len(rows) == 0 {
		m.lastMsg = "no problems in open tabs"
		return m
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].severity != rows[j].severity {
			return rows[i].severity < rows[j].severity
		}
		if rows[i].ref.path != rows[j].ref.path {
			return rows[i].ref.path < rows[j].ref.path
		}
		return rows[i].ref.line < rows[j].ref.line
	})
	glyph := func(sev int) string {
		switch {
		case sev <= 1:
			return "●"
		case sev == 2:
			return "▲"
		default:
			return "○"
		}
	}
	m.ovKind = overlayDiags
	m.ovDiags = m.ovDiags[:0]
	items := make([]overlay.Item, len(rows))
	for i, r := range rows {
		m.ovDiags = append(m.ovDiags, r.ref)
		items[i] = overlay.Item{
			Label:  fmt.Sprintf("%s %s", glyph(r.severity), r.msg),
			Detail: fmt.Sprintf("%s:%d", r.base, r.ref.line+1),
		}
	}
	m.ov = overlay.New("Problems:", items, m.width)
	return m
}

// ---- prompt (minibar text input driving a callback) ----

func (m Model) prompt(label, initial string, do func(*Model, string)) Model {
	m.mode = modePrompt
	m.promptLabel, m.promptText, m.promptDo = label, initial, do
	return m
}

func (m Model) updatePrompt(k tea.KeyMsg) (Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEscape:
		m.mode = modeEdit
	case tea.KeyEnter:
		do, text := m.promptDo, m.promptText
		m.mode = modeEdit
		do(&m, text)
		if m.deferred != nil {
			cmd := m.deferred
			m.deferred = nil
			return m, cmd
		}
	case tea.KeyBackspace:
		if r := []rune(m.promptText); len(r) > 0 {
			m.promptText = string(r[:len(r)-1])
		}
	case tea.KeySpace:
		m.promptText += " "
	case tea.KeyRunes:
		if !k.Alt {
			m.promptText += string(k.Runes)
		}
	}
	return m, nil
}

// ---- tabs ----

// openFile opens path in a tab (or activates an existing one).
func (m *Model) openFile(path string) {
	abs, _ := filepath.Abs(path)
	for i, d := range m.docs {
		if p, _ := filepath.Abs(d.path); p == abs {
			m.active = i
			m.focus = paneEditor
			return
		}
	}
	d, err := loadDoc(path)
	if err != nil {
		m.lastMsg = err.Error()
		return
	}
	m.docs = append(m.docs, d)
	m.active = len(m.docs) - 1
	m.focus = paneEditor
	m.layout()
	m.lspm.Open(path, d.ed.Buf.Bytes(), d.ed.Rev)
	m.loadGitHead(d)
}

// closeActive closes the active tab, prompting when dirty.
func (m Model) closeActive() Model {
	d := m.doc()
	if d == nil {
		return m
	}
	if d.ed.Dirty {
		return m.prompt(fmt.Sprintf("%s has unsaved changes — close? y/n:", filepath.Base(d.path)), "",
			func(m *Model, text string) {
				if strings.EqualFold(text, "y") {
					m.forceClose()
				}
			})
	}
	m.forceClose()
	return m
}

func (m *Model) forceClose() {
	m.lspm.Close(m.docs[m.active].path)
	m.docs = append(m.docs[:m.active], m.docs[m.active+1:]...)
	if m.active >= len(m.docs) {
		m.active = max(0, len(m.docs)-1)
	}
	if len(m.docs) == 0 {
		m.focus = paneSidebar
	}
}

// tabRanges returns each tab's [start, end) x-range and the x of its close
// glyph, matching renderTabBar exactly.
func (m Model) tabRanges() []struct{ start, end, closeX int } {
	out := make([]struct{ start, end, closeX int }, len(m.docs))
	x := 0
	for i, d := range m.docs {
		label := m.tabLabel(i, d)
		w := lipgloss.Width(label)
		out[i] = struct{ start, end, closeX int }{x, x + w, x + w - 2}
		x += w
	}
	return out
}

func (m Model) tabLabel(i int, d *doc) string {
	dirty := " "
	if d.ed.Dirty {
		dirty = "●"
	}
	return fmt.Sprintf(" %s %s × ", filepath.Base(d.path), dirty)
}

func (m Model) renderTabBar() string {
	var sb strings.Builder
	for i, d := range m.docs {
		label := m.tabLabel(i, d)
		if i == m.active {
			sb.WriteString(tabActiveStyle.Render(label))
		} else {
			sb.WriteString(tabStyle.Render(label))
		}
	}
	rest := m.width - lipgloss.Width(sb.String())
	if rest > 0 {
		sb.WriteString(tabBarStyle.Render(strings.Repeat(" ", rest)))
	}
	return sb.String()
}

// ---- mouse ----

// setPointer switches the terminal pointer shape ("default", "ew-resize",
// "ns-resize") via OSC 22 (kitty, foot, WezTerm, xterm ≥ 367). Terminals
// without support ignore the sequence.
func setPointer(shape string) {
	os.Stdout.WriteString("\x1b]22;" + shape + "\x1b\\")
}

func (m Model) dispatchMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	// Buttonless motion = hover: only used to swap the pointer shape over
	// the two dividers. Must never reach the drag paths below.
	if msg.Action == tea.MouseActionMotion && msg.Button == tea.MouseButtonNone {
		shape := "default"
		switch {
		case m.ovKind != overlayNone:
		case m.sidebarOpen && msg.Y > 0 && msg.X == m.side.Width:
			shape = "ew-resize"
		case m.termOpen && msg.Y == m.contentRows()+1 &&
			msg.X-m.editorX() >= m.termChipEnd(): // label/chips strip keeps the arrow
			shape = "ns-resize"
		}
		if shape != m.hoverShape {
			m.hoverShape = shape
			setPointer(shape)
		}
		return m, nil
	}
	if m.ovKind != overlayNone { // overlays are keyboard-driven; click closes
		if msg.Action == tea.MouseActionPress {
			m.ovKind = overlayNone
		}
		return m, nil
	}
	// The sidebar's bottom switcher row (above its spacer): a click on a
	// button swaps the panel.
	if m.sidebarOpen && msg.Y == m.height-3 && msg.X < m.side.Width &&
		msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		m.mouseDown = paneSidebar // no drag from a switcher click
		for i, r := range m.sideSwitcherRanges() {
			if msg.X >= r.start && msg.X < r.end {
				if i == 1 {
					m.gitView = true
					m.focus = paneGit
					m.refreshGit()
				} else {
					m.gitView = false
					m.focus = paneSidebar
				}
				break
			}
		}
		return m, nil
	}
	target := paneEditor
	switch {
	case msg.Action == tea.MouseActionMotion || msg.Action == tea.MouseActionRelease:
		target = m.mouseDown // drags stay with the pane they started in
	case msg.Y == 0:
		target = paneTabs
	case m.sidebarOpen && msg.X == m.side.Width:
		target = paneDivider
	case m.sidebarOpen && msg.X < m.editorX():
		target = paneSidebar
		if m.gitView {
			target = paneGit
		}
	case m.termOpen && msg.Y == m.contentRows()+1:
		target = panePanelDivider
	case m.termOpen && msg.Y > m.contentRows()+1:
		target = paneTerminal
	}
	if msg.Action == tea.MouseActionPress {
		m.mouseDown = target
	}

	switch target {
	case paneTerminal:
		t := m.activeTerm()
		if t == nil {
			return m, nil
		}
		switch {
		case msg.Button == tea.MouseButtonWheelUp:
			t.Scroll(+3)
		case msg.Button == tea.MouseButtonWheelDown:
			t.Scroll(-3)
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
			m.focus = paneTerminal
			m.compl.active = false
			m.hoverText = ""
		}
	case panePanelDivider:
		// A press on an instance chip or the "+" button is a click, not a
		// resize drag.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			x := msg.X - m.editorX()
			for i, r := range m.termChipRanges() {
				if x >= r.start && x < r.end {
					m.mouseDown = paneTerminal // no drag from a chip click
					if i == len(m.terms) {     // the "+" chip
						return m, m.newTerm()
					}
					m.termActive = i
					m.focus = paneTerminal
					return m, nil
				}
			}
			if x < m.termChipEnd() { // label strip: dead zone, no drag
				m.mouseDown = paneTerminal
				return m, nil
			}
		}
		if msg.Action == tea.MouseActionMotion || msg.Action == tea.MouseActionRelease {
			m.termH = clampInt(m.height-2-msg.Y, 3, max(3, m.height-8))
			m.layout()
		}
	case paneDivider:
		if msg.Action == tea.MouseActionMotion || msg.Action == tea.MouseActionRelease {
			m.sidebarW = max(12, min(msg.X, m.width/2))
			m.layout()
		}
	case paneTabs:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		for i, r := range m.tabRanges() {
			if msg.X >= r.start && msg.X < r.end {
				if msg.X >= r.closeX {
					m.active = i
					return m.closeActive(), nil
				}
				m.active = i
				m.focus = paneEditor
			}
		}
	case paneGit:
		switch {
		case msg.Button == tea.MouseButtonWheelUp:
			m.gitWheel(-3)
		case msg.Button == tea.MouseButtonWheelDown:
			m.gitWheel(3)
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
			m.focus = paneGit
			m.gitClick(msg.Y-2, msg.X) // -1 tab bar, -1 header
		}
	case paneSidebar:
		switch {
		case msg.Button == tea.MouseButtonWheelUp:
			m.side.Wheel(-3)
		case msg.Button == tea.MouseButtonWheelDown:
			m.side.Wheel(3)
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
			m.focus = paneSidebar
			if f := m.side.Click(msg.Y - 2); f != "" { // -1 tab bar, -1 header
				m.openFile(f)
			}
		}
	case paneEditor:
		if d := m.doc(); d != nil {
			if msg.Action == tea.MouseActionPress {
				m.focus = paneEditor
				m.compl.active = false
				m.hoverText = ""
			}
			msg.X -= m.editorX()
			msg.Y--
			if msg.Ctrl && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
				// ctrl+click = go to definition at the clicked cell
				d.ed, _ = d.ed.Update(tea.MouseMsg{X: msg.X, Y: msg.Y, Action: msg.Action, Button: msg.Button})
				return m, cmdDefinition(&m)
			}
			var cmd tea.Cmd
			d.ed, cmd = d.ed.Update(msg)
			return m, tea.Batch(cmd, m.syncLSP())
		}
	}
	return m, nil
}

// ---- find/replace minibar (unchanged behavior from Phase 1) ----

func (m Model) updateMinibar(k tea.KeyMsg) (Model, tea.Cmd) {
	d := m.doc()
	if d == nil {
		m.mode = modeEdit
		return m, nil
	}
	input := &m.query
	if m.mode == modeReplace {
		input = &m.repl
	}
	switch k.Type {
	case tea.KeyEscape:
		m.mode = modeEdit
		d.ed.SetSearch("", false)
		return m, nil
	case tea.KeyCtrlQ:
		return m, tea.Quit
	case tea.KeyRunes:
		*input += string(k.Runes)
	case tea.KeySpace:
		*input += " "
	case tea.KeyBackspace:
		if s := *input; len(s) > 0 {
			r := []rune(s)
			*input = string(r[:len(r)-1])
		}
	case tea.KeyCtrlT:
		m.useRegex = !m.useRegex
	case tea.KeyCtrlR:
		if m.mode == modeFind {
			m.mode = modeReplace
		} else {
			m.mode = modeFind
		}
		return m, nil
	case tea.KeyUp:
		d.ed.NextMatch(-1)
		return m, nil
	case tea.KeyDown:
		d.ed.NextMatch(+1)
		return m, nil
	case tea.KeyEnter:
		if m.mode == modeReplace {
			if k.Alt {
				n := d.ed.ReplaceAll(m.repl)
				m.lastMsg = fmt.Sprintf("replaced %d", n)
				m.mode = modeEdit
				d.ed.SetSearch("", false)
			} else {
				d.ed.ReplaceCurrent(m.repl)
			}
		} else {
			d.ed.NextMatch(+1)
		}
		return m, nil
	default:
		return m, nil
	}
	if m.mode != modeReplace {
		d.ed.SetSearch(m.query, m.useRegex)
	}
	if m.mode == modeFind {
		d.ed.NextMatch(0)
	}
	return m, nil
}

// ---- view ----

func (m Model) View() string {
	if m.height == 0 {
		return ""
	}
	var middle string
	if d := m.doc(); d != nil {
		middle = d.ed.View()
	} else {
		middle = m.welcome()
	}
	if m.termOpen && m.activeTerm() != nil {
		middle += "\n" + m.renderTermPanel()
	}
	if m.sidebarOpen {
		rows := max(1, m.height-2)
		border := strings.TrimSuffix(strings.Repeat(borderStyle.Render("│")+"\n", rows), "\n")
		side := m.side.View(m.focus == paneSidebar)
		if m.gitView {
			side = m.gitPanelView()
		}
		// switcher plus a spacer row so the buttons don't sit on the bottom bar
		side += "\n" + m.sideSwitcher() + "\n" + strings.Repeat(" ", m.side.Width)
		middle = lipgloss.JoinHorizontal(lipgloss.Top, side, border, middle)
	}
	if m.ovKind != overlayNone {
		middle = m.composite(middle, m.ov.View(), 1, -1)
	} else if d := m.doc(); d != nil && m.focus == paneEditor {
		cx, cy := d.ed.CursorScreen()
		left := m.editorX() + cx
		switch {
		case m.compl.active:
			middle = m.composite(middle, m.renderCompl(), cy+1, left)
		case m.hoverText != "":
			middle = m.composite(middle, m.renderHover(), cy+1, left)
		default:
			if diag, ok := d.ed.DiagUnderCursor(); ok {
				toast := m.renderToast(diag)
				h := lipgloss.Height(toast)
				w := lipgloss.Width(toast)
				middle = m.composite(middle, toast, max(0, m.height-2-h), max(0, m.width-w))
			}
		}
	}
	return m.renderTabBar() + "\n" + middle + "\n" + m.bottomBar()
}

// sideButtons are the panel-switcher labels at the bottom of the sidebar.
var sideButtons = [2]string{"Files", "Git"}

// sideSwitcherRanges returns each button's [start, end) x-range within the
// sidebar, matching sideSwitcher exactly so render and hit-test can't drift.
func (m Model) sideSwitcherRanges() [2]struct{ start, end int } {
	const margin = 2 // cells left and right of the control
	half := (m.side.Width - 2*margin) / 2
	return [2]struct{ start, end int }{{margin, margin + half}, {margin + half, m.side.Width - margin}}
}

// sideSwitcher renders the one-row Files/Git picker under the sidebar: a
// segmented control spanning the sidebar minus side margins, each button
// half of it, the active one highlighted — reusing the tab bar's themed
// styles.
func (m Model) sideSwitcher() string {
	ranges := m.sideSwitcherRanges()
	row := strings.Repeat(" ", ranges[0].start)
	for i, r := range ranges {
		st := tabStyle.Faint(true)
		if (i == 1) == m.gitView {
			st = tabActiveStyle
		}
		row += st.Render(centerCell(sideButtons[i], r.end-r.start))
	}
	return row + strings.Repeat(" ", max(0, m.side.Width-ranges[1].end))
}

// centerCell pads s to exactly w cells, label centered. ponytail: rune==cell
// assumption, same as the sidebar.
func centerCell(s string, w int) string {
	r := []rune(s)
	if len(r) >= w {
		return string(r[:w])
	}
	left := (w - len(r)) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-len(r)-left)
}

// composite splices over onto base starting at row top; left < 0 centers.
// ANSI-aware: base content stays visible on both sides of the box.
func (m Model) composite(base, over string, top, left int) string {
	baseLines := strings.Split(base, "\n")
	overLines := strings.Split(over, "\n")
	ow := 0
	for _, l := range overLines {
		ow = max(ow, lipgloss.Width(l))
	}
	if left < 0 {
		left = (m.width - ow) / 2
	}
	left = clampInt(left, 0, max(0, m.width-ow))
	if top+len(overLines) > len(baseLines) { // keep the box on screen
		top = max(0, len(baseLines)-len(overLines))
	}
	for i, ol := range overLines {
		row := top + i
		if row >= len(baseLines) {
			break
		}
		b := baseLines[row]
		// Square up the overlay row so the box interior is opaque.
		if pad := ow - lipgloss.Width(ol); pad > 0 {
			ol += strings.Repeat(" ", pad)
		}
		lhs := ansi.Truncate(b, left, "")
		if pad := left - lipgloss.Width(lhs); pad > 0 {
			lhs += strings.Repeat(" ", pad)
		}
		rhs := ansi.TruncateLeft(b, left+ow, "")
		baseLines[row] = lhs + "\x1b[0m" + ol + "\x1b[0m" + rhs
	}
	return strings.Join(baseLines, "\n")
}

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

func (m Model) welcome() string {
	h := m.contentRows()
	w := m.width - m.editorX()
	msg := welcomeStyle.Render("Ctrl+P — all commands   Ctrl+O — open file   Ctrl+B — file tree")
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, msg)
}

// bar renders one status-bar line hard-clipped to the terminal width, so a
// long message/path can never wrap the footer onto a second row.
func (m Model) bar(content string) string {
	return statusStyle.Width(m.width).Render(ansi.Truncate(content, m.width, "…"))
}

func (m Model) bottomBar() string {
	// While the palette is open, the footer shows the highlighted action's
	// ID — the name used for [keys] rebinding in config.toml. No nested
	// styles here: an inner reset would cut the bar's background short.
	if m.ovKind == overlayPalette {
		if i := m.ov.Selected(); i >= 0 {
			return m.bar(" id: " + m.ovActions[i].ID)
		}
	}
	switch m.mode {
	case modeFind, modeReplace:
		return m.minibar()
	case modePrompt:
		text := m.promptText
		// Keep the tail of a long input visible while typing.
		if over := lipgloss.Width(m.promptLabel) + len([]rune(text)) + 8 - m.width; over > 0 {
			r := []rune(text)
			text = "…" + string(r[min(len(r), over+1):])
		}
		left := promptStyle.Render(" "+m.promptLabel) + " " + text + "█"
		pad := max(1, m.width-lipgloss.Width(left)-5)
		return m.bar(left + fmt.Sprintf("%*s", pad, "esc  "))
	}
	d := m.doc()
	if d == nil {
		right := m.gitSeg() + "^P commands  ^O files  ^Q quit "
		return m.bar(fmt.Sprintf("%*s", m.width, right))
	}
	line, col := d.ed.Cursor()
	dirty := ""
	if d.ed.Dirty {
		dirty = " [+]"
	}
	multi := ""
	if n := d.ed.CursorCount(); n > 1 {
		multi = fmt.Sprintf("  %d cursors", n)
	}
	vimLabel := ""
	if m.vim != nil {
		vimLabel = m.vim.label() + "  "
	}
	name := d.path
	if !d.virtual { // virtual tabs carry a display title, not a real path
		name = rel(m.side.Root, d.path)
	}
	left := fmt.Sprintf(" %s%s%s  %d:%d%s", vimLabel, name, dirty, line+1, col+1, multi)
	right := fmt.Sprintf("%s%s  %dL  %s  ^P commands ", m.gitSeg(), m.lspStatusLine(d), d.ed.Buf.LineCount(), m.lastCost.Round(time.Microsecond))
	// The message slot: a transient message wins, else the blame annotation.
	msg := m.lastMsg
	if msg == "" {
		msg = m.blameSeg(d)
	}
	if msg != "" {
		space := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
		right = ansi.Truncate(msg, max(0, space), "…") + "  " + right
	}
	pad := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return m.bar(left + fmt.Sprintf("%*s", pad, "") + right)
}

func (m Model) minibar() string {
	d := m.doc()
	cur, total := d.ed.SearchInfo()
	counter := "no matches"
	if total > 0 {
		counter = fmt.Sprintf("%d/%d", cur, total)
	}
	if m.query == "" {
		counter = ""
	}
	re := ""
	if m.useRegex {
		re = " [.*]"
	}
	var left string
	if m.mode == modeFind {
		left = promptStyle.Render(" Find: ") + m.query + "█"
	} else {
		left = promptStyle.Render(" Replace ") + m.query + promptStyle.Render(" with: ") + m.repl + "█"
	}
	right := fmt.Sprintf("%s%s  ↓↑ next/prev  ^R %s  ^T regex  ⏎ go  esc ",
		counter, re, map[mode]string{modeFind: "replace", modeReplace: "find"}[m.mode])
	pad := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return m.bar(left + fmt.Sprintf("%*s", pad, "") + right)
}
