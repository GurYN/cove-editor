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
	OldNoNL, NewNoNL   bool // that side's last line has no trailing newline
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
			hunks = append(hunks, h)
			cur = &hunks[len(hunks)-1]
			continue
		}
		if cur == nil || ln == "" {
			continue
		}
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
