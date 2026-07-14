package editor

import "time"

// Edit is a single replacement: Old bytes at Off become New. Offsets are in
// pre-transaction coordinates; a transaction's edits are ascending and
// non-overlapping.
type Edit struct {
	Off      int
	Old, New []byte
}

// Tx is one undoable step: a set of simultaneous edits (one per cursor)
// plus the cursor sets to restore on undo/redo.
type Tx struct {
	Edits         []Edit
	Before, After []Cursor
	at            time.Time
	typing        bool // pure insertion at cursors; eligible for coalescing
}

// inverse returns the edits that revert tx, in post-tx coordinates,
// ascending and non-overlapping.
func (tx Tx) inverse() []Edit {
	inv := make([]Edit, len(tx.Edits))
	delta := 0
	for i, e := range tx.Edits {
		inv[i] = Edit{Off: e.Off + delta, Old: e.New, New: e.Old}
		delta += len(e.New) - len(e.Old)
	}
	return inv
}

type history struct {
	undo, redo []Tx
}

// push records tx, coalescing bursts of typing into one undo step.
func (h *history) push(tx Tx) {
	h.redo = nil
	if n := len(h.undo); n > 0 && coalescable(h.undo[n-1], tx) {
		prev := &h.undo[n-1]
		for i := range prev.Edits {
			prev.Edits[i].New = append(prev.Edits[i].New, tx.Edits[i].New...)
		}
		prev.After = tx.After
		prev.at = tx.at
		return
	}
	h.undo = append(h.undo, tx)
}

// coalescable: both are pure typing, same cursor count, each insertion lands
// exactly where the previous one left the cursor, within a short burst.
func coalescable(prev, next Tx) bool {
	if !prev.typing || !next.typing || len(prev.Edits) != len(next.Edits) {
		return false
	}
	if next.at.Sub(prev.at) > 800*time.Millisecond {
		return false
	}
	for i, e := range next.Edits {
		if e.Off != prev.After[i].Head {
			return false
		}
	}
	return true
}

// seal marks the last transaction as a coalescing boundary: following
// keystrokes start a fresh undo step (used after paste).
func (h *history) seal() {
	if n := len(h.undo); n > 0 {
		h.undo[n-1].typing = false
	}
}

func (h *history) popUndo() (Tx, bool) {
	if len(h.undo) == 0 {
		return Tx{}, false
	}
	tx := h.undo[len(h.undo)-1]
	h.undo = h.undo[:len(h.undo)-1]
	h.redo = append(h.redo, tx)
	return tx, true
}

func (h *history) popRedo() (Tx, bool) {
	if len(h.redo) == 0 {
		return Tx{}, false
	}
	tx := h.redo[len(h.redo)-1]
	h.redo = h.redo[:len(h.redo)-1]
	h.undo = append(h.undo, tx)
	return tx, true
}
