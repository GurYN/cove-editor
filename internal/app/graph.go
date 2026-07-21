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
// Every branch tip is pre-seeded with its own labeled lane, so each branch
// owns a column with its name on top and a line running down to its tip —
// even a branch whose tip is a deep ancestor (main/tst parked on the root
// commit) stays visible. Each lane holds the hash it expects next; a commit
// lands in the leftmost lane expecting it, extra expecting lanes close (╯),
// extra parents open or join lanes (╮ / ┤). Lanes are two columns wide.
func renderGraph(cs []git.GraphCommit) string {
	var (
		lanes []string // hash each lane expects next; "" = free
		names = map[int]string{}
		out   []string
	)
	grow := func() int { lanes = append(lanes, ""); return len(lanes) - 1 }

	for _, c := range cs { // one labeled lane per branch-decorated commit
		if lbl := branchLabel(c.Refs); lbl != "" {
			names[grow()] = lbl
			lanes[len(lanes)-1] = c.Hash
		}
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
		if L < 0 { // nobody expects it (tag-only tip): a fresh lane
			for i, l := range lanes {
				if l == "" {
					L = i
					break
				}
			}
			if L < 0 {
				L = grow()
			}
			lanes[L] = c.Hash
		}

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
					lanes[m] = p
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
		// One row of bare lane lines separates the names from the commits.
		sep := strings.TrimRight(strings.Repeat("│ ", len(names)), " ")
		out = append(append(head, sep), out...)
	}
	return strings.Join(out, "\n")
}

// graphHeader draws one row per labeled lane — the branch name horizontal,
// a ╭─ corner dropping into its lane's column, earlier lanes passing
// through as │. Header lanes are seeded first in renderGraph, so their
// indices are contiguous from 0.
func graphHeader(names map[int]string) []string {
	if len(names) == 0 {
		return nil
	}
	rows := make([]string, len(names))
	for l := range rows {
		rows[l] = strings.Repeat("│ ", l) + "╭─ " + names[l]
	}
	return rows
}

// branchLabel builds a lane's header label from a %D decoration: the
// commit's branch names comma-joined — HEAD's branch first, then locals,
// then remotes with no same-named local ("origin/main" is dropped when
// "main" is on the same commit). Tags skipped.
func branchLabel(refs string) string {
	var head string
	var locals, remotes []string
	isLocal := map[string]bool{}
	for n := range strings.SplitSeq(refs, ", ") {
		if h, ok := strings.CutPrefix(n, "HEAD -> "); ok {
			head = h
			isLocal[h] = true
			continue
		}
		if n == "" || n == "HEAD" || strings.HasPrefix(n, "tag: ") {
			continue
		}
		if strings.Contains(n, "/") {
			remotes = append(remotes, n)
		} else {
			locals = append(locals, n)
			isLocal[n] = true
		}
	}
	var parts []string
	if head != "" {
		parts = append(parts, head)
	}
	parts = append(parts, locals...)
	for _, r := range remotes {
		// origin/HEAD is a symref, not a branch — never worth a label.
		if _, tail, _ := strings.Cut(r, "/"); !isLocal[tail] && tail != "HEAD" {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, ",")
}
