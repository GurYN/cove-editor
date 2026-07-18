package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GurYN/cove-editor/internal/app"
)

// version is stamped by the release build via -ldflags "-X main.version=…".
// date is optional (-X main.date=…): needed only for builds without a git
// checkout (Homebrew source tarball); git builds derive it from vcs.time.
var (
	version = "dev"
	date    = ""
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("cove", version)
		return
	}
	app.Version, app.Date = version, date
	// Skip the OSC background-color query: it hangs on PTYs that never
	// answer (CI, expect, some SSH hops), and nothing we render depends
	// on light/dark yet.
	lipgloss.SetHasDarkBackground(true)

	// cove          -> workspace at cwd
	// cove <dir>    -> workspace at dir
	// cove <file>   -> workspace at file's dir, file opened
	path := ""
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	var data []byte
	if path != "" {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			data, err = os.ReadFile(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, "cove:", err)
				os.Exit(1)
			}
		} else if err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "cove:", err)
			os.Exit(1)
		}
	}
	p := tea.NewProgram(app.New(path, data),
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(), // hover events: divider resize pointer needs them
		tea.WithReportFocus(),    // refresh tree + git status when the terminal regains focus
	)
	_, err := p.Run()
	os.Stdout.WriteString("\x1b]22;default\x1b\\") // restore pointer shape (OSC 22)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cove:", err)
		os.Exit(1)
	}
}
