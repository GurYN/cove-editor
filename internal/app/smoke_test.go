package app

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/buffer"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// sampleSrc is the fixture every setup() model edits. Tests index into it
// (diag offsets, line 5 col 4, sidebar root "tmp") — keep it multi-line Go.
const sampleSrc = `package sample

import "fmt"

// greet says hello to whoever shows up.
func greet(name string) {
	fmt.Println("hello", name)
}
`

func setup(t *testing.T) tea.Model {
	t.Helper()
	lipgloss.SetColorProfile(termenv.ANSI256)
	src := []byte(sampleSrc)
	if err := os.WriteFile("/tmp/sample.go", src, 0o644); err != nil {
		t.Fatal(err)
	}
	var m tea.Model = New("/tmp/sample.go", src)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// close sidebar so the editor sits at x=0 (tri-state: focus, then close)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	return m
}

func TestTypeIntoGoFile(t *testing.T) {
	m := setup(t)
	for _, r := range "// cove was here" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	frame := m.View()
	if !strings.Contains(frame, "cove was here") {
		t.Fatalf("typed text missing from frame:\n%.800s", frame)
	}
}

// Terminal paste / fast typing arrives as one multi-rune KeyMsg, possibly
// with control characters embedded. They must never reach the buffer raw.
func TestPasteChunkWithCR(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("// cove was here\rsecond")})
	frame := m.View()
	if !strings.Contains(frame, "cove was here") {
		t.Fatal("chunk text missing from frame")
	}
	if strings.Contains(frame, "\r") {
		t.Fatal("raw \\r leaked into the rendered frame")
	}
	if !strings.Contains(frame, "second") {
		t.Fatal("text after \\r missing")
	}
}

// Esc never arrives alone from the terminal; it fuses with the next key
// into an alt-chord. The app must unfuse: Esc semantics, then the bare key.
func TestEscUnfuse(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF}) // open find
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")})
	// Esc then 'x' arrives as alt+x: minibar must close and x be typed.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true})
	if strings.Contains(m.View(), "Find:") {
		t.Fatal("minibar still open after Esc-fused key")
	}
	if got := string(docBuf(m).Line(0)); !strings.HasPrefix(got, "x") {
		t.Fatalf("bare key after unfuse not applied: %q", got)
	}
}

// Esc then Ctrl+Q (fused as alt+ctrl+q) must open the quit confirm
// (default), and y+Enter must quit. Esc at the prompt must cancel.
func TestEscCtrlQQuits(t *testing.T) {
	m := setup(t)
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ, Alt: true})
	if cmd != nil {
		t.Fatal("quit without confirmation")
	}
	if !strings.Contains(m2.View(), "Quit Cove?") {
		t.Fatal("no quit prompt shown")
	}
	m2, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m2, cmd = m2.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if cmd != nil {
		t.Fatal("esc did not cancel the prompt")
	}
	m2, _ = m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	_, cmd = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no quit command after confirm")
	}
}

func TestPaletteListsAndRuns(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	frame := m.View()
	for _, want := range []string{"Command:", "File: Save", "ctrl+s"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("palette missing %q", want)
		}
	}
	// Fuzzy-run "Sidebar: Toggle" -> sidebar reopens.
	for _, r := range "sidebtog" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	frame = m.View()
	if strings.Contains(frame, "Command:") {
		t.Fatal("palette did not close")
	}
	if !strings.Contains(frame, "tmp") { // sidebar shows /tmp root name
		t.Fatal("sidebar not visible after palette action")
	}
}

func TestTabsOpenSwitchClose(t *testing.T) {
	os.WriteFile("/tmp/cove-two.go", []byte("package two\n"), 0o644)
	m := setup(t)
	appm := m.(Model)
	appm.openFile("/tmp/cove-two.go")
	if len(appm.docs) != 2 || appm.active != 1 {
		t.Fatalf("docs=%d active=%d", len(appm.docs), appm.active)
	}
	frame := appm.View()
	if !strings.Contains(frame, "cove-two.go") || !strings.Contains(frame, "sample.go") {
		t.Fatal("tab bar missing tabs")
	}
	m2, _ := appm.update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if len(m2.docs) != 1 {
		t.Fatalf("close failed: %d docs", len(m2.docs))
	}
}

func TestDirtyCloseNeedsConfirm(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	appm := m.(Model)
	m2, _ := appm.update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if len(m2.docs) != 1 {
		t.Fatal("dirty tab closed without confirmation")
	}
	if !strings.Contains(m2.View(), "unsaved changes") {
		t.Fatal("no confirm prompt shown")
	}
	m2, _ = m2.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m2, _ = m2.update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m2.docs) != 0 {
		t.Fatalf("confirmed close did not close: %d docs", len(m2.docs))
	}
}

func TestSidebarClickOpensFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/aaa.go", []byte("package a\n"), 0o644)
	lipgloss.SetColorProfile(termenv.ANSI256)
	var m tea.Model = New(dir, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	// First tree row is at screen y=2 (tab bar + sidebar header above).
	m, _ = m.Update(tea.MouseMsg{X: 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	appm := m.(Model)
	if len(appm.docs) != 1 || !strings.HasSuffix(appm.docs[0].path, "aaa.go") {
		t.Fatalf("click did not open file: %+v", appm.docs)
	}
}

func TestFinderOpensFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/target.go", []byte("package t\n"), 0o644)
	os.WriteFile(dir+"/other.txt", []byte("x"), 0o644)
	lipgloss.SetColorProfile(termenv.ANSI256)
	var m tea.Model = New(dir, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	for _, r := range "targ" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	appm := m.(Model)
	if len(appm.docs) != 1 || !strings.HasSuffix(appm.docs[0].path, "target.go") {
		t.Fatalf("finder did not open target.go: %+v", appm.docs)
	}
}

func TestSidebarNewFile(t *testing.T) {
	dir := t.TempDir()
	lipgloss.SetColorProfile(termenv.ANSI256)
	var m tea.Model = New(dir, nil) // dir workspace: sidebar starts focused
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	for _, r := range "fresh.go" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(dir + "/fresh.go"); err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if appm := m.(Model); len(appm.docs) != 1 {
		t.Fatal("new file not opened in a tab")
	}
}

// pump runs the bubbletea cmd/msg loop manually until cond or timeout.
func pump(t *testing.T, m tea.Model, cmd tea.Cmd, cond func(Model) bool, timeout time.Duration) tea.Model {
	t.Helper()
	msgs := make(chan tea.Msg, 16)
	launch := func(c tea.Cmd) {
		if c != nil {
			go func() { msgs <- c() }()
		}
	}
	launch(cmd)
	deadline := time.After(timeout)
	for {
		if cond(m.(Model)) {
			return m
		}
		select {
		case msg := <-msgs:
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, c := range batch {
					launch(c)
				}
				continue
			}
			var next tea.Cmd
			m, next = m.Update(msg)
			launch(next)
		case <-deadline:
			t.Fatal("pump: condition not met in time")
		}
	}
}

// TestLSPDiagnosticsAndDefinition drives the whole app against real gopls.
func TestLSPDiagnosticsAndDefinition(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	os.WriteFile(dir+"/go.mod", []byte("module example.com/x\n\ngo 1.22\n"), 0o644)
	src := "package main\n\nfunc greet() string { return \"hi\" }\n\nfunc main() {\n\tprintln(greet())\n\tvar unused int\n}\n"
	os.WriteFile(dir+"/main.go", []byte(src), 0o644)

	lipgloss.SetColorProfile(termenv.ANSI256)
	data, _ := os.ReadFile(dir + "/main.go")
	var m tea.Model = New(dir+"/main.go", data)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Diagnostics must land on the editor as spans.
	m = pump(t, m, m.(Model).Init(), func(a Model) bool {
		d := a.doc()
		return d != nil && len(d.ed.Diags) > 0
	}, 60*time.Second)
	appm := m.(Model)
	if e, _, _ := appm.doc().ed.DiagCounts(); e == 0 {
		t.Fatal("no error diagnostics")
	}

	// F12 on greet() call site jumps to the definition line.
	appm.doc().ed.Go(5, 10)
	m2, cmd := appm.update(tea.KeyMsg{Type: tea.KeyF12})
	m = pump(t, m2, cmd, func(a Model) bool {
		line, _ := a.doc().ed.Cursor()
		return line == 2
	}, 15*time.Second)
	_ = m
}

func docBuf(m tea.Model) *buffer.Buffer {
	a := m.(Model)
	return a.doc().ed.Buf
}

func TestKeyRebindFromConfig(t *testing.T) {
	p := t.TempDir() + "/config.toml"
	os.WriteFile(p, []byte("[keys]\n\"file.save\" = \"f5\"\n"), 0o644)
	t.Setenv("COVE_CONFIG", p)
	tmp := t.TempDir() + "/x.txt"
	os.WriteFile(tmp, []byte("hi\n"), 0o644)
	data, _ := os.ReadFile(tmp)
	var m tea.Model = New(tmp, data)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF5}) // rebound save
	after, _ := os.ReadFile(tmp)
	if string(after) != "!hi\n" {
		t.Fatalf("f5 did not save: %q", after)
	}
	// old binding must still work? no — ctrl+s should now be unbound for save
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	after, _ = os.ReadFile(tmp)
	if string(after) != "!hi\n" {
		t.Fatalf("ctrl+s still saves after rebind: %q", after)
	}
}

func TestVimKeymap(t *testing.T) {
	p := t.TempDir() + "/config.toml"
	os.WriteFile(p, []byte("keymap = \"vim\"\n"), 0o644)
	t.Setenv("COVE_CONFIG", p)
	tmp := t.TempDir() + "/v.txt"
	os.WriteFile(tmp, []byte("alpha\nbeta\ngamma\n"), 0o644)
	data, _ := os.ReadFile(tmp)
	var m tea.Model = New(tmp, data)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	key := func(s string) {
		for _, r := range s {
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	esc := func() { m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape}) }

	// Normal mode: typing must not insert.
	key("zz")
	if got := string(docBuf(m).Line(0)); got != "alpha" {
		t.Fatalf("normal mode typed: %q", got)
	}
	if !strings.Contains(m.View(), "-- NORMAL --") {
		t.Fatal("no NORMAL badge")
	}
	// j moves down; dd deletes the line.
	key("j")
	key("dd")
	if got := string(docBuf(m).Bytes()); got != "alpha\ngamma\n" {
		t.Fatalf("dd: %q", got)
	}
	// p pastes it back below? (we paste at cursor: gamma line start)
	key("p")
	if got := string(docBuf(m).Bytes()); got != "alpha\nbeta\ngamma\n" {
		t.Fatalf("p: %q", got)
	}
	// i enters insert; typing works; esc back to normal.
	key("i")
	if !strings.Contains(m.View(), "-- INSERT --") {
		t.Fatal("no INSERT badge")
	}
	key("X")
	esc()
	if got := string(docBuf(m).Line(2)); got != "Xgamma" {
		t.Fatalf("insert: %q", got)
	}
	// u undoes.
	key("u")
	if got := string(docBuf(m).Line(2)); got != "gamma" {
		t.Fatalf("undo: %q", got)
	}
	// visual: vjy yanks two lines... keep simple: v$y yanks to line end.
	key("gg")
	key("v")
	if !strings.Contains(m.View(), "-- VISUAL --") {
		t.Fatal("no VISUAL badge")
	}
	key("$y")
	key("j0")
	key("p")
	if got := string(docBuf(m).Line(1)); got != "alphabeta" {
		t.Fatalf("visual yank/paste: %q", got)
	}
}

func TestTerminalToggleAndType(t *testing.T) {
	m := setup(t)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ}) // open panel
	if cmd == nil {
		t.Fatal("expected listenTerm cmd on first open")
	}
	app := m.(Model)
	if !app.termOpen || app.focus != paneTerminal || len(app.terms) == 0 {
		t.Fatalf("panel not open+focused: open=%v focus=%v", app.termOpen, app.focus)
	}
	if !strings.Contains(m.View(), "Terminal") {
		t.Fatal("panel title missing from view")
	}
	// keys route to the shell, then the echo shows up on screen
	for _, r := range "echo covesmoke" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(m.View(), "covesmoke") {
		if time.Now().After(deadline) {
			t.Fatal("typed command never appeared in panel")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// editor kept its space: doc height shrank by panel size
	app = m.(Model)
	if h := app.doc().ed.Height; h != 24-2-app.panelRows() {
		t.Fatalf("editor height %d not reduced by panel", h)
	}
	// Alt+enter ("\x1b\r" fused by bubbletea — e.g. Shift+Enter mapped to
	// that sequence for Claude Code) must reach the shell as a chord, not
	// unfuse into Esc + Enter or toggle the panel.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	app = m.(Model)
	if !app.termOpen || app.focus != paneTerminal {
		t.Fatal("alt+enter must stay in the terminal panel")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ}) // hide
	app = m.(Model)
	if app.termOpen || app.focus != paneEditor {
		t.Fatal("panel should hide and return focus to editor")
	}
	for _, tt := range app.terms {
		tt.Close()
	}
}

func TestSidebarSwitcherClick(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlB}) // reopen sidebar
	app := m.(Model)
	if !app.sidebarOpen || app.gitView {
		t.Fatal("expected open sidebar showing the file tree")
	}
	y := app.height - 3 // switcher row
	gitX := app.sideSwitcherRanges()[1].start
	m, _ = m.Update(tea.MouseMsg{X: gitX, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	app = m.(Model)
	if !app.gitView || app.focus != paneGit {
		t.Fatalf("git button: gitView=%v focus=%v, want git panel focused", app.gitView, app.focus)
	}
	filesX := app.sideSwitcherRanges()[0].start
	m, _ = m.Update(tea.MouseMsg{X: filesX, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	app = m.(Model)
	if app.gitView || app.focus != paneSidebar {
		t.Fatalf("files button: gitView=%v focus=%v, want file tree focused", app.gitView, app.focus)
	}
}

func TestPanelDividerDrag(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	app := m.(Model)
	titleY := app.contentRows() + 1
	x := app.editorX() + app.termChipEnd() + 2 // grab the border, not the label/chips
	m, _ = m.Update(tea.MouseMsg{X: x, Y: titleY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m, _ = m.Update(tea.MouseMsg{X: x, Y: titleY + 3, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m, _ = m.Update(tea.MouseMsg{X: x, Y: titleY + 3, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	app = m.(Model)
	if app.termH != termDefaultRows-3 {
		t.Fatalf("drag down 3 rows: termH = %d, want %d", app.termH, termDefaultRows-3)
	}
	// sidebar stays full height; only the editor shrinks for the panel
	if app.side.Height != 24-4 {
		t.Fatalf("sidebar height %d, want full %d", app.side.Height, 24-4)
	}
	for _, tt := range app.terms {
		tt.Close()
	}
}

func TestTerminalPlusButtonAndChips(t *testing.T) {
	m := setup(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	app := m.(Model)
	titleY := app.contentRows() + 1
	ranges := app.termChipRanges()
	plus := ranges[len(ranges)-1] // last chip is "+"
	m, _ = m.Update(tea.MouseMsg{X: app.editorX() + plus.start, Y: titleY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	app = m.(Model)
	if len(app.terms) != 2 || app.termActive != 1 {
		t.Fatalf("+ click: terms=%d active=%d, want 2/1", len(app.terms), app.termActive)
	}
	// click chip 1 switches back
	first := app.termChipRanges()[0]
	m, _ = m.Update(tea.MouseMsg{X: app.editorX() + first.start, Y: titleY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	app = m.(Model)
	if app.termActive != 0 || app.focus != paneTerminal {
		t.Fatalf("chip click: active=%d focus=%v, want 0/terminal", app.termActive, app.focus)
	}
	// hover over the chips strip keeps the arrow pointer; past it, ns-resize
	m, _ = m.Update(tea.MouseMsg{X: app.editorX() + first.start, Y: titleY, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if got := m.(Model).hoverShape; got != "default" {
		t.Fatalf("pointer over chips = %q, want default", got)
	}
	m, _ = m.Update(tea.MouseMsg{X: app.editorX() + app.termChipEnd() + 2, Y: titleY, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	if got := m.(Model).hoverShape; got != "ns-resize" {
		t.Fatalf("pointer over border = %q, want ns-resize", got)
	}
	// a chip click must not start a resize drag
	m, _ = m.Update(tea.MouseMsg{X: app.editorX() + first.start, Y: titleY + 3, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if got := m.(Model).termH; got != termDefaultRows {
		t.Fatalf("chip click dragged the divider: termH=%d", got)
	}
	for _, tt := range m.(Model).terms {
		tt.Close()
	}
}
