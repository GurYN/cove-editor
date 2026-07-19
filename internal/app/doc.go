package app

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/buffer"
	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/git"
	"github.com/GurYN/cove-editor/internal/syntax"
)

// doc is one open file: an editor over its buffer plus save metadata.
type doc struct {
	path    string
	ed      editor.Model
	crlf    bool
	mtime   time.Time
	confirm bool   // next save overwrites an externally-modified file
	sentRev int    // last editor revision synced to the language server
	virtual bool       // read-only in-memory view (git diff); never saved
	repo    *repoState // repo a virtual git tab (graph/commit) belongs to
	head    []byte     // file content at git HEAD (LF-normalized); nil = no baseline
	lineMap []int  // buffer line → HEAD line (-1 added/modified); from updateSigns

	blame     []git.BlameLine // per-HEAD-line; nil = not fetched, empty = unavailable
	blameBusy bool

	seen time.Time // last disk mtime the watcher warned about (dirty buffers)
	warn string    // one-shot load warning, surfaced by openFile
}

func newDoc(path string, data []byte) *doc {
	d := &doc{path: path, crlf: bytes.Contains(data, []byte("\r\n"))}
	if d.crlf {
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	}
	if fi, err := os.Stat(path); err == nil {
		d.mtime = fi.ModTime()
	}
	if !utf8.Valid(data) {
		d.warn = filepath.Base(path) + ": not valid UTF-8 — display may be lossy"
	}
	d.ed = editor.New(buffer.New(data))
	if h := syntax.New(path, data); h != nil {
		d.ed.Syntax = h
	}
	return d
}

// isBinary sniffs for a NUL byte in the head of the file — git's own
// text/binary heuristic. Binary files (executables, zips, images) would
// render as control-picture soup and get corrupted on save.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}

// isAsset reports whether path is a known image/PDF type — always shown as
// a metadata preview, even when small enough to pass the NUL sniff.
func isAsset(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".pdf":
		return true
	}
	return false
}

// assetDoc builds a read-only metadata preview tab for a file the editor
// can't display (images, PDFs, other binaries).
// ponytail: metadata only — inline rendering needs per-terminal graphics
// protocols (Kitty/Sixel) and fights the cell-grid renderer.
func assetDoc(path string, data []byte) *doc {
	desc := "binary file"
	if cfg, kind, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		desc = fmt.Sprintf("%s image, %d×%d px", strings.ToUpper(kind), cfg.Width, cfg.Height)
	} else if bytes.HasPrefix(data, []byte("%PDF-")) && len(data) >= 8 {
		desc = "PDF document, version " + string(data[5:8])
	}
	text := fmt.Sprintf("%s\n\n%s\n%s\n\npreview only — content not rendered\n",
		filepath.Base(path), desc, humanSize(len(data)))
	ed := editor.New(buffer.New([]byte(text)))
	ed.ReadOnly = true
	return &doc{path: path, virtual: true, ed: ed}
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func loadDoc(path string) (*doc, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	// len check keeps brand-new (not-yet-on-disk) files editable.
	if len(data) > 0 && (isAsset(path) || isBinary(data)) {
		return assetDoc(path, data), nil
	}
	return newDoc(path, data), nil
}

// watchTickMsg drives the disk-change poll (every 2s).
// ponytail: mtime polling, no watcher dependency; fsnotify if 2s ever feels slow.
type watchTickMsg struct{}

func watchTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return watchTickMsg{} })
}

// checkDiskChanges polls open files for outside edits. A clean buffer
// reloads in place (undoably, cursor kept); a dirty one gets a one-time
// warning and the save-time mtime guard arbitrates from there.
func (m *Model) checkDiskChanges() {
	for _, d := range m.docs {
		if d.virtual || d.mtime.IsZero() {
			continue
		}
		fi, err := os.Stat(d.path)
		if err != nil || fi.ModTime().Equal(d.mtime) {
			continue
		}
		if !d.ed.Dirty {
			m.reloadDoc(d.path)
			m.lastMsg = filepath.Base(d.path) + " reloaded (changed on disk)"
			continue
		}
		if !fi.ModTime().Equal(d.seen) {
			d.seen = fi.ModTime()
			m.lastMsg = filepath.Base(d.path) + " changed on disk — buffer has unsaved edits"
		}
	}
}

// save writes the buffer to disk. Returns a status message; guards against
// overwriting a file modified outside Cove (second save confirms).
func (d *doc) save() string {
	if d.virtual {
		return "read-only view"
	}
	// mtime.IsZero (never-loaded new file) with a successful Stat means the
	// file appeared on disk since we opened the tab — same confirm flow.
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(d.path); err == nil {
		mode = fi.Mode()
		if !fi.ModTime().Equal(d.mtime) && !d.confirm {
			d.confirm = true
			return "file changed on disk — save again to overwrite"
		}
	}
	d.confirm = false
	data := d.ed.Buf.Bytes()
	if d.crlf {
		data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	}
	// Write-then-rename so a mid-write failure (disk full, crash) never
	// leaves the original truncated.
	tmp := d.path + ".cove~"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		os.Remove(tmp)
		return err.Error()
	}
	if err := os.Rename(tmp, d.path); err != nil {
		os.Remove(tmp)
		return err.Error()
	}
	if fi, err := os.Stat(d.path); err == nil {
		d.mtime = fi.ModTime()
	}
	d.ed.Dirty = false
	return "saved"
}
