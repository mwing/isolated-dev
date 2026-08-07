package console

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Recorder captures everything needed to reproduce a session's rendering:
// the bytes the workload wrote, and every size the terminal was given.
//
// A screenshot shows the result of rendering; it cannot show which size
// the workload believed it had, and that is usually the question. The
// recording can be replayed through the same emulator to see what the
// screen should have looked like.
type Recorder struct {
	mu   sync.Mutex
	f    *os.File
	enc  *json.Encoder
	zero time.Time
}

// Entry is one recorded event.
type Entry struct {
	// Millis is the offset from the start of the recording.
	Millis int64  `json:"ms"`
	Type   string `json:"type"`
	// Data is base64 workload output for "out" entries.
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
	Note string `json:"note,omitempty"`
}

// NewRecorder creates a recording at path.
func NewRecorder(path string) (*Recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("console: recording: %w", err)
	}
	return &Recorder{f: f, enc: json.NewEncoder(f), zero: time.Now()}, nil
}

func (r *Recorder) write(e Entry) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e.Millis = time.Since(r.zero).Milliseconds()
	_ = r.enc.Encode(e)
}

// Output records bytes written by the workload.
func (r *Recorder) Output(p []byte) {
	if r == nil {
		return
	}
	r.write(Entry{Type: "out", Data: base64.StdEncoding.EncodeToString(p)})
}

// Note records a plain observation, such as the window size the console
// itself was given — the number the pane size is derived from.
func (r *Recorder) Note(note string) {
	if r == nil {
		return
	}
	r.write(Entry{Type: "note", Note: note})
}

// Resize records a size the workload was given.
func (r *Recorder) Resize(cols, rows int, note string) {
	if r == nil {
		return
	}
	r.write(Entry{Type: "resize", Cols: cols, Rows: rows, Note: note})
}

// Close finishes the recording.
func (r *Recorder) Close() error {
	if r == nil || r.f == nil {
		return nil
	}
	return r.f.Close()
}

// ReplayAt renders a recording at a fixed size, ignoring the sizes it
// recorded. Comparing the two isolates a rendering fault in the emulator's
// resize handling from one in its parsing.
func ReplayAt(path string, cols, rows int) (screen []string, sizes []Entry, err error) {
	return replay(path, cols, rows)
}

// Replay renders a recording through the emulator and returns the final
// screen plus the size history, which is what a screenshot cannot show.
func Replay(path string) (screen []string, sizes []Entry, err error) {
	return replay(path, 0, 0)
}

func replay(path string, fixedCols, fixedRows int) (screen []string, sizes []Entry, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	var term *Terminal
	for {
		var e Entry
		if err := dec.Decode(&e); err != nil {
			break
		}
		switch e.Type {
		case "resize":
			sizes = append(sizes, e)
			if fixedCols > 0 {
				if term == nil {
					term = NewTerminal(fixedCols, fixedRows)
				}
				continue
			}
			if term == nil {
				term = NewTerminal(e.Cols, e.Rows)
			} else {
				term.Resize(e.Cols, e.Rows)
			}
		case "note":
			sizes = append(sizes, e)
		case "out":
			if term == nil {
				if fixedCols > 0 {
					term = NewTerminal(fixedCols, fixedRows)
				} else {
					term = NewTerminal(80, 24)
				}
			}
			raw, decErr := base64.StdEncoding.DecodeString(e.Data)
			if decErr != nil {
				continue
			}
			_, _ = term.Write(raw)
		}
	}
	if term == nil {
		return nil, sizes, fmt.Errorf("console: recording %s has no content", path)
	}
	return term.Lines(), sizes, nil
}
