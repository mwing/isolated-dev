// Package console renders the live view: the workload in one pane, the
// tool's own knowledge in another, and denials as questions asked while
// the request waits.
//
// It owns the screen, which is what makes the prompt possible. Outside the
// console a prompt and an interactive workload fight over stdin; here the
// prompt has its own pane and the conflict does not arise.
//
// The model holds no policy of its own. Decisions are handed back to the
// caller through Actions, so the console stays a view over the same code
// paths `run` and `shell` use rather than a second implementation of them.
package console

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mwing/isolated-dev/go/internal/netpolicy"
)

// Decision is what the user chose about a blocked destination.
type Decision int

const (
	// DecideOnce allows it for this run only.
	DecideOnce Decision = iota
	// DecideProject allows it and records the grant.
	DecideProject
	// DecideNo leaves it blocked.
	DecideNo
)

// Actions are the side effects the console asks for. They are performed by
// the caller, which owns the sidecar and the trust store.
type Actions struct {
	// Grant applies a decision about a host. It runs off the UI goroutine
	// and reports what happened for display.
	Grant func(host string, d Decision) error
	// Quit stops the workload.
	Quit func()
}

// OutputMsg is a line from the workload.
type OutputMsg string

// EventMsg is a decision the sidecar made.
type EventMsg netpolicy.Event

// RedrawMsg asks for a repaint after the workload wrote to its terminal.
type RedrawMsg struct{}

// DoneMsg reports the workload finished.
type DoneMsg struct {
	Err      error
	ExitCode int
}

// grantedMsg reports the result of applying a decision.
type grantedMsg struct {
	host string
	err  error
}

// question is a pending decision awaiting an answer.
type question struct {
	host string
	port int
}

func (q question) String() string {
	if q.port == 0 {
		return q.host
	}
	return fmt.Sprintf("%s:%d", q.host, q.port)
}

// Model is the console state.
type Model struct {
	Title   string
	Policy  string
	Actions Actions
	// Term, when set, is an interactive workload: its screen replaces the
	// line buffer and keystrokes are forwarded to it.
	Term *Terminal

	width, height int

	output []string
	events []string
	// pending is the queue of unanswered questions. A retrying client can
	// raise the same destination repeatedly; only the first is queued.
	pending []question
	asked   map[string]bool

	status   string
	finished bool
	exitCode int
	err      error
}

// New returns a console model.
func New(title, policy string, actions Actions) *Model {
	return &Model{
		Title:   title,
		Policy:  policy,
		Actions: actions,
		asked:   map[string]bool{},
		status:  "running",
		// Sensible defaults so the first frame renders before any size
		// message arrives, and stays rendered if none ever does.
		width:  80,
		height: 24,
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// A zero size means the terminal could not be measured (a pty with
		// no window size set, some CI shells). Keeping the defaults renders
		// something usable instead of waiting forever for a size that will
		// never arrive.
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		if m.Term != nil {
			m.Term.Resize(m.width, m.outputHeight())
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case RedrawMsg:
		return m, nil

	case OutputMsg:
		m.output = append(m.output, string(msg))
		m.trim()
		return m, nil

	case EventMsg:
		return m, m.handleEvent(netpolicy.Event(msg))

	case grantedMsg:
		if msg.err != nil {
			m.events = append(m.events, "✗ could not allow "+msg.host+": "+msg.err.Error())
		}
		m.trim()
		return m, nil

	case DoneMsg:
		m.finished = true
		m.err = msg.Err
		m.exitCode = msg.ExitCode
		m.status = fmt.Sprintf("finished (exit %d)", msg.ExitCode)
		if msg.Err != nil {
			m.status = "failed: " + msg.Err.Error()
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// An unanswered question takes the keyboard: leaving a request hanging
	// while keystrokes do something else is how a firewall prompt becomes
	// an annoyance rather than a control.
	if len(m.pending) > 0 {
		q := m.pending[0]
		switch key {
		case "o":
			return m, m.decide(q, DecideOnce, "allowed for this run")
		case "p":
			return m, m.decide(q, DecideProject, "allowed and recorded for this project")
		case "n", "esc":
			m.pending = m.pending[1:]
			m.events = append(m.events, "✗ denied "+q.String())
			return m, nil
		}
		return m, nil
	}

	// With an interactive workload the keyboard belongs to it, or the
	// shell would be unusable: q would quit instead of typing a q. One
	// key is reserved to leave, chosen because almost nothing binds it.
	if m.Term != nil {
		if key == "ctrl+]" {
			if m.Actions.Quit != nil {
				m.Actions.Quit()
			}
			return m, tea.Quit
		}
		m.Term.Send(keyBytes(msg))
		return m, nil
	}

	switch key {
	case "q", "ctrl+c":
		if m.Actions.Quit != nil {
			m.Actions.Quit()
		}
		return m, tea.Quit
	}
	return m, nil
}

// keyBytes renders a key press as the bytes a terminal would send.
func keyBytes(msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyRunes:
		return string(msg.Runes)
	case tea.KeySpace:
		return " "
	case tea.KeyEnter:
		return "\r"
	case tea.KeyBackspace:
		return "\x7f"
	case tea.KeyTab:
		return "\t"
	case tea.KeyEsc:
		return "\x1b"
	case tea.KeyUp:
		return "\x1b[A"
	case tea.KeyDown:
		return "\x1b[B"
	case tea.KeyRight:
		return "\x1b[C"
	case tea.KeyLeft:
		return "\x1b[D"
	case tea.KeyHome:
		return "\x1b[H"
	case tea.KeyEnd:
		return "\x1b[F"
	case tea.KeyDelete:
		return "\x1b[3~"
	case tea.KeyCtrlC:
		return "\x03"
	case tea.KeyCtrlD:
		return "\x04"
	case tea.KeyCtrlZ:
		return "\x1a"
	case tea.KeyCtrlL:
		return "\x0c"
	case tea.KeyCtrlA:
		return "\x01"
	case tea.KeyCtrlE:
		return "\x05"
	case tea.KeyCtrlU:
		return "\x15"
	case tea.KeyCtrlK:
		return "\x0b"
	case tea.KeyCtrlW:
		return "\x17"
	case tea.KeyCtrlR:
		return "\x12"
	}
	return ""
}

func (m *Model) decide(q question, d Decision, note string) tea.Cmd {
	m.pending = m.pending[1:]
	m.events = append(m.events, "✓ "+note+": "+q.String())
	m.trim()

	host := q.host
	return func() tea.Msg {
		if m.Actions.Grant == nil {
			return grantedMsg{host: host}
		}
		return grantedMsg{host: host, err: m.Actions.Grant(host, d)}
	}
}

func (m *Model) handleEvent(e netpolicy.Event) tea.Cmd {
	dest := e.Host
	if e.Port != 0 {
		dest = fmt.Sprintf("%s:%d", e.Host, e.Port)
	}
	switch e.Action {
	case "pending":
		if !m.asked[dest] {
			m.asked[dest] = true
			m.pending = append(m.pending, question{host: e.Host, port: e.Port})
		}
	case "deny":
		m.events = append(m.events, "⛔ blocked "+dest)
	case "timeout":
		m.events = append(m.events, "⏱ no answer, blocked "+dest)
	case "granted":
		m.events = append(m.events, "→ proceeding "+dest)
	case "allow":
		// Successful traffic is the common case; showing every connection
		// would bury the decisions that matter.
		if e.Method == "DNS" {
			return nil
		}
		m.events = append(m.events, "· "+dest)
	}
	m.trim()
	return nil
}

// trim bounds the buffers so a long run cannot grow memory without limit.
func (m *Model) trim() {
	const maxOutput, maxEvents = 2000, 500
	if len(m.output) > maxOutput {
		m.output = m.output[len(m.output)-maxOutput:]
	}
	if len(m.events) > maxEvents {
		m.events = m.events[len(m.events)-maxEvents:]
	}
}

// Pending reports how many questions are unanswered, for tests.
func (m *Model) Pending() int { return len(m.pending) }

// ExitCode reports the workload's status.
func (m *Model) ExitCode() int { return m.exitCode }

// Err reports a failure to run the workload at all.
func (m *Model) Err() error { return m.err }

var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	askStyle    = lipgloss.NewStyle().Bold(true)
)

// View implements tea.Model.
func (m *Model) View() string {
	var b strings.Builder

	// Truncate the plain text, then style it: cutting an already-styled
	// string slices the escape sequences and corrupts the terminal.
	b.WriteString(headerStyle.Render(truncateWidth(m.Title, m.width)))
	if rest := m.width - lipgloss.Width(m.Title) - 2; rest > 0 {
		b.WriteString("  ")
		b.WriteString(dimStyle.Render(truncateWidth(m.Policy, rest)))
	}
	b.WriteString("\n")

	outputHeight := m.outputHeight()
	lines := m.output
	if m.Term != nil {
		lines = m.Term.Lines()
	}
	b.WriteString(pane(lines, outputHeight, m.width))
	b.WriteString(dimStyle.Render(strings.Repeat("─", max(m.width, 0))))
	b.WriteString("\n")
	b.WriteString(pane(m.events, m.eventHeight(), m.width))

	b.WriteString(m.footer())
	return b.String()
}

// outputHeight is the room left for the workload after the header, the
// event pane and the footer.
func (m *Model) outputHeight() int {
	events := m.eventHeight()
	h := m.height - events - 4
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) eventHeight() int {
	if m.height < 24 {
		return 4
	}
	return 8
}

// pane renders the last n lines, padding so the layout does not jump as
// content arrives.
func pane(lines []string, n, width int) string {
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	shown := lines[start:]
	for _, l := range shown {
		b.WriteString(truncateWidth(l, width))
		b.WriteString("\n")
	}
	for i := len(shown); i < n; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

// truncateWidth cuts a line to a display width. Slicing bytes would split
// a multibyte rune and leave the terminal rendering a replacement
// character for the rest of the line.
func truncateWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := lipgloss.Width(string(r))
		if used+w > width {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String()
}

func (m *Model) footer() string {
	if len(m.pending) > 0 {
		q := m.pending[0]
		extra := ""
		if len(m.pending) > 1 {
			extra = fmt.Sprintf("  (+%d more)", len(m.pending)-1)
		}
		return askStyle.Render(truncateWidth(fmt.Sprintf(
			"⛔ %s blocked, waiting.  [o] once  [p] project  [n] no%s",
			q.String(), extra), m.width))
	}
	quit := "q to stop"
	if m.Term != nil {
		quit = "ctrl+] to leave"
	}
	if m.finished {
		return dimStyle.Render(truncateWidth(m.status+" — press "+quit, m.width))
	}
	return dimStyle.Render(truncateWidth(m.status+" — "+quit, m.width))
}
