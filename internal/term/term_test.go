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
	for _, l := range strings.Split(ansiRE.ReplaceAllString(screen, ""), "\n") {
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
