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
