package app

import (
	"os"
	"path/filepath"
	"strings"
)

const maxFinderFiles = 20_000

// listFiles walks root for the fuzzy file finder, honoring the root
// .gitignore. ponytail: basename/prefix matching only, not the full
// gitignore spec; upgrade to a real gitignore lib if it misbehaves.
func listFiles(root string) []string {
	ignore := loadGitignore(root)
	var files []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || len(files) >= maxFinderFiles {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || ignore(rel, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !ignore(rel, name) {
			files = append(files, rel)
		}
		return nil
	})
	return files
}

func loadGitignore(root string) func(rel, base string) bool {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return func(string, string) bool { return false }
	}
	type pat struct {
		p        string
		anchored bool // leading "/": root-relative only, never by basename
	}
	var pats []pat
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		pats = append(pats, pat{strings.Trim(line, "/"), strings.HasPrefix(line, "/")})
	}
	return func(rel, base string) bool {
		for _, p := range pats {
			if !p.anchored {
				if ok, _ := filepath.Match(p.p, base); ok {
					return true
				}
			}
			if ok, _ := filepath.Match(p.p, rel); ok {
				return true
			}
			if strings.HasPrefix(rel, p.p+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
}
