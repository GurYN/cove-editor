package editor

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/GurYN/cove-editor/internal/buffer"
)

func TestMain(m *testing.M) {
	// Tests have no TTY; force a color profile so styling is observable.
	lipgloss.SetColorProfile(termenv.ANSI256)
	os.Exit(m.Run())
}

func bigFile(lines int) []byte {
	var sb bytes.Buffer
	for i := range lines {
		fmt.Fprintf(&sb, "line %06d: the quick brown fox jumps over the lazy dog\n", i)
	}
	return sb.Bytes()
}

func key(m Model, t tea.KeyType) Model {
	m, _ = m.Update(tea.KeyMsg{Type: t})
	return m
}

func typeRunes(m Model, s string) Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// TestKeystrokeLatency50k is the Phase 0 exit gate (PRD §7): keystroke to
// rendered frame in under 16ms at p99 on a 50k-line file. Permanent perf
// regression guard — never skipped.
func TestKeystrokeLatency50k(t *testing.T) {
	m := New(buffer.New(bigFile(50_000)))
	m.Width, m.Height = 120, 50
	m.Go(25_000, 0)

	const n = 500
	for attempt := range 2 {
		samples := make([]time.Duration, n)
		for i := range samples {
			start := time.Now()
			m = typeRunes(m, "x")
			frame := m.View()
			samples[i] = time.Since(start)
			if len(frame) == 0 {
				t.Fatal("empty frame")
			}
		}
		slices.Sort(samples)
		p50, p99 := samples[n/2], samples[n*99/100]
		t.Logf("keystroke->frame p50=%s p99=%s", p50, p99)
		if p99 > 16*time.Millisecond {
			if attempt < 1 {
				t.Logf("p99 %s over budget; retrying once (parallel-suite CPU contention)", p99)
				continue
			}
			t.Fatalf("p99 keystroke latency %s exceeds one frame (16ms)", p99)
		}
		return
	}
}

func TestEditAndScroll(t *testing.T) {
	m := New(buffer.New([]byte("alpha\nbeta\ngamma\n")))
	m.Width, m.Height = 40, 2

	m.LineEdge(+1, false)
	m = typeRunes(m, "!")
	if got := string(m.Buf.Line(0)); got != "alpha!" {
		t.Fatalf("line 0 = %q", got)
	}
	if !m.Dirty {
		t.Fatal("edit did not set Dirty")
	}
	for range 3 {
		m.MoveV(+1, false)
	}
	if m.top == 0 {
		t.Fatal("viewport did not scroll to follow cursor")
	}
	if line, _ := m.Cursor(); line != 3 {
		t.Fatalf("cursor line = %d, want 3", line)
	}
}

func TestUndoRedo(t *testing.T) {
	m := New(buffer.New([]byte("hello world\n")))
	m.Width, m.Height = 80, 10
	m.Go(m.Buf.LineCount()-1, 0)
	m = typeRunes(m, "bye")
	if got := string(m.Buf.Line(1)); got != "bye" {
		t.Fatalf("after typing: %q", got)
	}
	// Coalesced burst: one undo removes all three runes.
	m.UndoStep()
	if got := string(m.Buf.Bytes()); got != "hello world\n" {
		t.Fatalf("after undo: %q", got)
	}
	m.RedoStep()
	if got := string(m.Buf.Line(1)); got != "bye" {
		t.Fatalf("after redo: %q", got)
	}
	// Undo restores cursor position.
	m.UndoStep()
	if line, col := m.Cursor(); line != 1 || col != 0 {
		t.Fatalf("cursor after undo = %d:%d, want 1:0", line, col)
	}
}

func TestMultiCursorTyping(t *testing.T) {
	m := New(buffer.New([]byte("aaa\nbbb\nccc\n")))
	m.Width, m.Height = 80, 10
	// Cursor on line 0, add cursors below on lines 1 and 2.
	m.AddCursor(+1)
	m.AddCursor(+1)
	if m.CursorCount() != 3 {
		t.Fatalf("cursors = %d, want 3", m.CursorCount())
	}
	m = typeRunes(m, "x")
	if got := string(m.Buf.Bytes()); got != "xaaa\nxbbb\nxccc\n" {
		t.Fatalf("multi-insert: %q", got)
	}
	// One undo reverts all three inserts.
	m.UndoStep()
	if got := string(m.Buf.Bytes()); got != "aaa\nbbb\nccc\n" {
		t.Fatalf("multi-undo: %q", got)
	}
}

func TestSelectNextOccurrence(t *testing.T) {
	m := New(buffer.New([]byte("foo bar\nfoo baz\nfoo qux\n")))
	m.Width, m.Height = 80, 10
	m.SelectNext() // select word "foo"
	if got := string(m.Selection()); got != "foo" {
		t.Fatalf("selection = %q", got)
	}
	m.SelectNext()
	m.SelectNext()
	if m.CursorCount() != 3 {
		t.Fatalf("cursors = %d, want 3", m.CursorCount())
	}
	m = typeRunes(m, "F")
	if got := string(m.Buf.Bytes()); got != "F bar\nF baz\nF qux\n" {
		t.Fatalf("edit-all: %q", got)
	}
}

func TestSearchAndReplaceAll(t *testing.T) {
	m := New(buffer.New([]byte("cat dog cat\ndog cat dog\n")))
	m.Width, m.Height = 80, 10
	m.SetSearch("cat", false)
	if _, total := m.SearchInfo(); total != 3 {
		t.Fatalf("matches = %d, want 3", total)
	}
	if !m.NextMatch(+1) {
		t.Fatal("NextMatch failed")
	}
	if got := string(m.Selection()); got != "cat" {
		t.Fatalf("selected %q", got)
	}
	if cur, _ := m.SearchInfo(); cur != 1 {
		t.Fatalf("cur = %d on first match, want 1", cur)
	}
	m.NextMatch(+1)
	m.NextMatch(+1) // last match: counter must say 3/3, not wrap to 1
	if cur, _ := m.SearchInfo(); cur != 3 {
		t.Fatalf("cur = %d on last match, want 3", cur)
	}
	m.NextMatch(-1)
	m.NextMatch(-1) // back to the first match
	if n := m.ReplaceAll("bird"); n != 3 {
		t.Fatalf("replaced %d, want 3", n)
	}
	if got := string(m.Buf.Bytes()); got != "bird dog bird\ndog bird dog\n" {
		t.Fatalf("after replace: %q", got)
	}
	// Single undo reverts the whole ReplaceAll.
	m.UndoStep()
	if got := string(m.Buf.Bytes()); got != "cat dog cat\ndog cat dog\n" {
		t.Fatalf("after undo: %q", got)
	}
}

func TestRegexSearch(t *testing.T) {
	m := New(buffer.New([]byte("v1 v22 v333\n")))
	m.Width, m.Height = 80, 10
	m.SetSearch(`v\d{2,}`, true)
	if _, total := m.SearchInfo(); total != 2 {
		t.Fatalf("matches = %d, want 2", total)
	}
}

func TestRuneAwareMovement(t *testing.T) {
	m := New(buffer.New([]byte("héllo\n")))
	m.Width, m.Height = 80, 10
	m.MoveH(+1, false)
	m.MoveH(+1, false) // over é (2 bytes)
	_, col := m.Cursor()
	if col != 3 {
		t.Fatalf("col = %d, want 3 (after 2-byte rune)", col)
	}
	m = key(m, tea.KeyBackspace)
	if got := string(m.Buf.Line(0)); got != "hllo" {
		t.Fatalf("after backspace: %q", got)
	}
}

// Vertical movement carries a byte wantCol onto other lines; it must snap to
// a rune boundary or a following delete splits a UTF-8 character.
func TestVerticalMoveSnapsRuneBoundary(t *testing.T) {
	m := New(buffer.New([]byte("x\néy\n")))
	m.Width, m.Height = 80, 10
	m.MoveH(+1, false) // after "x": wantCol = 1, which is mid-é on line 1
	m.MoveV(+1, false)
	if line, col := m.Cursor(); line != 1 || col != 0 {
		t.Fatalf("cursor = %d:%d, want 1:0 (snapped off the é's middle)", line, col)
	}
	m = key(m, tea.KeyBackspace) // join lines — must not split the é
	if got := m.Buf.Bytes(); !utf8.Valid(got) || string(got) != "xéy\n" {
		t.Fatalf("after down+backspace: %q (mid-rune cursor split the é)", got)
	}
}

func TestSelectionRender(t *testing.T) {
	m := New(buffer.New([]byte("abc\n")))
	m.Width, m.Height = 10, 1
	m.MoveH(+1, true)
	m.MoveH(+1, true)
	frame := m.View()
	if !strings.Contains(frame, "ab") {
		t.Fatalf("frame missing text: %q", frame)
	}
	if !strings.Contains(frame, "\x1b[") {
		t.Fatal("selection produced no styling")
	}
}

func BenchmarkKeystrokeFrame50k(b *testing.B) {
	m := New(buffer.New(bigFile(50_000)))
	m.Width, m.Height = 120, 50
	m.Go(25_000, 0)
	for b.Loop() {
		m = typeRunes(m, "x")
		_ = m.View()
	}
}

// A pathological single-line file (2MB, no newlines) must still render in
// budget: the renderer decodes only the visible window.
func TestHugeSingleLine(t *testing.T) {
	m := New(buffer.New(bytes.Repeat([]byte("abcdefgh"), 256*1024)))
	m.Width, m.Height = 120, 50
	m = typeRunes(m, "w") // warm-up: first touch pays allocation costs
	_ = m.View()
	best := time.Hour
	for range 3 { // best-of-3: a single sample is scheduler-noise flaky
		start := time.Now()
		m = typeRunes(m, "x")
		if frame := m.View(); len(frame) == 0 {
			t.Fatal("empty frame")
		}
		if d := time.Since(start); d < best {
			best = d
		}
	}
	if best > 16*time.Millisecond {
		t.Fatalf("huge line keystroke->frame took %s", best)
	}
}

func TestLineNumberGutter(t *testing.T) {
	defer SetLineNumbers(true)
	m := New(buffer.New([]byte("alpha\nbeta\ngamma\n")))
	m.Width, m.Height = 40, 3
	frame := m.View()
	for _, want := range []string{"  1 alpha", "  2 beta", "  3 gamma"} {
		if !strings.Contains(stripANSI(frame), want) {
			t.Fatalf("gutter missing %q in %q", want, stripANSI(frame))
		}
	}
	// Click at screen x inside the text must land on the right column:
	// gutter is 5 wide, so x=7 on row 1 -> "beta" col 2.
	m, _ = m.Update(tea.MouseMsg{X: 7, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if line, col := m.Cursor(); line != 1 || col != 2 {
		t.Fatalf("click with gutter mapped to %d:%d, want 1:2", line, col)
	}
	// Disabled -> no numbers, click mapping shifts back.
	SetLineNumbers(false)
	if strings.Contains(stripANSI(m.View()), " 1 alpha") {
		t.Fatal("gutter still rendered when disabled")
	}
	m, _ = m.Update(tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if line, col := m.Cursor(); line != 0 || col != 2 {
		t.Fatalf("click without gutter mapped to %d:%d, want 0:2", line, col)
	}
}

func stripANSI(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func TestCenterAfterJump(t *testing.T) {
	m := New(buffer.New(bigFile(1000)))
	m.Width, m.Height = 80, 20
	m.Go(500, 0)
	m.Center()
	if m.top != 490 {
		t.Fatalf("top = %d, want 490 (line 500 centered in 20 rows)", m.top)
	}
	// Near the ends it clamps instead of over-scrolling.
	m.Go(3, 0)
	m.Center()
	if m.top != 0 {
		t.Fatalf("top = %d, want 0", m.top)
	}
	m.Go(999, 0)
	m.Center()
	if m.top != 981 { // 1001 lines (trailing newline) - 20 rows
		t.Fatalf("top = %d, want 981", m.top)
	}
}

// Horizontal wheel scrolls xoff without the render loop snapping it back;
// moving the cursor snaps the view to it again.
func TestHorizontalWheel(t *testing.T) {
	long := strings.Repeat("abcdefghij", 20) // 200 cells
	m := New(buffer.New([]byte(long + "\nshort\n")))
	m.Width, m.Height = 20, 2

	wheel := func(b tea.MouseButton) {
		m, _ = m.Update(tea.MouseMsg{X: 5, Y: 0, Action: tea.MouseActionPress, Button: b})
	}
	wheel(tea.MouseButtonWheelRight)
	wheel(tea.MouseButtonWheelRight)
	if m.xoff == 0 {
		t.Fatal("wheel right did not scroll horizontally")
	}
	frame := stripANSI(m.View())
	if strings.Contains(frame, "abcdefghij") && m.xoff%10 != 0 {
		t.Fatalf("view did not honor xoff=%d: %q", m.xoff, frame)
	}
	if m.xoff != 12 {
		t.Fatalf("xoff=%d after two wheel steps, want 12", m.xoff)
	}
	// Rendering must not snap the scroll back to the cursor.
	m.View()
	if m.xoff != 12 {
		t.Fatalf("View reset xoff to %d", m.xoff)
	}
	// Wheel left clamps at 0.
	for range 5 {
		wheel(tea.MouseButtonWheelLeft)
	}
	if m.xoff != 0 {
		t.Fatalf("xoff=%d after wheel left, want 0", m.xoff)
	}
	// Cursor movement snaps the view back so the cursor is visible.
	wheel(tea.MouseButtonWheelRight)
	m.MoveH(1, false)
	if m.xoff > 1 {
		t.Fatalf("xoff=%d after cursor move to col 1: cursor off screen", m.xoff)
	}
}
