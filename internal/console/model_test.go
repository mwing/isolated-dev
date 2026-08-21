package console

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/creack/pty"

	"github.com/mwing/isolated-dev/internal/netpolicy"
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
	if !strings.Contains(m.View(), "blocked: evil.example.com:443") {
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

// pipeTerminal returns a terminal whose "pty" is a pipe we can read, so a
// test can assert that keystrokes actually leave the model.
func pipeTerminal(t *testing.T) (*Terminal, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	term := NewTerminal(80, 24)
	term.Attach(w)
	return term, r
}

func TestKeystrokesReachTheWorkload(t *testing.T) {
	// The path that matters and was never asserted: a key pressed in the
	// console has to arrive at the workload's terminal.
	term, r := pipeTerminal(t)
	m := New("p", "allowlist", Actions{})
	m.Term = term
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})

	send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	send(m, tea.KeyMsg{Type: tea.KeyEnter})

	buf := make([]byte, 16)
	_ = r.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("nothing reached the workload: %v", err)
	}
	if got := string(buf[:n]); !strings.Contains(got, "hi") {
		t.Fatalf("workload received %q", got)
	}
}

func TestCtrlQLeaves(t *testing.T) {
	// ctrl+] alone was untypeable on a Finnish layout, where ] is
	// option+9 — the user had no way out at all.
	quit := false
	m := New("p", "allowlist", Actions{Quit: func() { quit = true }})
	m.Term = NewTerminal(80, 24)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})

	send(m, tea.KeyMsg{Type: tea.KeyCtrlQ})
	if !quit {
		t.Fatal("ctrl+q did not leave the console")
	}
}

func TestSingleCtrlCInterruptsButDoubleLeaves(t *testing.T) {
	term, r := pipeTerminal(t)
	quit := false
	m := New("p", "allowlist", Actions{Quit: func() { quit = true }})
	m.Term = term
	now := time.Unix(0, 0)
	m.Now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})

	// One press interrupts the workload rather than leaving.
	send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if quit {
		t.Fatal("a single ctrl+c left the console; it should interrupt the workload")
	}
	buf := make([]byte, 8)
	_ = r.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := r.Read(buf)
	if err != nil || string(buf[:n]) != "\x03" {
		t.Fatalf("interrupt not forwarded: %q %v", string(buf[:n]), err)
	}

	// Twice in quick succession is the reflex when something looks stuck.
	now = now.Add(300 * time.Millisecond)
	send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !quit {
		t.Fatal("double ctrl+c did not leave the console")
	}
}

func TestSlowDoubleCtrlCDoesNotLeave(t *testing.T) {
	// Two interrupts a minute apart are two interrupts, not an escape.
	term, _ := pipeTerminal(t)
	quit := false
	m := New("p", "allowlist", Actions{Quit: func() { quit = true }})
	m.Term = term
	now := time.Unix(0, 0)
	m.Now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})

	send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	now = now.Add(30 * time.Second)
	send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if quit {
		t.Fatal("two unrelated interrupts left the console")
	}
}

func TestFooterNamesAKeyThatExists(t *testing.T) {
	m := New("p", "allowlist", Actions{})
	m.Term = NewTerminal(80, 24)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if !strings.Contains(m.View(), "ctrl+q") {
		t.Errorf("footer does not name a usable escape:\n%s", m.View())
	}
}

func TestSizeSetBeforeAttachIsNotLost(t *testing.T) {
	// The container takes seconds to start, so the window size almost
	// always arrives before the pty exists. Losing that resize leaves the
	// workload drawing at the startup estimate inside a differently sized
	// emulator, which looks like a program producing no output.
	term := NewTerminal(80, 24)
	m := New("p", "allowlist", Actions{})
	m.Term = term
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})

	// The pty appears only now.
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	term.Attach(ptmx)

	ws, err := pty.GetsizeFull(ptmx)
	if err != nil {
		t.Fatal(err)
	}
	cols, rows := term.Size()
	if int(ws.Cols) != cols || int(ws.Rows) != rows {
		t.Fatalf("pty is %dx%d, emulator is %dx%d: the resize was lost",
			ws.Cols, ws.Rows, cols, rows)
	}
}

func TestFeedNeverBlocksTheWorkload(t *testing.T) {
	// The pty buffer is about a kilobyte. If whatever reads it stalls,
	// the workload's next write blocks and the program stops mid-frame
	// with no error anywhere — which is exactly how a wide terminal made
	// an agent look hung while a narrow one worked.
	term := NewTerminal(188, 44)
	sink, drain := term.Feed()
	defer drain()

	// Far more than the buffer could hold, written as fast as possible.
	chunk := []byte(strings.Repeat("x", 4096))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			if _, err := sink.Write(chunk); err != nil {
				t.Errorf("write failed: %v", err)
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("writing to the terminal blocked; a workload would stall here")
	}
}

func TestFeedStillRenders(t *testing.T) {
	term := NewTerminal(80, 24)
	sink, drain := term.Feed()
	if _, err := sink.Write([]byte("hello from the workload\r\n")); err != nil {
		t.Fatal(err)
	}
	drain() // waits for everything queued to be applied

	var found bool
	for _, l := range term.Lines() {
		if strings.Contains(l, "hello from the workload") {
			found = true
		}
	}
	if !found {
		t.Fatalf("content never reached the screen: %q", term.Lines())
	}
}

// A narrow terminal truncates the prompt's tail. The destination and the
// queue depth must survive that, because the keys are guessable and
// "which host, and how many more" is not.
func TestPromptSurvivesANarrowTerminal(t *testing.T) {
	m := newTestModel(nil)
	send(m, tea.WindowSizeMsg{Width: 46, Height: 20})
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "a.example.com", Port: 443}))
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "b.example.com", Port: 443}))

	view := m.View()
	for _, want := range []string{"a.example.com:443", "(+1 more)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q at 46 columns:\n%s", want, view)
		}
	}
}

// The header is where a long session shows its shape: the strip below only
// holds the last few lines.
func TestHeaderCountsWhatEgressDid(t *testing.T) {
	m := newTestModel(nil)
	send(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	for i := 0; i < 3; i++ {
		send(m, EventMsg(netpolicy.Event{Action: "allow", Host: "ok.example.com", Port: 443}))
	}
	send(m, EventMsg(netpolicy.Event{Action: "deny", Host: "no.example.com", Port: 443}))

	view := m.View()
	if !strings.Contains(view, "3 reached") || !strings.Contains(view, "1 blocked") {
		t.Fatalf("counts missing from the header:\n%s", strings.SplitN(view, "\n", 2)[0])
	}
}

// An unlabelled second pane reads as more program output, and the egress
// decisions in it get skipped.
func TestTheEgressStripIsLabelled(t *testing.T) {
	m := newTestModel(nil)
	send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View(), "egress") {
		t.Fatalf("no label on the divider:\n%s", m.View())
	}
	send(m, EventMsg(netpolicy.Event{Action: "pending", Host: "a.example.com", Port: 443}))
	if !strings.Contains(m.View(), "waiting for you") {
		t.Fatalf("divider does not say a question is waiting:\n%s", m.View())
	}
}

// A denial at DNS and a denial at the proxy look identical to a reader and
// are not the same event: only a request that reaches the proxy can be
// held for a decision. Reported the same way, the first reads as the
// prompt being broken — which is how it was reported from real use, where
// `curl` was asked and an npm preinstall script was not.
func TestADNSDenialSaysWhyItCouldNotBeAsked(t *testing.T) {
	m := newTestModel(nil)
	send(m, EventMsg(netpolicy.Event{Action: "deny", Host: "lith.fi", Method: "DNS",
		Reason: "not in allowlist"}))
	send(m, EventMsg(netpolicy.Event{Action: "deny", Host: "evil.example", Port: 443,
		Method: "CONNECT", Reason: "not in allowlist"}))

	view := m.View()
	if !strings.Contains(view, "not askable") {
		t.Errorf("a DNS denial does not say why no question was asked:\n%s", view)
	}
	// The proxy's denial keeps its plain form: it could have been asked, and
	// saying otherwise would be false.
	if strings.Contains(view, "evil.example:443 (asked DNS") {
		t.Errorf("a proxy denial was labelled as a DNS one:\n%s", view)
	}
}
