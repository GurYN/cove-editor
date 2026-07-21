package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/editor"
)

// vimState is the opt-in Vim keymap: a thin translation layer over the
// action registry and editor ops — a bridge for Vim muscle memory, not a
// Vim clone. ponytail: no counts, no registers, no text objects, dd/yy
// are the only operator+motion pairs; grow it if dogfooding demands.
type vimState struct {
	mode      int // vim modes below
	pendingG  bool
	pendingOp rune // 'd' or 'y' awaiting a doubled key
}

const (
	vimNormal = iota
	vimInsert
	vimVisual
)

func (v *vimState) label() string {
	switch v.mode {
	case vimInsert:
		return "-- INSERT --"
	case vimVisual:
		return "-- VISUAL --"
	default:
		return "-- NORMAL --"
	}
}

// handleVim intercepts editor-bound keys. handled=false lets the normal
// dispatch (registry, typing) run.
func (m Model) handleVim(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	v := m.vim
	d := m.doc()
	if d == nil {
		return m, nil, false
	}
	e := &d.ed

	if v.mode == vimInsert {
		if k.Type == tea.KeyEscape {
			v.mode = vimNormal
			e.Collapse()
			return m, nil, true
		}
		return m, nil, false // insert mode = normal editing
	}

	extend := v.mode == vimVisual
	if k.Type == tea.KeyEscape {
		v.mode = vimNormal
		v.pendingG, v.pendingOp = false, 0
		e.Collapse()
		return m, nil, true
	}
	if k.Type == tea.KeyCtrlR {
		e.RedoStep()
		return m, nil, true
	}
	if k.Type != tea.KeyRunes || k.Alt || len(k.Runes) != 1 {
		return m, nil, false // arrows, ctrl-keys etc. keep GUI behavior
	}
	r := k.Runes[0]

	if v.pendingG {
		v.pendingG = false
		if r == 'g' {
			e.Go(0, 0)
		}
		return m, nil, true
	}
	if v.pendingOp != 0 {
		op := v.pendingOp
		v.pendingOp = 0
		if r == op { // dd / yy
			selectLine(e)
			if op == 'd' {
				e.Cut()
			} else {
				e.Copy()
				e.Collapse()
			}
		}
		return m, nil, true
	}

	switch r {
	case 'h':
		e.MoveH(-1, extend)
	case 'j':
		e.MoveV(+1, extend)
	case 'k':
		e.MoveV(-1, extend)
	case 'l':
		e.MoveH(+1, extend)
	case 'w':
		e.MoveWord(+1, extend)
	case 'b':
		e.MoveWord(-1, extend)
	case '0':
		e.LineEdge(-1, extend)
	case '$':
		e.LineEdge(+1, extend)
	case 'g':
		v.pendingG = true
	case 'G':
		e.Go(e.Buf.LineCount()-1, 0)
	case 'i':
		v.mode = vimInsert
	case 'a':
		e.MoveH(+1, false)
		v.mode = vimInsert
	case 'o':
		e.LineEdge(+1, false)
		e.InsertNewline()
		v.mode = vimInsert
	case 'O':
		e.LineEdge(-1, false)
		e.InsertText("\n")
		e.MoveV(-1, false)
		v.mode = vimInsert
	case 'x':
		if extend {
			e.Cut()
			v.mode = vimNormal
		} else {
			e.DeleteRune(+1)
		}
	case 'd':
		if extend {
			e.Cut()
			v.mode = vimNormal
		} else {
			v.pendingOp = 'd'
		}
	case 'y':
		if extend {
			e.Copy()
			e.Collapse()
			v.mode = vimNormal
		} else {
			v.pendingOp = 'y'
		}
	case 'p':
		e.Paste()
	case 'u':
		e.UndoStep()
	case 'v':
		v.mode = vimVisual
	case '/':
		if act := m.reg.ByID("find.open"); act != nil {
			return m, act.Do(&m), true
		}
	default:
		// Unbound normal-mode keys never type.
	}
	return m, m.syncLSP(), true
}

// selectLine selects the current line including its newline.
func selectLine(e *editor.Model) {
	e.LineEdge(-1, false)
	e.LineEdge(+1, true)
	e.MoveH(+1, true)
}
