package console

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mwing/isolated-dev/go/internal/netpolicy"
)

func newTestModel(grant func(string, Decision) error) *Model {
	m := New("proj", "allowlist", Actions{Grant: grant})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return m
}

func send(m *Model, msg tea.Msg) tea.Cmd {
	_, cmd := m.Update(msg)
	return cmd
}

func TestPendingEventBecomesAQuestion(t *testing.T) {
	m := newTestModel(nil)
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "evil.example.com", Port: 443}))

	if m.Pending() != 1 {
		t.Fatalf("pending = %d", m.Pending())
	}
	if !strings.Contains(m.View(), "evil.example.com:443 blocked, waiting") {
		t.Errorf("question not shown:\n%s", m.View())
	}
}

func TestAnswerAppliesTheDecision(t *testing.T) {
	var gotHost string
	var gotDecision Decision
	m := newTestModel(func(host string, d Decision) error {
		gotHost, gotDecision = host, d
		return nil
	})
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "late.example.com", Port: 443}))

	cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd == nil {
		t.Fatal("no command produced for the answer")
	}
	cmd() // the grant runs off the UI goroutine

	if gotHost != "late.example.com" || gotDecision != DecideProject {
		t.Fatalf("grant = %q %v", gotHost, gotDecision)
	}
	if m.Pending() != 0 {
		t.Error("question stayed pending after being answered")
	}
}

func TestDenyLeavesItBlocked(t *testing.T) {
	called := false
	m := newTestModel(func(string, Decision) error {
		called = true
		return nil
	})
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "no.example.com", Port: 443}))
	send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if called {
		t.Error("denying granted anyway")
	}
	if m.Pending() != 0 {
		t.Error("question not cleared")
	}
	if !strings.Contains(m.View(), "denied") {
		t.Errorf("denial not shown:\n%s", m.View())
	}
}

func TestQuestionTakesTheKeyboard(t *testing.T) {
	// Leaving a request hanging while keystrokes do something else is how
	// a firewall prompt becomes an annoyance rather than a control.
	quit := false
	m := New("p", "allowlist", Actions{Quit: func() { quit = true }})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "x.example.com", Port: 443}))

	send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if quit {
		t.Error("q quit the console while a question was waiting")
	}
	if m.Pending() != 1 {
		t.Error("question lost")
	}
}

func TestRepeatedPendingAsksOnce(t *testing.T) {
	// A retrying client raises the same destination repeatedly.
	m := newTestModel(nil)
	for i := 0; i < 5; i++ {
		send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "same.example.com", Port: 443}))
	}
	if m.Pending() != 1 {
		t.Fatalf("pending = %d, want one question for five events", m.Pending())
	}
}

func TestDistinctDestinationsQueue(t *testing.T) {
	m := newTestModel(nil)
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "a.example.com", Port: 443}))
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "b.example.com", Port: 443}))

	if m.Pending() != 2 {
		t.Fatalf("pending = %d", m.Pending())
	}
	if !strings.Contains(m.View(), "(+1 more)") {
		t.Errorf("queue depth not shown:\n%s", m.View())
	}

	send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if !strings.Contains(m.View(), "b.example.com") {
		t.Errorf("second question not promoted:\n%s", m.View())
	}
}

func TestGrantFailureIsShown(t *testing.T) {
	m := newTestModel(func(string, Decision) error {
		return errors.New("sidecar gone")
	})
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "x.example.com", Port: 443}))
	cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	msg := cmd()
	send(m, msg)

	if !strings.Contains(m.View(), "sidecar gone") {
		t.Errorf("failure not surfaced:\n%s", m.View())
	}
}

func TestWorkloadOutputIsShown(t *testing.T) {
	m := newTestModel(nil)
	send(m, OutputMsg("hello from the container"))
	if !strings.Contains(m.View(), "hello from the container") {
		t.Errorf("output missing:\n%s", m.View())
	}
}

func TestSuccessfulDNSIsNotLogged(t *testing.T) {
	// Every resolution would bury the decisions that matter.
	m := newTestModel(nil)
	send(m, EventMsg(netpolicy.Event{Action: "allow", Host: "ok.example.com", Method: "DNS"}))
	if strings.Contains(m.View(), "ok.example.com") {
		t.Errorf("routine DNS noise shown:\n%s", m.View())
	}
}

func TestRendersWithoutAMeasuredTerminal(t *testing.T) {
	// Some ptys report no size. Waiting for one that never arrives would
	// leave the user staring at a blank console.
	m := New("proj", "allowlist", Actions{})
	m.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	send(m, OutputMsg("output before any size was known"))

	if !strings.Contains(m.View(), "output before any size was known") {
		t.Fatalf("nothing rendered without a size:\n%s", m.View())
	}
}

func TestDoneQuits(t *testing.T) {
	m := newTestModel(nil)
	cmd := send(m, DoneMsg{ExitCode: 2})
	if cmd == nil {
		t.Fatal("finishing did not quit")
	}
	if m.ExitCode() != 2 {
		t.Errorf("exit code = %d", m.ExitCode())
	}
	if !strings.Contains(m.View(), "exit 2") {
		t.Errorf("status not shown:\n%s", m.View())
	}
}

func TestMultibyteOutputIsNotSplitMidRune(t *testing.T) {
	// Slicing bytes to fit the width would leave the terminal rendering a
	// replacement character for the rest of the line.
	m := newTestModel(nil)
	send(m, OutputMsg(strings.Repeat("日本語", 100)))
	if strings.Contains(m.View(), "\uFFFD") {
		t.Error("a rune was split by truncation")
	}
}

func TestBuffersAreBounded(t *testing.T) {
	m := newTestModel(nil)
	for i := 0; i < 5000; i++ {
		send(m, OutputMsg("line"))
	}
	if len(m.output) > 2000 {
		t.Fatalf("output buffer grew to %d", len(m.output))
	}
}

func TestLayoutFitsTheTerminal(t *testing.T) {
	m := newTestModel(nil)
	for i := 0; i < 100; i++ {
		send(m, OutputMsg(strings.Repeat("x", 200)))
	}
	send(m, OutputMsg(strings.Repeat("日本語テキスト", 40)))
	// Measure display width, not bytes: the separator uses a 3-byte rune,
	// and styling adds escape sequences that occupy no columns.
	for _, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(line); w > 80 {
			t.Fatalf("line overflows the terminal width: %d columns", w)
		}
	}
}

func TestKeysGoToAnInteractiveWorkload(t *testing.T) {
	// With a shell in the pane the keyboard belongs to it, or q would quit
	// the console instead of typing a q.
	quit := false
	m := New("p", "allowlist", Actions{Quit: func() { quit = true }})
	m.Term = NewTerminal(80, 24)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})

	send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if quit {
		t.Fatal("q quit the console instead of reaching the shell")
	}
}

func TestReservedKeyLeavesAnInteractiveWorkload(t *testing.T) {
	quit := false
	m := New("p", "allowlist", Actions{Quit: func() { quit = true }})
	m.Term = NewTerminal(80, 24)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})

	send(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	if !quit {
		t.Fatal("the reserved key did not leave the console")
	}
}

func TestAQuestionOutranksTheShell(t *testing.T) {
	// A blocked request is waiting; answering it must not be typed into
	// the shell instead.
	var granted bool
	m := New("p", "allowlist", Actions{Grant: func(string, Decision) error {
		granted = true
		return nil
	}})
	m.Term = NewTerminal(80, 24)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "x.example.com", Port: 443}))

	cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("the answer did not reach the question")
	}
	cmd()
	if !granted {
		t.Error("answer was typed into the shell instead of answering")
	}
}

func TestTerminalRendersItsScreen(t *testing.T) {
	m := New("p", "allowlist", Actions{})
	m.Term = NewTerminal(80, 24)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})

	if _, err := m.Term.Write([]byte("user@box:/workspace$ echo hi\r\nhi\r\n")); err != nil {
		t.Fatal(err)
	}
	view := m.View()
	if !strings.Contains(view, "user@box:/workspace$ echo hi") {
		t.Errorf("terminal screen not rendered:\n%s", view)
	}
}

func TestTerminalIgnoresEscapeSequencesInLayout(t *testing.T) {
	// The emulator exists so cursor movement and colour do not land in the
	// middle of the console's own layout.
	m := New("p", "allowlist", Actions{})
	m.Term = NewTerminal(80, 24)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})

	if _, err := m.Term.Write([]byte("\x1b[31mred\x1b[0m\x1b[2J\x1b[Hclean")); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "\x1b[2J") || strings.Contains(line, "\x1b[31m") {
			t.Fatalf("raw escape sequence reached the layout: %q", line)
		}
	}
	if !strings.Contains(m.View(), "clean") {
		t.Errorf("screen content missing after a clear:\n%s", m.View())
	}
}

func TestRepeatedAllowsCollapseIntoACount(t *testing.T) {
	// An agent opens dozens of connections to the same host. A line each
	// buries the decisions that matter and storms the redraw, which is
	// what made the console look frozen.
	m := newTestModel(nil)
	for i := 0; i < 50; i++ {
		send(m, EventMsg(netpolicy.Event{Action: "allow", Host: "mcp-proxy.anthropic.com", Port: 443}))
	}
	if len(m.events) != 1 {
		t.Fatalf("events = %d, want one collapsed line", len(m.events))
	}
	if !strings.Contains(m.View(), "×50") {
		t.Errorf("count not shown:\n%s", m.View())
	}
}

func TestDistinctEventsStillAppear(t *testing.T) {
	m := newTestModel(nil)
	send(m, EventMsg(netpolicy.Event{Action: "allow", Host: "a.example.com", Port: 443}))
	send(m, EventMsg(netpolicy.Event{Action: "allow", Host: "b.example.com", Port: 443}))
	if len(m.events) != 2 {
		t.Fatalf("events = %v", m.events)
	}
}

func TestDenyReachesTheSidecar(t *testing.T) {
	// A "no" answered only in the UI leaves the sidecar holding every
	// retry for the full timeout.
	var gotDecision Decision
	var called bool
	m := newTestModel(func(_ string, d Decision) error {
		called, gotDecision = true, d
		return nil
	})
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "no.example.com", Port: 443}))

	cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd == nil {
		t.Fatal("denying produced no action")
	}
	cmd()
	if !called || gotDecision != DecideNo {
		t.Fatalf("sidecar not told about the denial (called=%v, decision=%v)", called, gotDecision)
	}
}

func TestResizeReachesTheWorkloadsTerminal(t *testing.T) {
	// Resizing only the emulator leaves the program drawing at its old
	// size: a wide window then shows a narrow column of output and a large
	// blank gap, which reads as a frozen console rather than a mis-sized
	// one.
	m := New("p", "allowlist", Actions{})
	m.Term = NewTerminal(80, 24)
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})

	cols, rows := m.Term.Size()
	if cols != 200 {
		t.Errorf("terminal width = %d, want the pane width 200", cols)
	}
	if rows != m.outputHeight() {
		t.Errorf("terminal height = %d, want the pane height %d", rows, m.outputHeight())
	}
	if rows <= 24 {
		t.Errorf("height %d did not grow with the window", rows)
	}
}

func TestNarrowWindowStillLeavesAUsablePane(t *testing.T) {
	m := New("p", "allowlist", Actions{})
	m.Term = NewTerminal(80, 24)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})

	if _, rows := m.Term.Size(); rows < 3 {
		t.Errorf("pane collapsed to %d rows", rows)
	}
}
