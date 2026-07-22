package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/GurYN/cove-editor/internal/lsp"
)

// syncWatched diffs workspace file mtimes against the previous sweep and
// forwards changes to every running language server as
// workspace/didChangeWatchedFiles. Cove registers no fs watcher, so without
// this a server never re-reads a closed file edited outside Cove (an agent,
// a shell, git) and its diagnostics go stale. Runs on the same triggers as
// the file-tree/git resync. The first sweep is baseline-only.
// ponytail: full stat walk per call, capped by listFiles at 20k files; a
// cap-truncated walk can misreport deletions. Upgrade to fsnotify if the
// focus-regain cost ever shows.
func (m *Model) syncWatched() []lsp.FileEvent {
	root := m.side.Root
	cur := make(map[string]time.Time, len(m.mtimes))
	for _, rel := range listFiles(root) {
		p := filepath.Join(root, rel)
		if fi, err := os.Stat(p); err == nil {
			cur[p] = fi.ModTime()
		}
	}
	prev := m.mtimes
	m.mtimes = cur
	if prev == nil {
		return nil
	}
	var events []lsp.FileEvent
	for p, t := range cur {
		if old, ok := prev[p]; !ok {
			events = append(events, lsp.FileEvent{URI: lsp.PathToURI(p), Type: 1})
		} else if !old.Equal(t) {
			events = append(events, lsp.FileEvent{URI: lsp.PathToURI(p), Type: 2})
		}
	}
	for p := range prev {
		if _, ok := cur[p]; !ok {
			events = append(events, lsp.FileEvent{URI: lsp.PathToURI(p), Type: 3})
		}
	}
	m.lspm.NotifyWatched(events)
	return events
}
