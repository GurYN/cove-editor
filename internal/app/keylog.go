package app

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// keylog appends key events to /tmp/cove-keys.log when COVE_KEYLOG is set —
// debugging aid for PTY-level input mysteries (see CLAUDE.md).
func keylog(msg tea.Msg) {
	if os.Getenv("COVE_KEYLOG") == "" {
		return
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		f, _ := os.OpenFile("/tmp/cove-keys.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		fmt.Fprintf(f, "type=%d alt=%v runes=%q str=%q\n", k.Type, k.Alt, string(k.Runes), k.String())
		f.Close()
	}
}
