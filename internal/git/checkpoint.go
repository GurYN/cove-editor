package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Checkpoints back the agent review: a snapshot of the working tree the
// agent's later edits are diffed against. A checkpoint is an ordinary
// commit (no parent) of the whole worktree — tracked, modified and
// untracked files alike, .gitignore honored — kept reachable under
// refs/cove/checkpoint so gc leaves it alone. The real index is never
// touched: a temporary copy of it takes the add, which also keeps git's
// stat cache so only changed files are rehashed.

const checkpointRef = "refs/cove/checkpoint"

// runEnv is run with extra environment entries.
func runEnv(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	noPrompt(cmd)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git: %s", firstLine(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// worktreeTree writes the current working tree as a tree object and
// returns its SHA.
func worktreeTree(top string) (string, error) {
	tmp, err := os.CreateTemp("", "cove-index-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	// Seed from the real index for its stat cache; a missing index (fresh
	// repo) means no temp file at all — git treats an empty file as corrupt.
	idx, _ := run(top, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(idx) {
		idx = filepath.Join(top, idx)
	}
	if data, err := os.ReadFile(idx); err == nil {
		os.WriteFile(tmpPath, data, 0o600)
		// Keep the original index mtime on the copy. Git treats entries
		// whose file mtime is not older than the index as "racily clean"
		// and rehashes them; a fresh copy timestamp would make every
		// same-second, same-size rewrite look clean and snapshot the old
		// blob.
		if fi, err := os.Stat(idx); err == nil {
			os.Chtimes(tmpPath, fi.ModTime(), fi.ModTime())
		}
	} else {
		os.Remove(tmpPath)
	}
	env := []string{"GIT_INDEX_FILE=" + tmpPath}
	if _, err := runEnv(top, env, "add", "-A", "--ignore-errors", "--", "."); err != nil {
		return "", err
	}
	return runEnv(top, env, "write-tree")
}

// Checkpoint snapshots the working tree and returns the checkpoint's
// commit SHA. Author identity is fixed so it works in repos with no
// user.name configured.
func Checkpoint(top string) (string, error) {
	tree, err := worktreeTree(top)
	if err != nil {
		return "", err
	}
	ident := []string{"GIT_AUTHOR_NAME=cove", "GIT_AUTHOR_EMAIL=cove@localhost",
		"GIT_COMMITTER_NAME=cove", "GIT_COMMITTER_EMAIL=cove@localhost"}
	sha, err := runEnv(top, ident, "commit-tree", tree, "-m", "cove checkpoint")
	if err != nil {
		return "", err
	}
	if _, err := run(top, "update-ref", checkpointRef, sha); err != nil {
		return "", err
	}
	return sha, nil
}

// LastCheckpoint returns the checkpoint ref's SHA, "" when none exists.
func LastCheckpoint(top string) string {
	sha, err := run(top, "rev-parse", "--verify", "-q", checkpointRef)
	if err != nil {
		return ""
	}
	return sha
}

// ShowAt returns a file's content at rev (path repo-relative, slash form).
func ShowAt(top, rev, path string) ([]byte, error) {
	cmd := exec.Command("git", "show", rev+":"+path)
	cmd.Dir = top
	return cmd.Output()
}

// Hunk is one change block of a unified diff. Old and New hold the full
// lines of each side (context included, no +/- prefix), so replacing the
// New range in the current file with Old reverts the hunk exactly.
type Hunk struct {
	OldStart, OldLines int // 1-based; OldLines may be 0 (pure insertion)
	NewStart, NewLines int
	Header             string // text after the @@ … @@ (enclosing function)
	Old, New           []string
	Added, Removed     int
	OldNoNL, NewNoNL   bool   // that side's last line has no trailing newline
	Text               string // the hunk as git printed it (header + prefixed lines)
}

// FileDiff is one changed file between two trees. Hunks is nil for added,
// deleted and binary files: the whole file is the change.
type FileDiff struct {
	Path   string // repo-relative, slash-separated
	Status byte   // 'A' added, 'M' modified, 'D' deleted
	Binary bool
	Hunks  []Hunk
}

// MaxChangedFiles caps ChangedSince: an agent that touches more than this
// is not being reviewed hunk by hunk anyway.
const MaxChangedFiles = 200

// ChangedSince diffs the checkpoint commit against the current working
// tree (untracked files included), one FileDiff per changed path.
func ChangedSince(top, sha string) ([]FileDiff, error) {
	tree, err := worktreeTree(top)
	if err != nil {
		return nil, err
	}
	return DiffTrees(top, sha, tree)
}

// DiffTrees diffs two tree-ish objects, one FileDiff per changed path.
func DiffTrees(top, a, b string) ([]FileDiff, error) {
	sha, tree := a, b
	out, err := run(top, "diff-tree", "-r", "-z", "--no-renames", "--name-status", sha, tree)
	if err != nil {
		return nil, err
	}
	var files []FileDiff
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		if status == "" || path == "" {
			continue
		}
		if len(files) >= MaxChangedFiles {
			break
		}
		fd := FileDiff{Path: path, Status: status[0]}
		patch, err := run(top, "diff-tree", "-p", "--no-renames", "--no-color", "-U3", sha, tree, "--", path)
		if err != nil {
			return nil, err
		}
		switch {
		case strings.Contains(patch, "\nBinary files ") || strings.HasPrefix(patch, "Binary files "):
			fd.Binary = true
		case fd.Status == 'M':
			fd.Hunks = ParseHunks(patch)
		}
		files = append(files, fd)
	}
	return files, nil
}

// ParseHunks extracts the hunks of a single-file unified diff.
func ParseHunks(patch string) []Hunk {
	var hunks []Hunk
	var cur *Hunk
	var last byte // side the previous line belonged to, for the no-newline marker
	for _, ln := range strings.Split(patch, "\n") {
		if strings.HasPrefix(ln, "@@") {
			h, ok := parseHunkHeader(ln)
			if !ok {
				cur = nil
				continue
			}
			h.Text = ln + "\n"
			hunks = append(hunks, h)
			cur = &hunks[len(hunks)-1]
			continue
		}
		if cur == nil || ln == "" {
			continue
		}
		cur.Text += ln + "\n"
		switch ln[0] {
		case ' ':
			cur.Old = append(cur.Old, ln[1:])
			cur.New = append(cur.New, ln[1:])
		case '-':
			cur.Old = append(cur.Old, ln[1:])
			cur.Removed++
		case '+':
			cur.New = append(cur.New, ln[1:])
			cur.Added++
		case '\\': // "\ No newline at end of file" applies to the line before
			switch last {
			case '-':
				cur.OldNoNL = true
			case '+':
				cur.NewNoNL = true
			case ' ':
				cur.OldNoNL, cur.NewNoNL = true, true
			}
			continue
		default:
			continue
		}
		last = ln[0]
	}
	return hunks
}

// parseHunkHeader reads "@@ -a[,b] +c[,d] @@ header".
func parseHunkHeader(ln string) (Hunk, bool) {
	rest := strings.TrimPrefix(ln, "@@ ")
	end := strings.Index(rest, " @@")
	if end < 0 {
		return Hunk{}, false
	}
	ranges := strings.Fields(rest[:end])
	if len(ranges) != 2 {
		return Hunk{}, false
	}
	h := Hunk{Header: strings.TrimSpace(strings.TrimPrefix(rest[end+3:], " "))}
	var ok bool
	if h.OldStart, h.OldLines, ok = parseRange(ranges[0], '-'); !ok {
		return Hunk{}, false
	}
	if h.NewStart, h.NewLines, ok = parseRange(ranges[1], '+'); !ok {
		return Hunk{}, false
	}
	return h, true
}

func parseRange(s string, sign byte) (start, n int, ok bool) {
	if len(s) < 2 || s[0] != sign {
		return 0, 0, false
	}
	start, n = 0, 1
	parts := strings.SplitN(s[1:], ",", 2)
	var err error
	if start, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, false
	}
	if len(parts) == 2 {
		if n, err = strconv.Atoi(parts[1]); err != nil {
			return 0, 0, false
		}
	}
	return start, n, true
}

// FirstChange is the 1-based line (in the new file) of the hunk's first
// changed line — past the leading context git adds around it. For a pure
// deletion it is the line the removed lines used to follow.
func (h Hunk) FirstChange() int {
	lead := 0
	for lead < len(h.Old) && lead < len(h.New) && h.Old[lead] == h.New[lead] {
		lead++
	}
	if lead >= len(h.New) { // nothing added: the change is a deletion after the context
		return max(1, h.NewStart+lead-1)
	}
	return max(1, h.NewStart+lead)
}

// Signature identifies a hunk's content, for remembering accepted hunks
// across recomputes; line numbers are left out so an accepted hunk stays
// accepted when an earlier hunk is reverted.
func (h Hunk) Signature() string {
	return strings.Join(h.Old, "\n") + "\x00" + strings.Join(h.New, "\n")
}

// TreeOf returns the tree SHA a commit (or tree) resolves to.
func TreeOf(top, rev string) (string, error) {
	return run(top, "rev-parse", rev+"^{tree}")
}

// WorktreeTree writes the current working tree (untracked included) as a
// tree object and returns its SHA, without touching the index.
func WorktreeTree(top string) (string, error) { return worktreeTree(top) }

// ApplyHunks builds the content that results from applying a subset of a
// file's hunks to its old content — what `git add -p` stages when you say
// yes to some hunks and no to others. Hunks come from one diff, so they
// don't overlap and are ordered by OldStart.
func ApplyHunks(old []byte, hunks []Hunk) []byte {
	lines := strings.Split(string(old), "\n")
	noNL := len(old) > 0 && old[len(old)-1] != '\n'
	if !noNL && len(lines) > 0 {
		lines = lines[:len(lines)-1] // the split's empty tail after the final newline
	}
	// Back to front so earlier hunks' line shifts never move later ones.
	for i := len(hunks) - 1; i >= 0; i-- {
		h := hunks[i]
		start := h.OldStart - 1
		if h.OldLines == 0 { // pure insertion: after line OldStart
			start = h.OldStart
		}
		end := start + h.OldLines
		if start < 0 || end > len(lines) {
			continue
		}
		repl := append([]string{}, h.New...)
		lines = append(lines[:start], append(repl, lines[end:]...)...)
		if end == len(lines)-len(repl)+h.OldLines { // hunk reached the old EOF: its newline rule wins
			noNL = h.NewNoNL
		}
	}
	out := strings.Join(lines, "\n")
	if !noNL && len(lines) > 0 {
		out += "\n"
	}
	return []byte(out)
}

// StageContent writes content into the index for path (repo-relative,
// slash form), whatever the index held before. The mode follows the
// worktree file's executable bit.
func StageContent(top, path string, content []byte) error {
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = top
	cmd.Stdin = strings.NewReader(string(content))
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git hash-object: %w", err)
	}
	mode := "100644"
	if fi, err := os.Stat(filepath.Join(top, filepath.FromSlash(path))); err == nil && fi.Mode()&0o111 != 0 {
		mode = "100755"
	}
	_, err = run(top, "update-index", "--add", "--cacheinfo", mode+","+strings.TrimSpace(string(out))+","+path)
	return err
}

// StagePath stages a path's worktree state: its content, or its deletion.
func StagePath(top, path string) error {
	_, err := run(top, "add", "-A", "--", path)
	return err
}

// ChangedPaths lists the paths that differ between two tree-ish objects.
func ChangedPaths(top, a, b string) (map[string]bool, error) {
	out, err := run(top, "diff-tree", "-r", "-z", "--no-renames", "--name-only", a, b)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			set[p] = true
		}
	}
	return set, nil
}
