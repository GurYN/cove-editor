package editor

import (
	"strings"
	"testing"

	"github.com/GurYN/cove-editor/internal/buffer"
)

func ghostEd(src string) Model {
	m := New(buffer.New([]byte(src)))
	m.Width = 60
	m.Height = 6
	return m
}

func TestGhostRendersAtEOL(t *testing.T) {
	m := ghostEd("func f() {\n}\n")
	m.Go(0, 10) // end of line 0
	m.SetGhost("return nil", m.Buf.Offset(0, 10))
	if !m.GhostVisible() {
		t.Fatal("ghost not visible at anchor")
	}
	if v := m.View(); !strings.Contains(stripANSI(v), "return nil") {
		t.Errorf("view missing ghost text:\n%s", v)
	}
}

func TestGhostHiddenOffAnchor(t *testing.T) {
	m := ghostEd("abc\ndef\n")
	m.Go(0, 3)
	m.SetGhost("XYZ", m.Buf.Offset(0, 3))
	m.MoveH(-1, false) // cursor moved: anchor broken
	if m.GhostVisible() {
		t.Error("ghost visible after cursor move")
	}
	if strings.Contains(stripANSI(m.View()), "XYZ") {
		t.Error("moved-cursor view still shows ghost")
	}
	// mid-line anchor never shows
	m2 := ghostEd("abcdef\n")
	m2.Go(0, 3)
	m2.SetGhost("XYZ", m2.Buf.Offset(0, 3))
	if m2.GhostVisible() {
		t.Error("mid-line ghost visible")
	}
}

func TestGhostMultiLinePushesRows(t *testing.T) {
	m := ghostEd("a\nb\nc\n")
	m.Go(0, 1)
	m.SetGhost("one\ntwo\nthree", m.Buf.Offset(0, 1))
	v := stripANSI(m.View())
	lines := strings.Split(v, "\n")
	if len(lines) != m.Height {
		t.Fatalf("view rows = %d, want %d:\n%s", len(lines), m.Height, v)
	}
	// rows: a+one, two, three, b, c, (filler)
	for i, want := range []string{"one", "two", "three", "b", "c"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("row %d = %q, want it to contain %q", i, lines[i], want)
		}
	}
}

func TestAcceptGhost(t *testing.T) {
	m := ghostEd("x := \n")
	m.Go(0, 5)
	m.SetGhost("compute()\nreturn x", m.Buf.Offset(0, 5))
	if !m.AcceptGhost() {
		t.Fatal("accept refused")
	}
	if got := string(m.Buf.Bytes()); got != "x := compute()\nreturn x\n" {
		t.Errorf("buffer = %q", got)
	}
	if m.Ghost != "" {
		t.Error("ghost not cleared after accept")
	}
	m.UndoStep()
	if got := string(m.Buf.Bytes()); got != "x := \n" {
		t.Errorf("undo = %q", got)
	}
}

func TestGhostMouseMapping(t *testing.T) {
	m := ghostEd("a\nb\nc\n")
	m.Go(0, 1)
	m.SetGhost("one\ntwo\nthree", m.Buf.Offset(0, 1))
	// Row 3 shows buffer line 1 ("b") — two ghost continuation rows shifted it.
	if l := m.lineAtRow(3); l != 1 {
		t.Errorf("lineAtRow(3) = %d, want 1", l)
	}
	// Clicks inside the ghost land on the anchor line.
	if l := m.lineAtRow(2); l != 0 {
		t.Errorf("lineAtRow(2) = %d, want 0", l)
	}
}
