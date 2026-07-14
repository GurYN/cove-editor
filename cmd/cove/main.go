package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GurYN/cove-editor/internal/app"
)

// version is stamped by the release build via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("cove", version)
		return
	}
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
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "cove:", err)
		os.Exit(1)
	}
}
