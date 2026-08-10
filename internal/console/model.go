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
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mwing/isolated-dev/internal/netpolicy"
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
	events []event
	// allowed and blocked count destinations for the header, which is the
	// one place a long-running session can show its shape at a glance.
	allowed int
	blocked int
	// pending is the queue of unanswered questions. A retrying client can
	// raise the same destination repeatedly; only the first is queued.
	pending []question
	asked   map[string]bool

	// repeat counts consecutive identical events, collapsed into one line.
	repeat int
	// lastInterrupt is when ctrl+c was last pressed, for the double-press
	// escape.
	lastInterrupt time.Time
	// Now is the clock, injected for tests.
	Now      func() time.Time
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

func (m *Model) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
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
			m.Term.Note(fmt.Sprintf("window %dx%d reported (raw %dx%d)",
				m.width, m.height, msg.Width, msg.Height))
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
			m.addEvent(kindWarn, "✗ could not allow "+msg.host+": "+msg.err.Error())
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
			return m, m.decide(q, DecideNo, "denied")
		}
		return m, nil
	}

	// With an interactive workload the keyboard belongs to it, or the
	// shell would be unusable: q would quit instead of typing a q. Leaving
	// therefore needs a reserved key — and it has to be one every keyboard
	// can actually produce. ctrl+] alone was a bug: on a Finnish layout ]
	// is option+9, so the combination cannot be typed at all and the user
	// had no way out.
	if m.Term != nil {
		switch key {
		case "ctrl+q", "ctrl+]":
			if m.Actions.Quit != nil {
				m.Actions.Quit()
			}
			return m, tea.Quit
		case "ctrl+c":
			// Single ctrl+c belongs to the workload: it is how an agent's
			// turn or a running command is interrupted. Twice in quick
			// succession means the user wants out, which is the reflex
			// when something appears stuck.
			now := m.now()
			if !m.lastInterrupt.IsZero() && now.Sub(m.lastInterrupt) < 1500*time.Millisecond {
				if m.Actions.Quit != nil {
					m.Actions.Quit()
				}
				return m, tea.Quit
			}
			m.lastInterrupt = now
			m.Term.Send("\x03")
			return m, nil
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
	mark := "✓ "
	if d == DecideNo {
		mark = "✗ "
	}
	kind := kindGrant
	if d == DecideNo {
		kind = kindDeny
	}
	m.addEvent(kind, mark+note+": "+q.String())

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
		m.blocked++
		m.addEvent(kindDeny, "⛔ blocked "+dest)
	case "timeout":
		m.blocked++
		m.addEvent(kindWarn, "⏱ no answer, blocked "+dest)
	case "error":
		// Allowed, but unreachable. Calling this "blocked" would send the
		// user to the allowlist to fix something it did not cause.
		m.addEvent(kindWarn, "✗ could not reach "+dest)
	case "granted":
		m.addEvent(kindGrant, "→ proceeding "+dest)
	case "allow":
		// Successful traffic is the common case. An agent opens dozens of
		// connections to the same host, and a line each buries the
		// decisions that matter — and storms the redraw, which is what
		// made the console look frozen. Repeats collapse into a count.
		if e.Method == "DNS" {
			return nil
		}
		m.allowed++
		m.addEvent(kindAllow, "· "+dest)
		return nil
	}
	m.trim()
	return nil
}

// addEvent appends a line, collapsing an immediate repeat into a count
// rather than filling the pane with identical entries.
func (m *Model) addEvent(kind eventKind, line string) {
	if n := len(m.events); n > 0 {
		if last := m.events[n-1].text; last == line || strings.HasPrefix(last, line+" ×") {
			m.repeat++
			m.events[n-1].text = fmt.Sprintf("%s ×%d", line, m.repeat+1)
			m.trim()
			return
		}
	}
	m.repeat = 0
	m.events = append(m.events, event{kind: kind, text: line})
	m.trim()
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

// eventKind decides how a line is coloured. The pane is small and mixed:
// without colour, a refusal and a successful connection read the same.
type eventKind int

const (
	kindAllow eventKind = iota
	kindDeny
	kindWarn
	kindGrant
)

type event struct {
	kind eventKind
	text string
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
	denyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	grantStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	countStyle  = lipgloss.NewStyle().Faint(true)
	// A held request is the one thing on screen that stops until it is
	// answered, so it gets the only reversed bar in the interface.
	askStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
)

// View implements tea.Model.
func (m *Model) View() string {
	var b strings.Builder

	b.WriteString(m.header())
	b.WriteString("\n")

	outputHeight := m.outputHeight()
	lines := m.output
	if m.Term != nil {
		lines = m.Term.Lines()
	}
	b.WriteString(pane(lines, outputHeight, m.width))
	b.WriteString(m.divider())
	b.WriteString("\n")
	b.WriteString(eventPane(m.events, m.eventHeight(), m.width))

	b.WriteString(m.footer())
	return b.String()
}

// header is one line: what is running on the left, what egress has done on
// the right. A long session otherwise gives no sense of its own shape —
// the event strip only shows the last few lines.
func (m *Model) header() string {
	counts := ""
	if m.allowed > 0 || m.blocked > 0 {
		counts = fmt.Sprintf("%d reached", m.allowed)
		if m.blocked > 0 {
			counts += fmt.Sprintf("  %d blocked", m.blocked)
		}
	}

	left := truncateWidth(m.Title, m.width)
	room := m.width - lipgloss.Width(left) - 2
	if room <= 0 {
		return headerStyle.Render(left)
	}

	// Counts sit at the right edge; the policy fills whatever is left,
	// because it is the less urgent of the two.
	right := truncateWidth(counts, room)
	middle := truncateWidth(m.Policy, room-lipgloss.Width(right)-2)
	gap := room - lipgloss.Width(middle) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return headerStyle.Render(left) + "  " + dimStyle.Render(middle) +
		strings.Repeat(" ", gap) + countStyle.Render(right)
}

// divider labels the strip below it. Unlabelled, the second pane reads as
// more program output, and the egress decisions in it get skipped.
func (m *Model) divider() string {
	label := " egress "
	if len(m.pending) > 0 {
		label = " egress — waiting for you "
	}
	if m.width <= lipgloss.Width(label)+4 {
		return dimStyle.Render(strings.Repeat("─", max(m.width, 0)))
	}
	rest := m.width - lipgloss.Width(label) - 2
	return dimStyle.Render("──" + label + strings.Repeat("─", max(rest, 0)))
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

// eventHeight keeps the event pane small. An agent draws its own
// interface and behaves best with a terminal close to the real one, so
// the workload gets the screen and the events get a strip — widened only
// when there is a question waiting, which is when they matter.
func (m *Model) eventHeight() int {
	if len(m.pending) > 0 {
		return 6
	}
	if m.height < 24 {
		return 2
	}
	return 4
}

// eventPane renders the egress strip, coloured by what each line means.
// The pane is small and mixed: without colour a refusal and a successful
// connection read the same, which is the one distinction it exists for.
func eventPane(events []event, n, width int) string {
	start := len(events) - n
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	shown := events[start:]
	for _, e := range shown {
		// Truncate the plain text before styling: cutting a styled string
		// slices the escape sequences and corrupts the terminal.
		b.WriteString(eventStyle(e.kind).Render(truncateWidth(e.text, width)))
		b.WriteString("\n")
	}
	for i := len(shown); i < n; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

func eventStyle(k eventKind) lipgloss.Style {
	switch k {
	case kindDeny:
		return denyStyle
	case kindWarn:
		return warnStyle
	case kindGrant:
		return grantStyle
	default:
		return dimStyle
	}
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
			extra = fmt.Sprintf(" (+%d more)", len(m.pending)-1)
		}
		// Destination and queue depth come before the keys: a narrow
		// terminal truncates the tail, and the keys are guessable while
		// "which host, and how many more" is not.
		//
		// Padded to the full width so the bar reads as a bar rather than
		// as a sentence that happens to be inverted.
		line := fmt.Sprintf(" ⛔ blocked: %s%s   [o] once   [p] this project   [n] refuse",
			q.String(), extra)
		line = truncateWidth(line, m.width)
		if pad := m.width - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		return askStyle.Render(line)
	}
	quit := "q to stop"
	if m.Term != nil {
		quit = "ctrl+q or double ctrl+c to leave"
	}
	if m.finished {
		return dimStyle.Render(truncateWidth(m.status+" — press "+quit, m.width))
	}
	return dimStyle.Render(truncateWidth(m.status+" — "+quit, m.width))
}
