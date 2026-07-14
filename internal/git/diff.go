package git

import "bytes"

// Gutter sign values produced by LineSigns.
const (
	SignNone = byte(0)
	SignAdd  = byte('a')
	SignMod  = byte('m')
	SignDel  = byte('d') // lines were deleted just below this line
)

// LineSigns diffs old→new content and returns one sign per new line.
func LineSigns(oldB, newB []byte) []byte {
	signs, _ := Align(oldB, newB)
	return signs
}

// Align diffs old→new content. It returns one sign per new line plus, for
// each new line, the old line it corresponds to (-1 for added/modified
// lines) — the mapping blame needs to survive unsaved edits.
// Common prefix/suffix are trimmed first, so the Myers pass only sees the
// edited region — typical cost is O(changed lines), not O(file).
func Align(oldB, newB []byte) (signs []byte, oldFor []int) {
	a, b := splitLines(oldB), splitLines(newB)
	signs = make([]byte, len(b))
	oldFor = make([]int, len(b))
	for i := range oldFor {
		oldFor[i] = -1
	}
	p := 0
	for p < len(a) && p < len(b) && bytes.Equal(a[p], b[p]) {
		oldFor[p] = p
		p++
	}
	ea, eb := len(a), len(b)
	for ea > p && eb > p && bytes.Equal(a[ea-1], b[eb-1]) {
		ea--
		eb--
		oldFor[eb] = ea
	}
	if p == ea && p == eb { // identical
		return signs, oldFor
	}
	ma, mb := a[p:ea], b[p:eb]
	delA, insB, ok := diffMarks(ma, mb)
	if !ok { // edit distance cap hit: coarse-mark the whole middle
		// ponytail: only pathological rewrites land here; per-hunk detail
		// needs a bigger D budget if anyone complains.
		delA = make([]bool, len(ma))
		insB = make([]bool, len(mb))
		for i := range delA {
			delA[i] = true
		}
		for i := range insB {
			insB[i] = true
		}
	}
	// Convert delete/insert marks to signs: paired del+ins = modified,
	// lone ins = added, lone del = a deletion marker on the line above.
	markDel := func(atB int) {
		at := max(0, min(atB, len(b)-1))
		if len(b) > 0 && signs[at] == SignNone {
			signs[at] = SignDel
		}
	}
	i, j := 0, 0
	for i < len(ma) || j < len(mb) {
		switch {
		case i < len(ma) && delA[i] && j < len(mb) && insB[j]:
			signs[p+j] = SignMod
			i++
			j++
		case j < len(mb) && insB[j]:
			if signs[p+j] == SignNone {
				signs[p+j] = SignAdd
			}
			j++
		case i < len(ma) && delA[i]:
			markDel(p + j - 1)
			i++
		default: // matched pair
			oldFor[p+j] = p + i
			i++
			j++
		}
	}
	return signs, oldFor
}

func splitLines(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	return bytes.Split(b, []byte("\n"))
}

// diffMarks runs Myers O(ND) over the trimmed middle and reports which a
// lines are deleted and which b lines are inserted. ok is false when the
// edit distance exceeds the cap (caller falls back to coarse marking).
func diffMarks(a, b [][]byte) (delA, insB []bool, ok bool) {
	n, m := len(a), len(b)
	delA, insB = make([]bool, n), make([]bool, m)
	maxD := min(n+m, 1000)
	off := maxD
	v := make([]int, 2*maxD+1)
	var trace [][]int
	D := -1
	for d := 0; d <= maxD && D < 0; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && bytes.Equal(a[x], b[y]) {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				D = d
				break
			}
		}
	}
	if D < 0 {
		return delA, insB, false
	}
	x, y := n, m
	for d := D; d > 0; d-- {
		vp := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vp[off+k-1] < vp[off+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vp[off+prevK]
		prevY := prevX - prevK
		if prevK == k+1 {
			insB[prevY] = true // down move: b[prevY] inserted
		} else {
			delA[prevX] = true // right move: a[prevX] deleted
		}
		x, y = prevX, prevY
	}
	return delA, insB, true
}
