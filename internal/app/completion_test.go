package app

import "testing"

func TestStripSnippet(t *testing.T) {
	cases := []struct {
		in    string
		out   string
		caret int
	}{
		{"$0</html>", "</html>", 0},             // html close-tag completion
		{"plain", "plain", -1},                  // no snippet syntax
		{"foo($1)$0", "foo()", 4},               // caret inside parens, not at $0
		{"foo(${1:x}, ${2:y})", "foo(x, y)", 4}, // placeholders kept
		{"${0}", "", 0},                         // braced zero
		{"cost: \\$5", "cost: $5", -1},          // escaped dollar
		{"a$b", "a$b", -1},                      // lone $ is literal
	}
	for _, c := range cases {
		out, caret := stripSnippet(c.in)
		if out != c.out || caret != c.caret {
			t.Errorf("stripSnippet(%q) = %q, %d; want %q, %d", c.in, out, caret, c.out, c.caret)
		}
	}
}
