package wizard

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model is the menu. It picks an action and exits; it never runs anything
// itself.
//
// Running the action outside the full-screen program is deliberate: the
// commands stream build output, ask questions and hand over a terminal,
// and a menu that tried to host all that would be a worse version of each.
type Model struct {
	Title  string
	Checks []Check
	Items  []Action

	cursor   int
	width    int
	height   int
	chosen   *Action
	quitting bool
}

// New builds the menu.
func New(title string, checks []Check, items []Action) *Model {
	return &Model{Title: title, Checks: checks, Items: items, width: 80, height: 24}
}

// Chosen is the action the user picked, or nil if they quit.
func (m *Model) Chosen() *Action { return m.chosen }

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.Items)-1 {
				m.cursor++
			}
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = len(m.Items) - 1
		case "enter", " ":
			if len(m.Items) > 0 {
				chosen := m.Items[m.cursor]
				m.chosen = &chosen
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	todoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	brokenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	commandStyle  = lipgloss.NewStyle().Faint(true).Italic(true)
)

func styleFor(s State) lipgloss.Style {
	switch s {
	case OK:
		return okStyle
	case Broken:
		return brokenStyle
	default:
		return todoStyle
	}
}

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder

	b.WriteString(titleStyle.Render(m.Title))
	b.WriteString("\n\n")

	for _, c := range m.Checks {
		b.WriteString("  ")
		b.WriteString(styleFor(c.State).Render(c.State.mark()))
		b.WriteString(" ")
		b.WriteString(c.Label)
		if c.Detail != "" {
			b.WriteString(dimStyle.Render(" — " + c.Detail))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("What next?"))
	b.WriteString("\n")

	for i, item := range m.Items {
		if i == m.cursor {
			b.WriteString(selectedStyle.Render("  ▸ " + item.Label))
		} else {
			b.WriteString("    " + item.Label)
		}
		b.WriteString("\n")
	}

	// The explanation is for the highlighted entry only. A wall of text
	// against every option is a wall of text nobody reads, and the whole
	// point is that the person here does not yet know what these do.
	if m.cursor < len(m.Items) {
		item := m.Items[m.cursor]
		b.WriteString("\n")
		for _, line := range wrap(item.Explain, m.width-4) {
			b.WriteString("  " + dimStyle.Render(line) + "\n")
		}
		if cmd := item.Command(); cmd != "" {
			b.WriteString("  " + commandStyle.Render("runs: "+cmd) + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  ↑/↓ move   enter run   q quit"))
	b.WriteString("\n")
	return b.String()
}

// wrap breaks text to a width, on word boundaries.
func wrap(s string, width int) []string {
	if width < 20 {
		width = 20
	}
	var out []string
	var line strings.Builder
	for _, word := range strings.Fields(s) {
		if line.Len() > 0 && line.Len()+1+len(word) > width {
			out = append(out, line.String())
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteString(" ")
		}
		line.WriteString(word)
	}
	if line.Len() > 0 {
		out = append(out, line.String())
	}
	return out
}
