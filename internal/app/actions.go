package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/action"
	"github.com/GurYN/cove-editor/internal/config"
	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/git"
)

// sampleConfig is written on first "Open Settings" — config discoverability.
const sampleConfig = `# Cove configuration. Changes apply on restart.

# theme = "cove-dark"        # or "cove-light"
# keymap = "default"          # "vim" for the opt-in Vim keymap

# [editor]
# tab_size = 4
# line_numbers = true
# confirm_quit = true        # false: ctrl+q quits without asking

# Hide files from the file tree (globs on the base name). Hidden files stay
# visible in the git panel so they can't sneak into a commit unseen.
# [files]
# hidden = [".DS_Store", "*.pyc", "node_modules"]

# Rebind any action by its ID — the value is the NEW key, replacing the
# default. Open the command palette (ctrl+p) and highlight an action: its
# ID appears in the footer.
# [keys]
# "file.save" = "ctrl+s"
# "lsp.rename" = "f4"   # moves rename off its default f2

# Override theme colors (hex or ANSI-256).
# [colors]
# keyword = "#c586c0"

# Register or override language servers.
# [lsp.zig]
# command = ["zls"]
# extensions = [".zig"]
# language_id = "zig"       # LSP languageId sent to the server (defaults to the key)

# Favorite TUI apps: each gets a palette entry ("App: redis") that runs it
# in the terminal panel. Re-invoking focuses the running instance.
# [apps.redis]
# command = ["redis-tui"]
# key = "ctrl+alt+r"        # optional; rebindable via [keys] "app.redis"
`

// newRegistry declares every user action: ID, palette title, default key,
// context. This is the single source of truth the palette, the key
// dispatcher, and (Phase 4) TOML rebinding read from.
func newRegistry() *action.Registry {
	r := action.NewRegistry()
	reg := func(id, title, key string, when action.Context, do func(*Model) tea.Cmd) {
		r.Register(action.Action{ID: id, Title: title, Key: key, When: when,
			Do: func(app any) tea.Cmd { return do(app.(*Model)) }})
	}
	// hidden: bound keys that don't belong in the palette (plain movement)
	hid := func(id, key string, when action.Context, do func(*Model) tea.Cmd) {
		r.Register(action.Action{ID: id, Key: key, When: when, Hidden: true,
			Do: func(app any) tea.Cmd { return do(app.(*Model)) }})
	}
	// ed wraps an editor operation on the active document.
	ed := func(f func(*editor.Model)) func(*Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			if d := m.doc(); d != nil {
				f(&d.ed)
			}
			return nil
		}
	}

	// ---- global ----
	quit := func(m *Model) tea.Cmd {
		m.saveSession()
		m.lspm.Shutdown()
		for _, t := range m.terms {
			t.Close()
		}
		return tea.Quit
	}
	reg("app.quit", "Quit", "ctrl+q", action.Global, func(m *Model) tea.Cmd {
		if !m.confirmQuit {
			return quit(m)
		}
		label := "Quit Cove? y/n:"
		n := 0
		for _, d := range m.docs {
			if !d.virtual && d.ed.Dirty {
				n++
			}
		}
		if n > 0 {
			label = fmt.Sprintf("Quit Cove? %d file(s) with unsaved changes — y/n:", n)
		}
		*m = m.prompt(label, "", func(m *Model, text string) {
			if strings.EqualFold(text, "y") {
				m.deferred = quit(m)
			}
		})
		return nil
	})
	reg("app.about", "About Cove", "", action.Global, func(m *Model) tea.Cmd { m.aboutOpen = true; return nil })
	reg("app.palette", "Command Palette", "ctrl+p", action.Global, func(m *Model) tea.Cmd { *m = m.openPalette(); return nil })
	hid("app.palette.f1", "f1", action.Global, func(m *Model) tea.Cmd { *m = m.openPalette(); return nil })
	reg("file.open", "Go to File…", "ctrl+o", action.Global, func(m *Model) tea.Cmd { *m = m.openFinder(); return nil })
	reg("file.save", "File: Save", "ctrl+s", action.Global, func(m *Model) tea.Cmd {
		if d := m.doc(); d != nil {
			m.lastMsg = d.save()
			m.lspm.Save(d.path)
			if len(m.git.repos) > 0 {
				m.refreshGit() // keep the panel/branch segment honest
			}
		}
		return nil
	})
	reg("file.saveAll", "File: Save All", "", action.Global, func(m *Model) tea.Cmd {
		n, fail := 0, ""
		for _, d := range m.docs {
			if d.virtual || !d.ed.Dirty {
				continue
			}
			if s := d.save(); s != "saved" {
				fail = filepath.Base(d.path) + ": " + s
				continue
			}
			m.lspm.Save(d.path)
			n++
		}
		m.lastMsg = fmt.Sprintf("saved %d file(s)", n)
		if fail != "" {
			m.lastMsg = fail
		}
		if n > 0 && len(m.git.repos) > 0 {
			m.refreshGit()
		}
		return nil
	})
	reg("tab.close", "Tab: Close", "ctrl+w", action.Global, func(m *Model) tea.Cmd { *m = m.closeActive(); return nil })
	reg("tab.next", "Tab: Next", "ctrl+pgdown", action.Global, func(m *Model) tea.Cmd {
		if len(m.docs) > 0 {
			m.active = (m.active + 1) % len(m.docs)
		}
		return nil
	})
	reg("tab.prev", "Tab: Previous", "ctrl+pgup", action.Global, func(m *Model) tea.Cmd {
		if len(m.docs) > 0 {
			m.active = (m.active + len(m.docs) - 1) % len(m.docs)
		}
		return nil
	})
	reg("sidebar.toggle", "Sidebar: Toggle", "ctrl+b", action.Global, func(m *Model) tea.Cmd {
		// Same tri-state as git.toggle: closed (or showing git) → show tree
		// and focus it; open but unfocused → focus; focused → close.
		switch {
		case m.git.view: // ctrl+b always means the file tree
			m.git.view = false
			m.sidebarOpen = true
			m.focus = paneSidebar
		case m.sidebarOpen && m.focus == paneSidebar:
			m.sidebarOpen = false
			m.focus = paneEditor
		default:
			m.sidebarOpen = true
			m.focus = paneSidebar
		}
		return nil
	})
	reg("term.toggle", "Terminal: Toggle", "ctrl+j", action.Global, func(m *Model) tea.Cmd { return m.toggleTerm() })
	reg("term.new", "Terminal: New Instance", "", action.Global, func(m *Model) tea.Cmd { return m.newTerm() })

	// ---- split panes ----
	reg("pane.split", "Pane: Split Right", "ctrl+\\", action.Global, func(m *Model) tea.Cmd { m.splitOpen(); return nil })
	reg("pane.close", "Pane: Close Split", "", action.Global, func(m *Model) tea.Cmd { m.split = false; return nil })
	reg("pane.other", "Pane: Focus Other", "", action.Global, func(m *Model) tea.Cmd {
		if m.split {
			m.focusPane(!m.splitRight)
		}
		return nil
	})
	// Ctrl+Tab is eaten by terminal emulators (their own tab switching), so
	// F6/Shift+F6 like VSCode's Focus Next/Previous Part. bubbletea v1 decodes
	// xterm's Shift+F6 (CSI 17;2~) as f18.
	reg("focus.next", "Focus: Next Panel", "f6", action.Global, func(m *Model) tea.Cmd { return m.cycleFocus(+1) })
	reg("focus.prev", "Focus: Previous Panel", "f18", action.Global, func(m *Model) tea.Cmd { return m.cycleFocus(-1) })

	// ---- git (registered in the Git context so the palette shows the
	// panel's single-letter keys, which only fire with the panel focused;
	// the palette itself runs Do directly, so they stay runnable anywhere) ----
	reg("git.toggle", "Git: Toggle Panel", "ctrl+g", action.Global, func(m *Model) tea.Cmd { m.toggleGit(); return nil })
	// Refresh reads local status immediately, then fetches in the background
	// so the ±ahead/behind counts track the actual remote, not the last pull.
	reg("git.refresh", "Git: Refresh Status", "r", action.Git, func(m *Model) tea.Cmd {
		m.refreshGit()
		r := m.curRepo()
		if r == nil || r.snap.Upstream == "" {
			return nil // nothing to fetch from (no remote / unpublished branch)
		}
		return m.gitOpRepo(r, "fetch")
	})
	reg("git.commit", "Git: Commit Staged…", "c", action.Git, func(m *Model) tea.Cmd { return m.gitCommitPrompt() })
	reg("git.undoCommit", "Git: Undo Last Commit (Keep Changes Staged)", "z", action.Git, func(m *Model) tea.Cmd { return m.gitUndoCommitPrompt() })
	reg("git.amend", "Git: Amend Last Commit…", "m", action.Git, func(m *Model) tea.Cmd { return m.gitAmendPrompt() })
	reg("git.pushForce", "Git: Push — Force With Lease", "", action.Global, func(m *Model) tea.Cmd { return m.gitOp("push force") })
	reg("git.push", "Git: Push", "", action.Global, func(m *Model) tea.Cmd { return m.gitOp("push") })
	reg("git.pull", "Git: Pull", "", action.Global, func(m *Model) tea.Cmd { return m.gitOp("pull") })
	reg("git.fetch", "Git: Fetch", "f", action.Git, func(m *Model) tea.Cmd { return m.gitOp("fetch") })
	reg("git.log", "Git: History…", "l", action.Git, func(m *Model) tea.Cmd {
		return m.withRepo(func(m *Model, r *repoState) tea.Cmd { m.openHistoryPicker(r); return nil })
	})
	reg("git.graph", "Git: Commit Graph", "g", action.Git, func(m *Model) tea.Cmd { return m.gitOpenGraph() })
	reg("git.branch", "Git: Switch Branch…", "b", action.Git, func(m *Model) tea.Cmd {
		return m.withRepo(func(m *Model, r *repoState) tea.Cmd {
			return m.gitFetchThen(r, func(m *Model) { m.openBranchPicker(r) })
		})
	})
	reg("git.sync", "Git: Sync Branch — Rebase Onto…", "s", action.Git, func(m *Model) tea.Cmd {
		return m.withRepo(func(m *Model, r *repoState) tea.Cmd {
			return m.gitFetchThen(r, func(m *Model) { m.openSyncPicker(r) })
		})
	})
	reg("git.stash", "Git: Stash All Changes", "", action.Global, func(m *Model) tea.Cmd { return m.gitOp("stash") })
	reg("git.stashFile", "Git: Stash Selected File", "h", action.Git, func(m *Model) tea.Cmd { return m.gitStashFile() })
	reg("git.stashPop", "Git: Stash Pop (Restore)", "p", action.Git, func(m *Model) tea.Cmd { return m.gitOp("stash pop") })
	reg("git.branchNew", "Git: New Branch…", "", action.Global, func(m *Model) tea.Cmd { return m.gitBranchPrompt() })
	reg("git.restore", "Git: Discard File Changes (Restore)", "x", action.Git, func(m *Model) tea.Cmd { m.gitRestorePrompt(); return nil })
	reg("git.resolveOurs", "Git: Resolve Conflict — Keep Ours (Whole File)", "o", action.Git, func(m *Model) tea.Cmd { m.gitResolveSide(false); return nil })
	reg("git.resolveTheirs", "Git: Resolve Conflict — Keep Theirs (Whole File)", "t", action.Git, func(m *Model) tea.Cmd { m.gitResolveSide(true); return nil })
	// Per-block resolution in the editor, on the conflict under the cursor.
	reg("merge.ours", "Merge: Accept Ours (Conflict at Cursor)", "", action.Editor, func(m *Model) tea.Cmd { m.mergeAccept("ours"); return nil })
	reg("merge.theirs", "Merge: Accept Theirs (Conflict at Cursor)", "", action.Editor, func(m *Model) tea.Cmd { m.mergeAccept("theirs"); return nil })
	reg("merge.both", "Merge: Accept Both (Conflict at Cursor)", "", action.Editor, func(m *Model) tea.Cmd { m.mergeAccept("both"); return nil })
	reg("merge.next", "Merge: Next Conflict", "", action.Editor, func(m *Model) tea.Cmd { m.mergeNext(); return nil })
	reg("git.blame", "Git: Toggle Inline Blame", "", action.Global, func(m *Model) tea.Cmd {
		m.git.blameOn = !m.git.blameOn
		// No "blame on" message: lastMsg and the blame annotation share the
		// status-bar slot, so it would mask the annotation it announces.
		if !m.git.blameOn {
			m.lastMsg = "blame off"
		}
		return nil // the fetch is scheduled by the Update choke point
	})
	// "All" is per repo, never across repos: the target is the section under
	// the panel cursor (or the active file's repo), and the toast names it.
	reg("git.stageAll", "Git: Stage All", "a", action.Git, func(m *Model) tea.Cmd {
		return m.withRepo(func(m *Model, r *repoState) tea.Cmd {
			if err := git.StageAll(r.top); err != nil {
				m.lastMsg = err.Error()
			} else {
				m.lastMsg = m.repoMsg(r, "staged all changes")
			}
			m.refreshGit()
			return nil
		})
	})
	reg("git.unstageAll", "Git: Unstage All", "u", action.Git, func(m *Model) tea.Cmd {
		return m.withRepo(func(m *Model, r *repoState) tea.Cmd {
			if err := git.UnstageAll(r.top); err != nil {
				m.lastMsg = err.Error()
			} else {
				m.lastMsg = m.repoMsg(r, "unstaged all")
			}
			m.refreshGit()
			return nil
		})
	})
	ghid := func(id, key string, do func(*Model) tea.Cmd) { hid(id, key, action.Git, do) }
	ghid("git.up", "up", func(m *Model) tea.Cmd { m.git.move(-1, m.gitHeight()); return nil })
	ghid("git.down", "down", func(m *Model) tea.Cmd { m.git.move(+1, m.gitHeight()); return nil })
	ghid("git.stage", " ", func(m *Model) tea.Cmd { m.gitStageToggle(); return nil })
	ghid("git.open", "enter", func(m *Model) tea.Cmd {
		if r, ok := m.git.selected(); ok {
			m.gitOpenDiff(r)
		}
		return nil
	})
	ghid("git.focusEditor", "esc", func(m *Model) tea.Cmd {
		if len(m.docs) > 0 {
			m.focus = paneEditor
		}
		return nil
	})

	// ---- navigation history ----
	reg("nav.back", "Go Back (Jump List)", "alt+left", action.Global, func(m *Model) tea.Cmd { m.navBack(); return nil })
	reg("nav.forward", "Go Forward (Jump List)", "alt+right", action.Global, func(m *Model) tea.Cmd { m.navForward(); return nil })

	// ---- project-wide search ----
	reg("search.project", "Search in Project…", "f7", action.Global, func(m *Model) tea.Cmd {
		initial := ""
		if d := m.doc(); d != nil {
			if sel := d.ed.Selection(); len(sel) > 0 && !bytes.Contains(sel, []byte("\n")) {
				initial = string(sel)
			}
		}
		*m = m.prompt("Search project:", initial, func(m *Model, q string) {
			if strings.TrimSpace(q) != "" {
				m.lastMsg = "searching…"
				m.deferred = m.projectSearchCmd(q)
			}
		})
		return nil
	})
	reg("search.replaceProject", "Replace in Project…", "", action.Global, func(m *Model) tea.Cmd {
		return m.replaceProjectPrompt()
	})

	// ---- editor: search ----
	reg("find.open", "Find", "ctrl+f", action.Editor, func(m *Model) tea.Cmd {
		d := m.doc()
		if d == nil {
			return nil
		}
		m.mode = modeFind
		if sel := d.ed.Selection(); len(sel) > 0 && !bytes.Contains(sel, []byte("\n")) {
			m.query = string(sel)
		}
		m.miniCur = len([]rune(m.query))
		d.ed.SetSearch(m.query, m.useRegex)
		return nil
	})
	reg("find.replace", "Find and Replace", "ctrl+r", action.Editor, func(m *Model) tea.Cmd {
		if m.doc() != nil {
			m.mode = modeReplace
			m.miniCur = len([]rune(m.repl))
		}
		return nil
	})

	// ---- editor: editing ----
	reg("edit.undo", "Edit: Undo", "ctrl+z", action.Editor, ed(func(e *editor.Model) { e.UndoStep() }))
	reg("edit.redo", "Edit: Redo", "ctrl+y", action.Editor, ed(func(e *editor.Model) { e.RedoStep() }))
	reg("edit.copy", "Edit: Copy", "ctrl+c", action.Editor, ed(func(e *editor.Model) { e.Copy() }))
	reg("edit.cut", "Edit: Cut", "ctrl+x", action.Editor, ed(func(e *editor.Model) { e.Cut() }))
	reg("edit.paste", "Edit: Paste", "ctrl+v", action.Editor, ed(func(e *editor.Model) { e.Paste() }))
	reg("edit.selectAll", "Selection: Select All", "ctrl+a", action.Editor, ed(func(e *editor.Model) { e.SelectAll() }))
	reg("edit.selectNext", "Selection: Add Next Occurrence", "ctrl+d", action.Editor, ed(func(e *editor.Model) { e.SelectNext() }))
	reg("edit.expand", "Selection: Expand to Syntax Node", "ctrl+e", action.Editor, ed(func(e *editor.Model) { e.ExpandSelection() }))
	reg("edit.selectAllOccurrences", "Selection: Select All Occurrences", "", action.Editor, ed(func(e *editor.Model) { e.SelectAllOccurrences() }))
	reg("edit.indent", "Edit: Indent Line", "", action.Editor, ed(func(e *editor.Model) { e.IndentLines(+1) }))
	reg("edit.outdent", "Edit: Outdent Line", "shift+tab", action.Editor, ed(func(e *editor.Model) { e.IndentLines(-1) }))
	// ctrl+_ is the byte terminals send for Ctrl+/ — the VSCode comment key.
	reg("edit.toggleComment", "Edit: Toggle Line Comment", "ctrl+_", action.Editor, func(m *Model) tea.Cmd {
		if d := m.doc(); d != nil {
			d.ed.ToggleComment(commentPrefix(d.path))
		}
		return nil
	})
	reg("edit.duplicateLine", "Edit: Duplicate Line", "", action.Editor, ed(func(e *editor.Model) { e.DuplicateLine() }))
	reg("edit.deleteLine", "Edit: Delete Line", "", action.Editor, ed(func(e *editor.Model) { e.DeleteLine() }))
	reg("edit.moveLineUp", "Edit: Move Line Up", "alt+shift+up", action.Editor, ed(func(e *editor.Model) { e.MoveLine(-1) }))
	reg("edit.moveLineDown", "Edit: Move Line Down", "alt+shift+down", action.Editor, ed(func(e *editor.Model) { e.MoveLine(+1) }))
	reg("cursor.addAbove", "Cursor: Add Above", "alt+up", action.Editor, ed(func(e *editor.Model) { e.AddCursor(-1) }))
	reg("cursor.addBelow", "Cursor: Add Below", "alt+down", action.Editor, ed(func(e *editor.Model) { e.AddCursor(+1) }))
	reg("edit.collapse", "Selection: Collapse to Single Cursor", "esc", action.Editor, ed(func(e *editor.Model) { e.Collapse() }))

	// ---- editor: movement (hidden from palette, still rebindable) ----
	type mv struct {
		id, key string
		do      func(*editor.Model)
	}
	for _, v := range []mv{
		{"cursor.left", "left", func(e *editor.Model) { e.MoveH(-1, false) }},
		{"cursor.right", "right", func(e *editor.Model) { e.MoveH(+1, false) }},
		{"cursor.up", "up", func(e *editor.Model) { e.MoveV(-1, false) }},
		{"cursor.down", "down", func(e *editor.Model) { e.MoveV(+1, false) }},
		{"select.left", "shift+left", func(e *editor.Model) { e.MoveH(-1, true) }},
		{"select.right", "shift+right", func(e *editor.Model) { e.MoveH(+1, true) }},
		{"select.up", "shift+up", func(e *editor.Model) { e.MoveV(-1, true) }},
		{"select.down", "shift+down", func(e *editor.Model) { e.MoveV(+1, true) }},
		{"cursor.wordLeft", "ctrl+left", func(e *editor.Model) { e.MoveWord(-1, false) }},
		{"cursor.wordRight", "ctrl+right", func(e *editor.Model) { e.MoveWord(+1, false) }},
		{"select.wordLeft", "ctrl+shift+left", func(e *editor.Model) { e.MoveWord(-1, true) }},
		{"select.wordRight", "ctrl+shift+right", func(e *editor.Model) { e.MoveWord(+1, true) }},
		{"cursor.home", "home", func(e *editor.Model) { e.LineEdge(-1, false) }},
		{"cursor.end", "end", func(e *editor.Model) { e.LineEdge(+1, false) }},
		{"select.home", "shift+home", func(e *editor.Model) { e.LineEdge(-1, true) }},
		{"select.end", "shift+end", func(e *editor.Model) { e.LineEdge(+1, true) }},
		{"cursor.pgup", "pgup", func(e *editor.Model) { e.Page(-1) }},
		{"cursor.pgdown", "pgdown", func(e *editor.Model) { e.Page(+1) }},
		{"delete.back", "backspace", func(e *editor.Model) { e.DeleteRune(-1) }},
		{"delete.fwd", "delete", func(e *editor.Model) { e.DeleteRune(+1) }},
	} {
		hid(v.id, v.key, action.Editor, ed(v.do))
	}
	reg("cursor.docStart", "Go to Beginning of File", "ctrl+home", action.Editor, ed(func(e *editor.Model) { e.Go(0, 0) }))
	reg("cursor.docEnd", "Go to End of File", "ctrl+end", action.Editor, ed(func(e *editor.Model) { e.Go(e.Buf.LineCount()-1, 0) }))
	reg("cursor.gotoLine", "Go to Line…", "ctrl+l", action.Editor, func(m *Model) tea.Cmd {
		if m.doc() == nil {
			return nil
		}
		*m = m.prompt("Go to line:", "", func(m *Model, text string) {
			n, err := strconv.Atoi(strings.TrimSpace(text))
			if err != nil || n < 1 {
				return
			}
			if d := m.doc(); d != nil {
				m.pushJump()
				d.ed.Go(n-1, 0)
				d.ed.Center()
			}
		})
		return nil
	})

	reg("view.lineNumbers", "View: Toggle Line Numbers", "", action.Global, func(m *Model) tea.Cmd {
		editor.SetLineNumbers(!editor.LineNumbersEnabled())
		return nil
	})
	reg("app.settings", "Open Settings (config.toml)", "", action.Global, func(m *Model) tea.Cmd {
		p := config.Path()
		if _, err := os.Stat(p); os.IsNotExist(err) {
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(sampleConfig), 0o644)
		}
		m.openFile(p)
		return nil
	})

	// ---- language intelligence ----
	reg("lsp.definition", "Go to Definition", "f12", action.Editor, func(m *Model) tea.Cmd { return cmdDefinition(m) })
	reg("lsp.references", "Find All References", "shift+f12", action.Editor, func(m *Model) tea.Cmd { return cmdReferences(m) })
	reg("lsp.hover", "Show Documentation (Hover)", "ctrl+k", action.Editor, func(m *Model) tea.Cmd { return cmdHover(m) })
	reg("lsp.complete", "Trigger Completion", "ctrl+@", action.Editor, func(m *Model) tea.Cmd { return cmdCompletion(m) })
	reg("lsp.format", "Format Document", "", action.Editor, func(m *Model) tea.Cmd { return cmdFormat(m) })
	reg("lsp.symbols", "Go to Symbol in File (Outline)", "ctrl+t", action.Editor, func(m *Model) tea.Cmd { return cmdSymbols(m) })
	reg("lsp.workspaceSymbols", "Go to Symbol in Project…", "f3", action.Editor, func(m *Model) tea.Cmd {
		if m.doc() == nil {
			return nil
		}
		*m = m.prompt("Symbol search:", "", func(m *Model, q string) {
			if strings.TrimSpace(q) != "" {
				m.deferred = cmdWorkspaceSymbols(m, q)
			}
		})
		return nil
	})
	reg("lsp.codeAction", "Quick Fix / Code Action", "alt+enter", action.Editor, func(m *Model) tea.Cmd { return cmdCodeActions(m) })
	reg("lsp.problems", "Problems: List Errors and Warnings", "f8", action.Global, func(m *Model) tea.Cmd { *m = m.openProblems(); return nil })
	reg("lsp.rename", "Rename Symbol", "f2", action.Editor, func(m *Model) tea.Cmd {
		d := m.doc()
		if d == nil {
			return nil
		}
		initial := ""
		if sel := d.ed.Selection(); len(sel) > 0 && !bytes.Contains(sel, []byte("\n")) {
			initial = string(sel)
		}
		*m = m.prompt("Rename symbol to:", initial, func(m *Model, name string) {
			if name != "" {
				m.deferred = cmdRename(m, name)
			}
		})
		return nil
	})

	// ---- sidebar ----
	shid := func(id, key string, do func(*Model) tea.Cmd) { hid(id, key, action.Sidebar, do) }
	shid("tree.up", "up", func(m *Model) tea.Cmd { m.side.Move(-1); return nil })
	shid("tree.down", "down", func(m *Model) tea.Cmd { m.side.Move(+1); return nil })
	shid("tree.collapse", "left", func(m *Model) tea.Cmd { m.side.Collapse(); return nil })
	shid("tree.expand", "right", func(m *Model) tea.Cmd { m.side.Expand(); return nil })
	shid("tree.open", "enter", func(m *Model) tea.Cmd {
		if f := m.side.Toggle(); f != "" {
			m.openFile(f)
		}
		return nil
	})
	shid("tree.focusEditor", "esc", func(m *Model) tea.Cmd {
		if len(m.docs) > 0 {
			m.focus = paneEditor
		}
		return nil
	})
	reg("tree.newFile", "File Tree: New File", "n", action.Sidebar, func(m *Model) tea.Cmd {
		dir := m.side.SelectedDir()
		*m = m.prompt("New file in "+rel(m.side.Root, dir)+":", "", func(m *Model, name string) {
			if name == "" {
				return
			}
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				m.lastMsg = err.Error()
				return
			}
			m.side.Refresh()
			m.openFile(path)
		})
		return nil
	})
	reg("tree.newDir", "File Tree: New Folder", "N", action.Sidebar, func(m *Model) tea.Cmd {
		dir := m.side.SelectedDir()
		*m = m.prompt("New folder in "+rel(m.side.Root, dir)+":", "", func(m *Model, name string) {
			if name == "" {
				return
			}
			if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
				m.lastMsg = err.Error()
			}
			m.side.Refresh()
		})
		return nil
	})
	reg("tree.rename", "File Tree: Rename", "r", action.Sidebar, func(m *Model) tea.Cmd {
		path, _, ok := m.side.Selected()
		if !ok {
			return nil
		}
		*m = m.prompt("Rename to:", filepath.Base(path), func(m *Model, name string) {
			if name == "" || name == filepath.Base(path) {
				return
			}
			newPath := filepath.Join(filepath.Dir(path), name)
			if err := os.Rename(path, newPath); err != nil {
				m.lastMsg = err.Error()
				return
			}
			for _, d := range m.docs {
				if same(d.path, path) {
					d.path = newPath
				}
			}
			m.side.Refresh()
		})
		return nil
	})
	reg("tree.delete", "File Tree: Delete", "x", action.Sidebar, func(m *Model) tea.Cmd {
		path, isDir, ok := m.side.Selected()
		if !ok {
			return nil
		}
		what := "file"
		if isDir {
			what = "folder"
		}
		*m = m.prompt(fmt.Sprintf("Delete %s %s? y/n:", what, rel(m.side.Root, path)), "", func(m *Model, text string) {
			if !strings.EqualFold(text, "y") {
				return
			}
			if err := os.RemoveAll(path); err != nil {
				m.lastMsg = err.Error()
				return
			}
			for i, d := range slices.Backward(m.docs) {
				if same(d.path, path) {
					m.active = i
					m.forceClose()
				}
			}
			m.side.Refresh()
		})
		return nil
	})
	return r
}

// commentPrefix is the line-comment token for a file, by extension. Empty
// means no line comments (toggle is a no-op).
// ponytail: static map; move into the syntax langs registry if it outgrows this.
func commentPrefix(path string) string {
	switch filepath.Base(path) {
	case "Dockerfile", "Makefile":
		return "#"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py", ".sh", ".bash", ".zsh", ".rb", ".pl", ".toml", ".yaml", ".yml", ".conf", ".ini", ".mk", ".dockerfile":
		return "#"
	case ".lua", ".sql":
		return "--"
	case ".html", ".htm", ".css", ".md", ".txt", ".json", "":
		return ""
	default:
		return "//"
	}
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil && r != "." {
		return r
	}
	return filepath.Base(root)
}

func same(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	if aa == bb {
		return true
	}
	// Symlinked prefixes (macOS /tmp → /private/tmp, symlinked checkouts)
	// make one file reachable by two absolute paths — compare resolved.
	ra, errA := filepath.EvalSymlinks(aa)
	rb, errB := filepath.EvalSymlinks(bb)
	return errA == nil && errB == nil && ra == rb
}
