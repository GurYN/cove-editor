// Package git shells out to the git binary — status, staging, diff, commit,
// branches, push/pull. No library dependency; slow calls (push/pull) are the
// caller's job to run off the UI thread (a tea.Cmd goroutine).
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// FileStatus is one changed path from porcelain v2. Index/Work hold the XY
// letters ('.' = unchanged, '?' = untracked, '!' = conflict).
type FileStatus struct {
	Path        string // repo-top-relative
	Index, Work byte
}

func (f FileStatus) Staged() bool    { return f.Index != '.' && f.Index != '?' && f.Index != '!' }
func (f FileStatus) Unstaged() bool  { return f.Work != '.' }
func (f FileStatus) Untracked() bool { return f.Index == '?' }
func (f FileStatus) Conflict() bool  { return f.Index == '!' }

// Snapshot is everything the UI needs, produced by one Status call.
type Snapshot struct {
	Top           string // repo top-level dir, absolute
	Branch        string
	Upstream      string
	Ahead, Behind int
	Files         []FileStatus
}

// run executes git in dir and returns trimmed stdout. Credential prompts are
// disabled so a background call can never hang waiting for a password.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git: %s", firstLine(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// runLoose is for push/pull/checkout, whose human-facing result often lands
// on stderr even on success. Combined output, 60s cap.
func runLoose(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s != "" {
			return "", fmt.Errorf("git: %s", firstLine(s))
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return s, nil
}

// Top resolves the repo's top-level directory; errors when dir is not
// inside a git work tree.
func Top(dir string) (string, error) { return run(dir, "rev-parse", "--show-toplevel") }

// Status parses `git status --porcelain=v2 --branch` into a Snapshot.
func Status(top string) (Snapshot, error) {
	snap := Snapshot{Top: top}
	// -uall: list files inside untracked directories, not a collapsed "dir/"
	out, err := run(top, "status", "--porcelain=v2", "--branch", "--untracked-files=all")
	if err != nil {
		return snap, err
	}
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case ln == "":
		case strings.HasPrefix(ln, "# branch.head "):
			snap.Branch = ln[len("# branch.head "):]
		case strings.HasPrefix(ln, "# branch.upstream "):
			snap.Upstream = ln[len("# branch.upstream "):]
		case strings.HasPrefix(ln, "# branch.ab "):
			fmt.Sscanf(ln, "# branch.ab +%d -%d", &snap.Ahead, &snap.Behind)
		case strings.HasPrefix(ln, "1 "):
			if f := strings.SplitN(ln, " ", 9); len(f) == 9 {
				snap.Files = append(snap.Files, FileStatus{Path: f[8], Index: f[1][0], Work: f[1][1]})
			}
		case strings.HasPrefix(ln, "2 "): // rename: path field is "new\told"
			if f := strings.SplitN(ln, " ", 10); len(f) == 10 {
				path, _, _ := strings.Cut(f[9], "\t")
				snap.Files = append(snap.Files, FileStatus{Path: path, Index: f[1][0], Work: f[1][1]})
			}
		case strings.HasPrefix(ln, "? "):
			snap.Files = append(snap.Files, FileStatus{Path: ln[2:], Index: '?', Work: '?'})
		case strings.HasPrefix(ln, "u "):
			if f := strings.SplitN(ln, " ", 11); len(f) == 11 {
				snap.Files = append(snap.Files, FileStatus{Path: f[10], Index: '!', Work: '!'})
			}
		}
	}
	sort.Slice(snap.Files, func(i, j int) bool { return snap.Files[i].Path < snap.Files[j].Path })
	return snap, nil
}

func Stage(top, path string) error { _, err := run(top, "add", "--", path); return err }
func StageAll(top string) error    { _, err := run(top, "add", "-A"); return err }

// Unstage falls back to rm --cached for repos with no commits yet (restore
// --staged needs a HEAD to restore from).
func Unstage(top, path string) error {
	if _, err := run(top, "restore", "--staged", "--", path); err != nil {
		_, err = run(top, "rm", "--cached", "-q", "--", path)
		return err
	}
	return nil
}

func UnstageAll(top string) error {
	if _, err := run(top, "reset", "-q"); err != nil {
		_, err = run(top, "rm", "--cached", "-r", "-q", "--", ".")
		return err
	}
	return nil
}

// Diff returns the unified diff for one path. Untracked files diff against
// /dev/null (git exits 1 there by design — the output is still the diff).
func Diff(top, path string, staged bool) (string, error) {
	if staged {
		return run(top, "diff", "--cached", "--", path)
	}
	return run(top, "diff", "--", path)
}

func DiffUntracked(top, path string) string {
	cmd := exec.Command("git", "diff", "--no-index", "--", os.DevNull, path)
	cmd.Dir = top
	out, _ := cmd.Output()
	return string(out)
}

func Commit(top, msg string) (string, error) { return run(top, "commit", "-m", msg) }

func Push(top string) (string, error) { return runLoose(top, "push") }
func Pull(top string) (string, error) { return runLoose(top, "pull") }

func Branches(top string) ([]string, error) {
	out, err := run(top, "branch", "--format=%(refname:short)")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

func Checkout(top, name string) error {
	_, err := runLoose(top, "checkout", name)
	return err
}

func CreateBranch(top, name string) error {
	_, err := runLoose(top, "checkout", "-b", name)
	return err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
