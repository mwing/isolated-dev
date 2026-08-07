package console

import (
	"io"
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
	// Rec captures output and sizes for later replay.
	Rec  *Recorder
	mu   sync.Mutex
	vt   vt10x.Terminal
	rows int
	cols int
	// out is the master side of the pty, written to when keys are pressed.
	out *os.File
}

// Feed decouples reading the workload's pty from rendering it.
//
// Whatever reads the pty must never stall, because the pty buffer is about
// a kilobyte: once it fills, the workload's next write blocks and the
// program stops mid-frame with no error anywhere. Rendering, styling and
// writing frames to the user's terminal are all downstream of that read
// and any of them can be slow, so the read hands bytes to a buffered
// channel and returns immediately.
func (t *Terminal) Feed() (io.Writer, func()) {
	ch := make(chan []byte, 4096)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for b := range ch {
			t.mu.Lock()
			_, _ = t.vt.Write(b)
			t.mu.Unlock()
		}
	}()

	w := &feedWriter{ch: ch, rec: t}
	return w, func() {
		close(ch)
		<-done
	}
}

type feedWriter struct {
	ch  chan []byte
	rec *Terminal
	// mu guards against a close racing a write on the way out.
	mu     sync.Mutex
	closed bool
}

func (w *feedWriter) Write(p []byte) (int, error) {
	b := make([]byte, len(p))
	copy(b, p)
	w.rec.Rec.Output(b)

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return len(p), nil
	}
	select {
	case w.ch <- b:
	default:
		// The buffer is full only if rendering has fallen far behind.
		// Blocking here would stall the workload, so the oldest pending
		// chunk is dropped instead: a briefly wrong screen redraws, a
		// blocked workload does not recover.
		select {
		case <-w.ch:
		default:
		}
		select {
		case w.ch <- b:
		default:
		}
	}
	return len(p), nil
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

	t.Rec.Resize(cols, rows, "attach")
	_ = pty.Setsize(f, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Write feeds workload output into the emulator.
func (t *Terminal) Write(p []byte) (int, error) {
	t.Rec.Output(p)
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

	t.Rec.Resize(cols, rows, "resize")
	if out != nil {
		_ = pty.Setsize(out, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	}
}

// Note records an observation in the session recording.
func (t *Terminal) Note(note string) { t.Rec.Note(note) }

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
