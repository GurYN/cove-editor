// Package term is the integrated terminal panel: the user's shell on a PTY,
// screen state kept by a VT100 emulator (vt10x), rendered on demand. The app
// learns about new output via Notify (listen-cmd pattern, like lsp.Manager).
package term

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"

	"github.com/GurYN/cove-editor/internal/term/vt10x"
)

// Glyph.Mode attribute bits (mirrors vt10x's unexported attr* constants).
const (
	attrReverse = 1 << iota
	attrUnderline
	attrBold
)

type Term struct {
	vt     vt10x.Terminal
	ptmx   *os.File
	proc   *os.Process
	notify chan struct{} // coalesced "output arrived"; closed when the shell exits
	closed sync.Once
	scroll int // rows scrolled back into history; 0 = live screen
}

// New starts $SHELL (fallback /bin/sh) in dir on a cols×rows PTY.
func New(dir string, cols, rows int) (*Term, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = dir
	// COLORTERM tells apps the emulator accepts 24-bit color (vt10x parses
	// 38;2/48;2 and View re-emits it) — without it chalk/ink apps drop to
	// 256 colors or none.
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	t := &Term{
		vt:     vt10x.New(vt10x.WithSize(cols, rows)),
		ptmx:   ptmx,
		proc:   cmd.Process,
		notify: make(chan struct{}, 1),
	}
	go t.pump(cmd)
	return t, nil
}

// pump copies PTY output into the emulator and pings notify. A read error
// means the shell is gone: reap it and close notify so the app can react.
func (t *Term) pump(cmd *exec.Cmd) {
	buf := make([]byte, 4096)
	for {
		n, err := t.ptmx.Read(buf)
		if n > 0 {
			t.vt.Lock()
			h0 := t.vt.HistoryLen()
			t.vt.Unlock()
			t.vt.Write(buf[:n])
			t.vt.Lock()
			// Keep a scrolled-back view anchored while output pushes
			// more lines into history.
			if t.scroll > 0 {
				t.scroll += t.vt.HistoryLen() - h0
			}
			t.vt.Unlock()
			select {
			case t.notify <- struct{}{}:
			default:
			}
		}
		if err != nil {
			cmd.Wait()
			close(t.notify)
			return
		}
	}
}

// Notify yields once per burst of output; closed when the shell exits.
func (t *Term) Notify() <-chan struct{} { return t.notify }

// Close tears the PTY down and kills the shell.
func (t *Term) Close() {
	t.closed.Do(func() {
		t.ptmx.Close()
		t.proc.Kill()
	})
}

func (t *Term) Resize(cols, rows int) {
	pty.Setsize(t.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	t.vt.Resize(cols, rows)
}

// Send encodes a key press as terminal input bytes and writes it to the PTY.
// Typing snaps the view back to the live screen.
func (t *Term) Send(k tea.KeyMsg) {
	if b := encodeKey(k); len(b) > 0 {
		t.vt.Lock()
		t.scroll = 0
		t.vt.Unlock()
		t.ptmx.Write(b)
	}
}

// Wheel handles one wheel step at cell (x, y) of the panel. Apps that asked
// for mouse reporting get a mouse code; alt-screen apps get arrow keys
// (xterm's alternate-scroll convention — how less/vim/htop scroll in real
// terminals); otherwise the view moves through Cove's scrollback.
func (t *Term) Wheel(up bool, x, y int) {
	t.vt.Lock()
	seq := wheelSeq(t.vt.ModeSet(vt10x.ModeMouseMask), t.vt.ModeSet(vt10x.ModeMouseSgr),
		t.vt.ModeSet(vt10x.ModeAltScreen), up, x, y)
	if seq != nil {
		t.scroll = 0 // the app owns the viewport; snap to live like typing does
	}
	t.vt.Unlock()
	if seq == nil {
		if up {
			t.Scroll(+3)
		} else {
			t.Scroll(-3)
		}
		return
	}
	t.ptmx.Write(seq)
}

// wheelSeq encodes a wheel step for the child app; nil means the app doesn't
// handle it (scroll the local view instead).
func wheelSeq(mouse, sgr, alt, up bool, x, y int) []byte {
	btn := 64 // wheel-up; 65 = wheel-down
	if !up {
		btn = 65
	}
	switch {
	case mouse && sgr:
		return fmt.Appendf(nil, "\x1b[<%d;%d;%dM", btn, x+1, y+1)
	case mouse: // legacy X10 encoding: 32+code, coords offset by 33, cap 255
		return []byte{0x1b, '[', 'M', byte(32 + btn), byte(33 + min(x, 222)), byte(33 + min(y, 222))}
	case alt:
		seq := "\x1b[A"
		if !up {
			seq = "\x1b[B"
		}
		return []byte(strings.Repeat(seq, 3))
	}
	return nil
}

// Scroll moves the view into scrollback: positive = older lines, negative =
// toward the live screen. Clamped to available history.
func (t *Term) Scroll(delta int) {
	t.vt.Lock()
	defer t.vt.Unlock()
	t.scroll = max(0, min(t.scroll+delta, t.vt.HistoryLen()))
}

// Scrolled reports how many rows back the view currently sits.
func (t *Term) Scrolled() int {
	t.vt.Lock()
	defer t.vt.Unlock()
	return t.scroll
}

func encodeKey(k tea.KeyMsg) []byte {
	// KeySpace is a negative sentinel in bubbletea v1 (-15), NOT byte 32 —
	// it must not fall through to the "KeyType is the byte" branch below.
	if k.Type == tea.KeySpace {
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}, Alt: k.Alt}
	}
	if k.Type == tea.KeyRunes {
		b := []byte(string(k.Runes))
		if k.Alt {
			b = append([]byte{0x1b}, b...)
		}
		return b
	}
	switch k.Type {
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	}
	// Control keys, enter, tab, esc, space, backspace: the KeyType IS the byte.
	if k.Type >= 0 && k.Type < 128 {
		return []byte{byte(k.Type)}
	}
	return nil
}

// View renders the visible window as styled rows: the live screen, shifted
// up into scrollback when the user has scrolled. When focused and live, the
// cursor cell is drawn reversed.
func (t *Term) View(focused bool) string {
	t.vt.Lock()
	defer t.vt.Unlock()
	cols, rows := t.vt.Size()
	hist := t.vt.HistoryLen()
	scroll := min(t.scroll, hist) // history may have been trimmed
	cur := t.vt.Cursor()
	showCur := focused && t.vt.CursorVisible() && scroll == 0
	var sb strings.Builder
	for y := range rows {
		last := ""
		for x := range cols {
			var g vt10x.Glyph
			// Window rows map onto history tail first, then the screen.
			if hy := hist - scroll + y; hy < hist {
				g = t.vt.HistoryCell(hy, x)
			} else {
				g = t.vt.Cell(x, hy-hist)
				if showCur && x == cur.X && hy-hist == cur.Y {
					g.Mode ^= attrReverse
				}
			}
			if s := sgr(g); s != last {
				sb.WriteString(s)
				last = s
			}
			if g.Char == 0 {
				sb.WriteRune(' ')
			} else {
				sb.WriteRune(g.Char)
			}
		}
		sb.WriteString("\x1b[0m")
		if y < rows-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// sgr builds the escape sequence for a cell's color and attributes.
// vt10x colors are palette indexes (0-255) or packed truecolor RGB
// (< 1<<24); its Default* sentinels (>= 1<<24) map to the terminal
// defaults, emitted as nothing after the leading reset.
func sgr(g vt10x.Glyph) string {
	parts := []string{"0"}
	if g.Mode&attrReverse != 0 {
		parts = append(parts, "7")
	}
	if g.Mode&attrUnderline != 0 {
		parts = append(parts, "4")
	}
	if g.Mode&attrBold != 0 {
		parts = append(parts, "1")
	}
	if p := colorPart("38", g.FG); p != "" {
		parts = append(parts, p)
	}
	if p := colorPart("48", g.BG); p != "" {
		parts = append(parts, p)
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// colorPart encodes one vt10x color as an SGR fragment.
// ponytail: truecolor goes to the host as-is; termenv-profile downgrade for
// 256-color-only hosts if anyone ever runs Cove on one.
func colorPart(base string, c vt10x.Color) string {
	switch {
	case c < 256:
		return base + ";5;" + strconv.Itoa(int(c))
	case c < 1<<24:
		return fmt.Sprintf("%s;2;%d;%d;%d", base, c>>16&0xff, c>>8&0xff, c&0xff)
	}
	return "" // DefaultFG/DefaultBG
}
