package app

// Custom commit-graph layout: one row per commit, box-drawing connectors,
// stable lanes (a branch keeps its column and, via logSyntax, its color),
// and a vertical branch-name header over the columns. Replaces git's own
// --graph ASCII, which spends extra rows shuffling lanes on every merge.

import (
	"strings"

	"github.com/GurYN/cove-editor/internal/git"
)

// renderGraph lays commits (topo order, newest first) into lanes.
// Each lane holds the hash it expects next; a commit lands in the leftmost
// lane expecting it, extra expecting lanes close (╯), extra parents open or
// join lanes (╮ / ┤). Lanes are two columns wide.
func renderGraph(cs []git.GraphCommit) string {
	var (
		lanes []string // hash each lane expects next; "" = free
		ever  []bool   // column ever occupied — header names only on fresh columns
		names = map[int]string{}
		out   []string
	)
	grow := func() int {
		lanes, ever = append(lanes, ""), append(ever, false)
		return len(lanes) - 1
	}

	for _, c := range cs {
		L, closing := -1, []int(nil)
		for i, l := range lanes {
			if l == c.Hash {
				if L < 0 {
					L = i
				} else {
					closing = append(closing, i)
				}
			}
		}
		if L < 0 { // nobody expects it: a branch tip, new lane
			for i, l := range lanes {
				if l == "" {
					L = i
					break
				}
			}
			if L < 0 {
				L = grow()
			}
			if !ever[L] {
				if n := branchName(c.Refs); n != "" {
					names[L] = n
				}
			}
			lanes[L] = c.Hash
		}
		ever[L] = true

		cells := make([]rune, 2*len(lanes))
		for i := range lanes {
			cells[2*i], cells[2*i+1] = ' ', ' '
			if lanes[i] != "" {
				cells[2*i] = '│'
			}
		}
		span := func(a, b int) { // horizontal run between lanes a and b
			if a > b {
				a, b = b, a
			}
			for x := 2*a + 1; x < 2*b; x++ {
				switch cells[x] {
				case '│':
					cells[x] = '┼'
				case ' ':
					cells[x] = '─'
				}
			}
		}
		for _, j := range closing {
			cells[2*j] = '╯'
			span(L, j)
			lanes[j] = ""
		}
		if len(c.Parents) > 0 { // first parent continues the lane
			lanes[L] = c.Parents[0]
		} else {
			lanes[L] = "" // root commit: the lane ends here
		}
		if len(c.Parents) > 1 { // merge: each extra parent branches off
			for _, p := range c.Parents[1:] {
				m := -1
				for i, l := range lanes {
					if i != L && l == p {
						m = i
						break
					}
				}
				if m >= 0 { // a lane already expects this parent: join it
					if m > L {
						cells[2*m] = '┤'
					} else {
						cells[2*m] = '├'
					}
				} else { // open a lane (skip columns already drawn this row)
					for i, l := range lanes {
						if l == "" && cells[2*i] == ' ' {
							m = i
							break
						}
					}
					if m < 0 {
						m = grow()
						cells = append(cells, ' ', ' ')
					}
					lanes[m], ever[m] = p, true
					if m > L {
						cells[2*m] = '╮'
					} else {
						cells[2*m] = '╭'
					}
				}
				span(L, m)
			}
		}
		cells[2*L] = '●'

		row := strings.TrimRight(string(cells), " ") + "  " + c.Short
		if c.Refs != "" {
			row += " (" + c.Refs + ")"
		}
		row += " " + c.Subject + " · " + c.Author + ", " + c.Age
		out = append(out, row)
	}
	if head := graphHeader(names); head != nil {
		out = append(append(head, ""), out...) // blank row between names and graph
	}
	return strings.Join(out, "\n")
}

// graphHeader draws lane branch names vertically, bottom-aligned so the
// letters end just above the graph, each at its lane's column.
func graphHeader(names map[int]string) []string {
	if len(names) == 0 {
		return nil
	}
	maxLane, maxLen := 0, 0
	for l, n := range names {
		maxLane = max(maxLane, l)
		maxLen = max(maxLen, len([]rune(n)))
	}
	rows := make([]string, maxLen)
	for r := range maxLen {
		line := make([]rune, 2*maxLane+1)
		for i := range line {
			line[i] = ' '
		}
		for l, n := range names {
			if rn := []rune(n); r >= maxLen-len(rn) {
				line[2*l] = rn[r-(maxLen-len(rn))]
			}
		}
		rows[r] = strings.TrimRight(string(line), " ")
	}
	return rows
}

// branchName picks a header label from a %D decoration: the shortest branch
// name — "main" beats "origin/main" — skipping tags, capped so the header
// stays short.
func branchName(refs string) string {
	best := ""
	for n := range strings.SplitSeq(refs, ", ") {
		n = strings.TrimPrefix(n, "HEAD -> ")
		if n == "" || n == "HEAD" || strings.HasPrefix(n, "tag: ") {
			continue
		}
		if best == "" || len(n) < len(best) {
			best = n
		}
	}
	if r := []rune(best); len(r) > 14 {
		best = string(r[:13]) + "…"
	}
	return best
}
