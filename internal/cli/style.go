package cli

import "github.com/charmbracelet/lipgloss"

// Styling for the reporting commands.
//
// lipgloss resolves the colour profile from the real standard output, so a
// run whose output is a pipe or a file emits none of this. That is the
// property that matters here: `dev doctor > report.txt` and `dev status |
// grep` have to stay readable, and a status line that only parses when a
// human is watching is worse than an unstyled one.
//
// Colour carries meaning rather than decoration — a mark says what state
// something is in, and the same colour means the same thing everywhere:
// green passed, red blocks a run, yellow is worth reading but does not,
// dim is context for the line it sits on.
var (
	sectionStyle = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	failStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	arrowStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)
