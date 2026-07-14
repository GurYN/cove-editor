package app

import (
	"bytes"
	"os"
	"time"

	"github.com/GurYN/cove-editor/internal/buffer"
	"github.com/GurYN/cove-editor/internal/editor"
	"github.com/GurYN/cove-editor/internal/syntax"
)

// doc is one open file: an editor over its buffer plus save metadata.
type doc struct {
	path    string
	ed      editor.Model
	crlf    bool
	mtime   time.Time
	confirm bool // next save overwrites an externally-modified file
	sentRev int  // last editor revision synced to the language server
	virtual bool // read-only in-memory view (git diff); never saved
}

func newDoc(path string, data []byte) *doc {
	d := &doc{path: path, crlf: bytes.Contains(data, []byte("\r\n"))}
	if d.crlf {
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	}
	if fi, err := os.Stat(path); err == nil {
		d.mtime = fi.ModTime()
	}
	d.ed = editor.New(buffer.New(data))
	if h := syntax.New(path, data); h != nil {
		d.ed.Syntax = h
	}
	return d
}

func loadDoc(path string) (*doc, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return newDoc(path, data), nil
}

// save writes the buffer to disk. Returns a status message; guards against
// overwriting a file modified outside Cove (second save confirms).
func (d *doc) save() string {
	if d.virtual {
		return "read-only view"
	}
	if fi, err := os.Stat(d.path); err == nil && !d.mtime.IsZero() && !fi.ModTime().Equal(d.mtime) && !d.confirm {
		d.confirm = true
		return "file changed on disk — save again to overwrite"
	}
	d.confirm = false
	data := d.ed.Buf.Bytes()
	if d.crlf {
		data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	}
	if err := os.WriteFile(d.path, data, 0o644); err != nil {
		return err.Error()
	}
	if fi, err := os.Stat(d.path); err == nil {
		d.mtime = fi.ModTime()
	}
	d.ed.Dirty = false
	return "saved"
}
