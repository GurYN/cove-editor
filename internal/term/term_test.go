package term

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/term/vt10x"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// hasOutputLine reports whether some screen row starts with want — i.e. it
// is program output, not the typed command echoing back.
func hasOutputLine(screen, want string) bool {
	for l := range strings.SplitSeq(ansiRE.ReplaceAllString(screen, ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), want) {
			return true
		}
	}
	return false
}

// TestShellRoundTrip proves the pipeline: keys → PTY → shell → emulator → View.
func TestShellRoundTrip(t *testing.T) {
	tm, err := New(t.TempDir(), 40, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer tm.Close()

	for _, r := range "echo covedone" {
		tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-tm.Notify():
			if !ok {
				t.Fatal("shell exited before producing output")
			}
			if hasOutputLine(tm.View(false), "covedone") {
				return
			}
		case <-deadline:
			t.Fatalf("echo output never arrived; screen:\n%s", tm.View(false))
		}
	}
}

func TestEncodeKey(t *testing.T) {
	cases := []struct {
		k    tea.KeyMsg
		want string
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ls")}, "ls"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Alt: true}, "\x1bf"},
		{tea.KeyMsg{Type: tea.KeyEnter}, "\r"},
		{tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f"},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03"},
		{tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{tea.KeyMsg{Type: tea.KeyEscape}, "\x1b"},
		{tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}, " "},
		{tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" "), Alt: true}, "\x1b "},
		{tea.KeyMsg{Type: tea.KeyEnter, Alt: true}, "\x1b\r"}, // Shift+Enter via terminals mapping it to "\x1b\r"
		{tea.KeyMsg{Type: tea.KeyUp, Alt: true}, "\x1b\x1b[A"},
	}
	for _, c := range cases {
		if got := string(encodeKey(c.k)); got != c.want {
			t.Errorf("encodeKey(%v) = %q, want %q", c.k, got, c.want)
		}
	}
}

// TestScrollback drives the vendored emulator directly (no PTY) so the
// window math is deterministic.
func TestScrollback(t *testing.T) {
	tm := &Term{vt: vt10x.New(vt10x.WithSize(10, 4))}
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(tm.vt, "line%d\r\n", i)
	}
	screenLines := func() []string {
		return strings.Split(ansiRE.ReplaceAllString(tm.View(false), ""), "\n")
	}
	if !strings.Contains(tm.View(false), "line20") {
		t.Fatalf("live screen should show the tail:\n%v", screenLines())
	}
	tm.Scroll(1000) // clamps to full history
	if got := strings.TrimSpace(screenLines()[0]); got != "line1" {
		t.Fatalf("scrolled to top, first row = %q, want line1", got)
	}
	if tm.Scrolled() == 0 {
		t.Fatal("Scrolled() should report offset")
	}
	tm.Scroll(-1000)
	if tm.Scrolled() != 0 || !strings.Contains(tm.View(false), "line20") {
		t.Fatal("scroll back down should return to the live screen")
	}
}

// Wheel routing: mouse-reporting apps get mouse codes, alt-screen apps get
// arrow keys (alternate scroll), plain shells scroll the local view.
func TestWheelSeq(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mouse, sgr, alt  bool
		up               bool
		want             string
	}{
		{"plain shell -> local scroll", false, false, false, true, ""},
		{"alt screen up -> arrows", false, false, true, true, "\x1b[A\x1b[A\x1b[A"},
		{"alt screen down -> arrows", false, false, true, false, "\x1b[B\x1b[B\x1b[B"},
		{"sgr mouse up", true, true, false, true, "\x1b[<64;5;3M"},
		{"sgr mouse down", true, true, true, false, "\x1b[<65;5;3M"}, // mouse wins over alt
		{"x10 mouse up", true, false, false, true, "\x1b[M\x60\x25\x23"},
	} {
		got := string(wheelSeq(tc.mouse, tc.sgr, tc.alt, tc.up, 4, 2))
		if got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The emulator must expose the modes wheel routing depends on.
func TestModeTracking(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	if vt.ModeSet(vt10x.ModeAltScreen) || vt.ModeSet(vt10x.ModeMouseMask) {
		t.Fatal("modes set on a fresh terminal")
	}
	vt.Write([]byte("\x1b[?1049h\x1b[?1002h\x1b[?1006h")) // alt screen + mouse + SGR
	if !vt.ModeSet(vt10x.ModeAltScreen) || !vt.ModeSet(vt10x.ModeMouseMask) || !vt.ModeSet(vt10x.ModeMouseSgr) {
		t.Fatal("modes not tracked after DECSET")
	}
	vt.Write([]byte("\x1b[?1049l\x1b[?1002l\x1b[?1006l"))
	if vt.ModeSet(vt10x.ModeAltScreen) || vt.ModeSet(vt10x.ModeMouseMask) || vt.ModeSet(vt10x.ModeMouseSgr) {
		t.Fatal("modes stuck after DECRST")
	}
}

// Truecolor (38;2/48;2) output must survive the emulator round-trip into the
// rendered view — full-color TUIs (Claude Code) were showing colorless.
func TestTruecolorRendering(t *testing.T) {
	tm := &Term{vt: vt10x.New(vt10x.WithSize(10, 2))}
	tm.vt.Write([]byte("\x1b[38;2;255;100;0mX\x1b[48;5;27mY"))
	v := tm.View(false)
	if !strings.Contains(v, "38;2;255;100;0") {
		t.Fatalf("truecolor fg dropped:\n%q", v)
	}
	if !strings.Contains(v, "48;5;27") {
		t.Fatalf("indexed bg dropped:\n%q", v)
	}
}
