package app

import (
	"strings"
	"testing"
)

const conflicted = "keep\n<<<<<<< HEAD\nours line\n=======\ntheirs line\n>>>>>>> feature\ntail\n"

func TestConflictBlocks(t *testing.T) {
	src := []byte(conflicted)
	bs := conflictBlocks(src, 0, len(src))
	if len(bs) != 1 {
		t.Fatalf("blocks = %+v", bs)
	}
	b := bs[0]
	if string(b.ours(src)) != "ours line\n" || string(b.theirs(src)) != "theirs line\n" {
		t.Fatalf("ours=%q theirs=%q", b.ours(src), b.theirs(src))
	}
	if !strings.HasPrefix(string(src[b.start:]), "<<<<<<<") || string(src[b.end:]) != "tail\n" {
		t.Fatalf("bounds: start=%d end=%d", b.start, b.end)
	}

	// window cutting the block mid-way still yields a styleable block
	cut := conflictBlocks(src, 0, b.mid+2)
	if len(cut) != 1 || cut[0].start != b.start {
		t.Fatalf("cut = %+v", cut)
	}

	if got := conflictBlocks([]byte("plain\ntext\n"), 0, 11); len(got) != 0 {
		t.Fatalf("false positive: %+v", got)
	}
}

func TestMergeAccept(t *testing.T) {
	m := Model{docs: []*doc{newDoc("x.txt", []byte(conflicted))}}
	m.mergeAccept("theirs")
	if got := string(m.doc().ed.Buf.Bytes()); got != "keep\ntheirs line\ntail\n" {
		t.Fatalf("buffer = %q", got)
	}
	if !strings.Contains(m.lastMsg, "all resolved") {
		t.Fatalf("msg = %q", m.lastMsg)
	}
	m.mergeAccept("theirs") // nothing left
	if !strings.Contains(m.lastMsg, "no conflict markers") {
		t.Fatalf("msg = %q", m.lastMsg)
	}

	m = Model{docs: []*doc{newDoc("x.txt", []byte(conflicted))}}
	m.mergeAccept("both")
	if got := string(m.doc().ed.Buf.Bytes()); got != "keep\nours line\ntheirs line\ntail\n" {
		t.Fatalf("buffer = %q", got)
	}
}
