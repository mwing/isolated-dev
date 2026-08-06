package netpolicy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Notice is a policy denial worth telling the user about right now, rather
// than at exit. A blocked destination mid-run is actionable — the user can
// decide whether to allow it — but only if they hear about it while it is
// still happening.
type Notice struct {
	Destination string
	Method      string
	Reason      string
	Count       int
	First       time.Time
}

// String renders a notice for a terminal.
func (n Notice) String() string {
	switch {
	case n.Method == "DNS":
		return fmt.Sprintf("blocked DNS lookup: %s", n.Destination)
	case n.Count > 1:
		return fmt.Sprintf("blocked: %s (x%d)", n.Destination, n.Count)
	default:
		return fmt.Sprintf("blocked: %s", n.Destination)
	}
}

// Watcher turns a stream of sidecar events into live notices.
//
// It deduplicates by destination: the first hit is reported immediately,
// repeats are counted silently. A retrying client can produce hundreds of
// identical denials per second, and a notifier that prints all of them is
// one the user turns off — at which point it protects nobody.
type Watcher struct {
	// Notify receives each new destination the moment it is first denied.
	Notify func(Notice)
	// RepeatAfter re-notifies about a destination that keeps being hit
	// this long after the previous notice. Zero disables re-notification.
	RepeatAfter time.Duration
	// Now is the clock, injected for tests.
	Now func() time.Time

	mu    sync.Mutex
	seen  map[string]*Notice
	total map[string]int
}

// NewWatcher returns a watcher that calls notify.
func NewWatcher(notify func(Notice)) *Watcher {
	return &Watcher{
		Notify:      notify,
		RepeatAfter: 60 * time.Second,
		seen:        map[string]*Notice{},
		total:       map[string]int{},
	}
}

func (w *Watcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Run reads JSON event lines until the stream ends. It is intended to run
// in a goroutine alongside the workload, reading `docker logs --follow`.
func (w *Watcher) Run(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		w.Observe(e)
	}
	return sc.Err()
}

// Observe processes one event, notifying when appropriate.
func (w *Watcher) Observe(e Event) {
	if e.Action != "deny" {
		return
	}
	key := destinationKey(e)

	w.mu.Lock()
	if w.seen == nil {
		w.seen = map[string]*Notice{}
		w.total = map[string]int{}
	}
	w.total[key]++
	prev, known := w.seen[key]
	now := w.now()

	var out *Notice
	switch {
	case !known:
		n := Notice{Destination: key, Method: e.Method, Reason: e.Reason, Count: 1, First: now}
		w.seen[key] = &n
		cp := n
		out = &cp
	case w.RepeatAfter > 0 && now.Sub(prev.First) >= w.RepeatAfter:
		prev.First = now
		cp := *prev
		cp.Count = w.total[key]
		out = &cp
	}
	w.mu.Unlock()

	if out != nil && w.Notify != nil {
		w.Notify(*out)
	}
}

// Totals returns the full denial tally, including suppressed repeats.
func (w *Watcher) Totals() map[string]int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]int, len(w.total))
	for k, v := range w.total {
		out[k] = v
	}
	return out
}

func destinationKey(e Event) string {
	if e.Method == "DNS" {
		return e.Host
	}
	if e.Port != 0 {
		return fmt.Sprintf("%s:%d", e.Host, e.Port)
	}
	return e.Host
}
