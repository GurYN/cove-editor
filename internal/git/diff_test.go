package git

import (
	"bytes"
	"strings"
	"testing"
)

func signsOf(t *testing.T, old, new string) []byte {
	t.Helper()
	return LineSigns([]byte(old), []byte(new))
}

func want(t *testing.T, got []byte, want string) {
	t.Helper()
	// want uses '.' for no sign, one char per new line
	w := make([]byte, len(want))
	for i := range want {
		if want[i] == '.' {
			w[i] = 0
		} else {
			w[i] = want[i]
		}
	}
	if !bytes.Equal(got, w) {
		t.Fatalf("signs = %q, want %q", got, w)
	}
}

func TestLineSigns(t *testing.T) {
	want(t, signsOf(t, "a\nb\nc\n", "a\nb\nc\n"), "....")          // identical (incl. trailing empty line)
	want(t, signsOf(t, "a\nb\nc", "a\nX\nc"), ".m.")               // modified
	want(t, signsOf(t, "a\nb", "a\nnew\nb"), ".a.")                // added
	want(t, signsOf(t, "a\nb\nc", "a\nc"), "d.")                   // deleted: marker above the gap
	want(t, signsOf(t, "a\nb\nc", "b\nc"), "d.")                   // deleted first line: marker clamps to 0
	want(t, signsOf(t, "", "x\ny"), "aa")                          // empty baseline: all added
	want(t, signsOf(t, "a\nb\nc\nd\ne", "a\nX\nc\nY\ne"), ".m.m.") // two separate hunks
	want(t, signsOf(t, "a\nb\nc", "a\nb1\nb2\nc"), ".ma.")         // replace 1→2: mod + add
}

func TestLineSignsBigFileFastPath(t *testing.T) {
	// 50k identical lines with one edit: prefix/suffix trim must keep this
	// instant and mark exactly one line.
	var sb strings.Builder
	for i := 0; i < 50000; i++ {
		sb.WriteString("line\n")
	}
	old := sb.String()
	lines := strings.Split(old, "\n")
	lines[25000] = "changed"
	got := LineSigns([]byte(old), []byte(strings.Join(lines, "\n")))
	count := 0
	for i, s := range got {
		if s != 0 {
			count++
			if i != 25000 || s != SignMod {
				t.Fatalf("sign %c at line %d", s, i)
			}
		}
	}
	if count != 1 {
		t.Fatalf("marked %d lines, want 1", count)
	}
}
