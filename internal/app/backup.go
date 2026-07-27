package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"

	"github.com/GurYN/cove-editor/internal/config"
	"github.com/GurYN/cove-editor/internal/editor"
)

// Crash recovery: dirty buffers are snapshotted to <config-dir>/backups on
// the 2s watch tick. A buffer that reads clean again gets its backup
// removed, and quitting removes all of them — a confirmed quit is a
// deliberate discard. Only a crash leaves backups behind; reopening the
// file restores the snapshot as an undoable edit, guarded by the disk mtime
// recorded at snapshot time so a file rewritten after the crash is never
// clobbered.

type backupData struct {
	Path  string `json:"path"`
	Mtime int64  `json:"mtime"` // disk mtime (ns) the buffer was based on
	Text  []byte `json:"text"`
}

func backupPath(path string) string {
	abs, _ := filepath.Abs(path)
	h := fnv.New64a()
	h.Write([]byte(abs))
	return filepath.Join(filepath.Dir(config.Path()), "backups", fmt.Sprintf("%x.json", h.Sum64()))
}

// writeBackups runs on the watch tick: snapshot dirty buffers that moved
// since the last snapshot, drop backups of buffers that are clean again.
func (m *Model) writeBackups() {
	for _, d := range m.docs {
		if d.virtual {
			continue
		}
		if !d.ed.Dirty {
			if d.backedUp {
				os.Remove(backupPath(d.path))
				d.backedUp = false
			}
			continue
		}
		if d.backedUp && d.ed.Rev == d.backupRev {
			continue
		}
		abs, _ := filepath.Abs(d.path)
		data, err := json.Marshal(backupData{Path: abs, Mtime: d.mtime.UnixNano(), Text: d.ed.Buf.Bytes()})
		if err != nil {
			continue
		}
		p := backupPath(d.path)
		os.MkdirAll(filepath.Dir(p), 0o700)
		if os.WriteFile(p, data, 0o600) == nil { // 0600: buffers may hold secrets
			d.backedUp, d.backupRev = true, d.ed.Rev
		}
	}
}

// clearBackups runs on quit: leaving with unsaved changes is confirmed by
// the user, so the snapshots must not resurrect discarded edits.
func (m *Model) clearBackups() {
	for _, d := range m.docs {
		if d.backedUp {
			os.Remove(backupPath(d.path))
		}
	}
}

// dropBackup forgets one doc's snapshot (tab closed with changes discarded).
func (m *Model) dropBackup(d *doc) {
	if d.backedUp {
		os.Remove(backupPath(d.path))
		d.backedUp = false
	}
}

// tryRestore applies a crash snapshot to a freshly opened doc, undoably.
// A backup based on a different disk state (the file changed after the
// crash) is discarded — the disk version is newer truth.
func (m *Model) tryRestore(d *doc) {
	p := backupPath(d.path)
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var b backupData
	if json.Unmarshal(data, &b) != nil {
		os.Remove(p)
		return
	}
	if bytes.Equal(b.Text, d.ed.Buf.Bytes()) { // nothing to restore
		os.Remove(p)
		return
	}
	if b.Mtime != d.mtime.UnixNano() {
		os.Remove(p)
		m.notify(filepath.Base(d.path) + ": crash recovery data discarded (file changed on disk)")
		return
	}
	old := append([]byte(nil), d.ed.Buf.Bytes()...)
	d.ed.ApplyEdits([]editor.Edit{{Off: 0, Old: old, New: b.Text}})
	d.backedUp, d.backupRev = true, d.ed.Rev
	m.notify(filepath.Base(d.path) + ": restored unsaved changes (undo to discard)")
}

// sweepBackups removes snapshots old enough that their crash story is cold —
// orphans from renamed or never-reopened files must not pile up forever.
func sweepBackups() {
	dir := filepath.Join(filepath.Dir(config.Path()), "backups")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 30*24*time.Hour {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
