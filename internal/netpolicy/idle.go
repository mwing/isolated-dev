package netpolicy

import (
	"io"
	"net"
	"sync"
	"time"
)

// idleTimer closes a relayed connection that has gone quiet, and only then.
//
// The deadline used to be set once, before the copies started, and nothing
// ever moved it: io.Copy does not refresh a deadline. So IdleTimeout was a
// hard lifetime — every relayed connection died ten minutes in however busy
// it was, which is a long agent session, a large clone, or ssh through the
// ProxyCommand, all cut off mid-transfer. The field said "inactivity", and
// this is what makes that true.
//
// Activity in either direction counts. A download is silent on the way up
// and a long poll is silent on the way down; treating one direction's quiet
// as idleness would kill both.
type idleTimer struct {
	idle  time.Duration
	conns []net.Conn

	mu   sync.Mutex
	last time.Time
}

func newIdleTimer(idle time.Duration, conns ...net.Conn) *idleTimer {
	t := &idleTimer{idle: idle, conns: conns}
	t.touch()
	return t
}

// touch pushes the deadline out on every connection it guards.
func (t *idleTimer) touch() {
	if t.idle <= 0 {
		return
	}
	now := time.Now()

	t.mu.Lock()
	// A syscall per packet would be the cost of exactness nobody asked for.
	// Refreshing at most every tenth of the window means a connection that
	// falls silent is closed within idle+idle/10 of its last byte, which is
	// well inside the tolerance of a value measured in minutes.
	if !t.last.IsZero() && now.Sub(t.last) < t.idle/10 {
		t.mu.Unlock()
		return
	}
	t.last = now
	t.mu.Unlock()

	deadline := now.Add(t.idle)
	for _, c := range t.conns {
		_ = c.SetDeadline(deadline)
	}
}

// reader wraps r so that bytes passing through keep the connection alive.
// Reads are where activity is observed: the bytes a copy reads are the same
// bytes it writes, so watching one end is watching both.
func (t *idleTimer) reader(r io.Reader) io.Reader {
	if t.idle <= 0 {
		return r
	}
	return &idleReader{t: t, r: r}
}

type idleReader struct {
	t *idleTimer
	r io.Reader
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.t.touch()
	}
	return n, err
}
