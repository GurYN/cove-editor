package editor

import (
	"bytes"
	"regexp"
	"strings"
)

type searchState struct {
	pattern string
	isRegex bool
	re      *regexp.Regexp // nil when plain or invalid
	matches [][2]int       // sorted byte ranges
	dirty   bool           // buffer changed since last scan
	active  bool
}

func (s *searchState) clear() { *s = searchState{} }

// SetSearch sets the live search pattern. Plain search is smart-case:
// all-lowercase patterns match case-insensitively.
func (m *Model) SetSearch(pattern string, isRegex bool) {
	m.search = searchState{pattern: pattern, isRegex: isRegex, active: pattern != "", dirty: true}
	if isRegex && pattern != "" {
		m.search.re, _ = regexp.Compile(pattern) // invalid regex → no matches
	}
	m.rescan()
}

// SearchInfo returns (current match 1-based, total). 0 total means no matches.
func (m *Model) SearchInfo() (int, int) {
	m.rescan()
	if !m.search.active || len(m.search.matches) == 0 {
		return 0, 0
	}
	cur := 0
	head := m.cursors[m.primary].Head
	for i, r := range m.search.matches {
		if r[1] <= head {
			cur = (i + 1) % len(m.search.matches)
		}
	}
	return cur + 1, len(m.search.matches)
}

func (m *Model) rescan() {
	s := &m.search
	if !s.dirty || !s.active {
		s.dirty = false
		return
	}
	s.dirty = false
	s.matches = s.matches[:0]
	src := m.Buf.Bytes()
	// ponytail: full-buffer rescan per edit while search is open; scan is
	// ~1ms on 2MB. Incremental match repair if it ever shows up in the gate.
	if s.isRegex {
		if s.re == nil {
			return
		}
		for _, loc := range s.re.FindAllIndex(src, -1) {
			if loc[0] < loc[1] {
				s.matches = append(s.matches, [2]int{loc[0], loc[1]})
			}
		}
		return
	}
	needle := []byte(s.pattern)
	fold := s.pattern == strings.ToLower(s.pattern)
	hay := src
	if fold {
		hay = bytes.ToLower(src)
		needle = bytes.ToLower(needle)
	}
	for i := 0; ; {
		j := bytes.Index(hay[i:], needle)
		if j < 0 {
			break
		}
		start := i + j
		s.matches = append(s.matches, [2]int{start, start + len(needle)})
		i = start + max(1, len(needle))
	}
}

// NextMatch selects the next (dir>0) or previous (dir<0) match relative to
// the primary cursor, wrapping. Returns false if there are no matches.
func (m *Model) NextMatch(dir int) bool {
	m.rescan()
	ms := m.search.matches
	if len(ms) == 0 {
		return false
	}
	head := m.cursors[m.primary].Head
	pick := -1
	if dir >= 0 {
		for i, r := range ms {
			if r[0] > head || (r[0] == head && r[1] > m.cursors[m.primary].Head) {
				if r[1] != m.cursors[m.primary].Head || r[0] != m.cursors[m.primary].Anchor {
					pick = i
					break
				}
			}
		}
		if pick < 0 {
			pick = 0
		}
	} else {
		for i := len(ms) - 1; i >= 0; i-- {
			if ms[i][1] < head {
				pick = i
				break
			}
		}
		if pick < 0 {
			pick = len(ms) - 1
		}
	}
	r := ms[pick]
	m.cursors = []Cursor{{Anchor: r[0], Head: r[1]}}
	m.primary = 0
	m.scrollToCursor()
	return true
}

// ReplaceCurrent replaces the primary selection if it is a match, then
// advances to the next match.
func (m *Model) ReplaceCurrent(repl string) {
	m.rescan()
	lo, hi := m.cursors[m.primary].sel()
	for _, r := range m.search.matches {
		if r[0] == lo && r[1] == hi {
			m.cursors = []Cursor{{Anchor: lo, Head: hi}}
			m.primary = 0
			m.InsertText(repl)
			break
		}
	}
	m.NextMatch(+1)
}

// ReplaceAll replaces every match in one undoable transaction. Returns the
// number of replacements.
func (m *Model) ReplaceAll(repl string) int {
	m.rescan()
	ms := m.search.matches
	if len(ms) == 0 {
		return 0
	}
	text := []byte(repl)
	edits := make([]Edit, len(ms))
	for i, r := range ms {
		edits[i] = Edit{Off: r[0], Old: append([]byte(nil), m.Buf.Slice(r[0], r[1])...), New: text}
	}
	m.apply(Tx{Edits: edits})
	m.cursors = m.cursors[:1]
	m.primary = 0
	return len(ms)
}

// visibleMatches returns matches intersecting [startOff, endOff).
func (m *Model) visibleMatches(startOff, endOff int) [][2]int {
	if !m.search.active {
		return nil
	}
	m.rescan()
	var out [][2]int
	for _, r := range m.search.matches {
		if r[1] > startOff && r[0] < endOff {
			out = append(out, r)
		}
	}
	return out
}
