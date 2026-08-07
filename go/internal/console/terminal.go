package console

import (
	"os"
	"strings"
	"sync"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// Terminal is the screen of an interactive workload.
//
// The console cannot simply pass bytes through: it owns the screen and
// draws its own panes, so the workload's output has to be interpreted
// rather than echoed. A shell emits cursor movement, colours and full
// redraws; without a terminal emulator those escape sequences would land
// in the middle of the console's layout and corrupt it.
type Terminal struct {
	mu   sync.Mutex
	vt   vt10x.Terminal
	rows int
	cols int
	// out is the master side of the pty, written to when keys are pressed.
	out *os.File
}

// NewTerminal returns an emulator of the given size.
func NewTerminal(cols, rows int) *Terminal {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return &Terminal{
		vt:   vt10x.New(vt10x.WithSize(cols, rows)),
		cols: cols,
		rows: rows,
	}
}

// Attach records the pty to send keystrokes to, and applies the size the
// emulator already has.
//
// The container takes seconds to start, so the window size almost always
// arrives before the pty exists. Without applying it here that resize is
// lost: the emulator ends up at the real size while the workload keeps
// drawing at whatever it was given at startup, filling part of a wider
// screen and leaving the rest blank — which looks like a program producing
// no output.
func (t *Terminal) Attach(f *os.File) {
	t.mu.Lock()
	t.out = f
	cols, rows := t.cols, t.rows
	t.mu.Unlock()

	_ = pty.Setsize(f, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Write feeds workload output into the emulator.
func (t *Terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.vt.Write(p)
}

// Send forwards keystrokes to the workload.
func (t *Terminal) Send(s string) {
	t.mu.Lock()
	out := t.out
	t.mu.Unlock()
	if out == nil {
		return
	}
	_, _ = out.WriteString(s)
}

// Resize changes the emulated screen size and tells the workload about it.
//
// Both halves matter. Resizing only the emulator leaves the program
// drawing at its old size, so a wide window shows a narrow column of
// output and a large blank gap — which reads as a frozen console rather
// than a mis-sized one.
func (t *Terminal) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	t.mu.Lock()
	t.cols, t.rows = cols, rows
	t.vt.Resize(cols, rows)
	out := t.out
	t.mu.Unlock()

	if out != nil {
		_ = pty.Setsize(out, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	}
}

// Size reports the current dimensions.
func (t *Terminal) Size() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows
}

// Lines renders the emulated screen as plain text, one string per row.
//
// Attributes are dropped deliberately: reproducing colour inside a pane
// means re-emitting escape sequences that the console's own styling then
// has to reason about. Legible text in the right place is worth more here
// than colour, and the workload's own output remains available in full
// through `dev2 run`.
func (t *Terminal) Lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]string, 0, t.rows)
	for y := 0; y < t.rows; y++ {
		var b strings.Builder
		for x := 0; x < t.cols; x++ {
			b.WriteRune(t.vt.Cell(x, y).Char)
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

// CursorRow reports the cursor's row, so the view can scroll to it.
func (t *Terminal) CursorRow() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.vt.Cursor().Y
}
