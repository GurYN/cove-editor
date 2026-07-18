package app

import (
	"runtime/debug"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Version and Date are set by cmd/cove from its ldflags-stamped values;
// Date is empty unless stamped (git builds fall back to vcs.time).
var (
	Version = "dev"
	Date    = ""
)

var aboutBoxStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("62")).
	Padding(1, 4).
	Align(lipgloss.Center)

// buildDate is the commit date Go embeds via -buildvcs. Release builds
// check out the tag, so the tagged commit's date is the release date.
// Empty when built outside a git checkout (e.g. from a source tarball).
func buildDate() string {
	if Date != "" {
		return Date
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.time" && len(s.Value) >= 10 {
				return s.Value[:10] // RFC3339 → YYYY-MM-DD
			}
		}
	}
	return ""
}

// aboutLogo is a figlet-small "COVE"; each row gets one fade color.
var (
	aboutLogo = []string{
		`  ___ _____   _____ `,
		` / __/ _ \ \ / / __|`,
		`| (_| (_) \ V /| _| `,
		` \___\___/ \_/ |___|`,
	}
	aboutFade     = []string{"117", "111", "105", "99"} // cyan → purple, top to bottom
	aboutVerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	aboutDimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func (m Model) aboutView() string {
	var sb strings.Builder
	for i, l := range aboutLogo {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(aboutFade[i])).Render(l))
		sb.WriteByte('\n')
	}
	sb.WriteString("\n" + welcomeStyle.Render("a GUI-native terminal IDE") + "\n\n")
	line := aboutVerStyle.Render(Version)
	if d := buildDate(); d != "" {
		line += welcomeStyle.Render("  ·  released " + d)
	}
	sb.WriteString(line + "\n\n")
	sb.WriteString(aboutDimStyle.Render("any key to close"))
	return aboutBoxStyle.Render(sb.String())
}
