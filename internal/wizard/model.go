package wizard

import (
	"fmt"
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
	headingStyle  = lipgloss.NewStyle().Faint(true).Bold(true)
)

// explainHeight is the room kept for the highlighted action's explanation,
// whether or not it needs all of it.
//
// Reserved rather than fitted: the explanation changes with the cursor, and
// letting the block grow and shrink moves the footer and the list under the
// reader's eyes while they are moving through it. A menu that shifts while
// being read is harder to use than one with a gap in it.
const explainHeight = 4

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

	// The label column is padded to the widest, so the details line up in a
	// column instead of ragging along behind labels of different lengths.
	labelWidth := 0
	for _, c := range m.Checks {
		if w := lipgloss.Width(c.Label); w > labelWidth {
			labelWidth = w
		}
	}
	for _, c := range m.Checks {
		b.WriteString("  ")
		b.WriteString(styleFor(c.State).Render(c.State.mark()))
		b.WriteString("  ")
		if c.Detail == "" {
			b.WriteString(c.Label)
		} else {
			b.WriteString(c.Label)
			b.WriteString(strings.Repeat(" ", labelWidth-lipgloss.Width(c.Label)))
			b.WriteString(dimStyle.Render("  " + c.Detail))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(headingStyle.Render("What next?"))
	b.WriteString("\n")

	first, last := m.visibleRange()
	if first > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("    ↑ %d more above", first)))
		b.WriteString("\n")
	}
	for i := first; i < last; i++ {
		item := m.Items[i]
		if i == m.cursor {
			// The whole row is padded and highlighted rather than just the
			// text: a marker alone is easy to lose on a list of similar
			// lines, and the bar tracks the eye down the list.
			row := "  ▸ " + item.Label
			if pad := m.width - lipgloss.Width(row); pad > 0 {
				row += strings.Repeat(" ", pad)
			}
			b.WriteString(selectedStyle.Render(row))
		} else {
			b.WriteString("    " + item.Label)
		}
		b.WriteString("\n")
	}
	if last < len(m.Items) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("    ↓ %d more below", len(m.Items)-last)))
		b.WriteString("\n")
	}

	// The explanation is for the highlighted entry only. A wall of text
	// against every option is a wall of text nobody reads, and the whole
	// point is that the person here does not yet know what these do.
	b.WriteString("\n")
	used := 0
	if m.cursor < len(m.Items) {
		item := m.Items[m.cursor]
		cmd := item.Command()
		// The command line is the last row when there is one, so a long
		// explanation is what gets cut rather than the thing the entry is
		// teaching: that every action is a command you could have typed.
		room := explainHeight
		if cmd != "" {
			room--
		}
		lines := wrap(item.Explain, m.width-4)
		for i, line := range lines {
			if i >= room {
				break
			}
			// Say that it was cut. A sentence ending mid-clause reads as a
			// rendering fault rather than as an explanation with more to it.
			if i == room-1 && len(lines) > room {
				line += " …"
			}
			b.WriteString("  " + dimStyle.Render(line) + "\n")
			used++
		}
		if cmd != "" {
			b.WriteString("  " + commandStyle.Render("runs: "+cmd) + "\n")
			used++
		}
	}
	for i := used; i < explainHeight; i++ {
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  ↑/↓ move   enter run   q quit"))
	b.WriteString("\n")
	return b.String()
}

// visibleRange is the slice of items to draw, scrolled to keep the cursor
// on screen.
//
// Without this the list simply ran past the bottom of a short terminal: the
// entries below were unreachable in the sense that mattered, since nothing
// showed where the cursor had gone.
func (m *Model) visibleRange() (int, int) {
	room := m.itemRoom()
	if len(m.Items) <= room {
		return 0, len(m.Items)
	}
	// Keep the cursor centred where possible, and pinned at the ends.
	first := m.cursor - room/2
	if first < 0 {
		first = 0
	}
	if first+room > len(m.Items) {
		first = len(m.Items) - room
	}
	return first, first + room
}

// itemRoom is how many action rows fit once the fixed furniture is drawn:
// title, blank, the checks, the heading, the explanation block, the footer.
func (m *Model) itemRoom() int {
	const furniture = 7
	room := m.height - len(m.Checks) - explainHeight - furniture
	// The scroll indicators are furniture too. Reserved for both as soon as
	// either can appear, so the list keeps one height rather than growing by
	// a row as the cursor leaves the top.
	if len(m.Items) > room {
		room -= 2
	}
	if room < 3 {
		room = 3
	}
	return room
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
