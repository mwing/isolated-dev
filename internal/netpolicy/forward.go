package netpolicy

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Forward publishes a workload port to the host.
//
// A workload on an internal network cannot publish ports itself: docker
// needs a gateway to publish through and an internal network has none.
// Removing the internal network to get ports back would remove the egress
// control with it, so the sidecar forwards instead. It already straddles
// both networks, and having one component own the whole network boundary
// is easier to reason about than two.
//
// Inbound is not subject to the egress allowlist. The allowlist answers
// "what may this workload reach"; a published port answers "what may reach
// this workload", and the user answered that by asking for the port.
const (
	// forwardDialTimeout bounds reaching the workload. It is on the same
	// host: it answers immediately or it is not listening.
	forwardDialTimeout = 10 * time.Second
	// defaultForwardIdle is long because a published port carries
	// websockets, debugger sessions and long polls that are legitimately
	// silent. This bounds a wedged connection without touching a working
	// one, which is why the proxy's ten minutes would be wrong here.
	defaultForwardIdle = time.Hour
)

type Forward struct {
	// ListenPort is the port the sidecar listens on, published to the host.
	ListenPort int
	// Target is the workload's address on the internal network, resolved
	// by container name through docker's embedded DNS.
	TargetHost string
	TargetPort int
	// IdleTimeout closes a forwarded connection that has gone quiet in both
	// directions. Zero uses defaultForwardIdle.
	IdleTimeout time.Duration

	ln        net.Listener
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error

	// live are the connections being relayed right now, and closing is set
	// once teardown starts. Close needs both: a copy blocked reading an idle
	// connection does not notice a closed listener, and waiting for it to
	// notice is waiting forever.
	mu      sync.Mutex
	live    map[net.Conn]struct{}
	closing bool
}

// closeWait bounds how long teardown waits for relays to finish after their
// connections have been closed under them. Long enough for a copy to return,
// short enough that nothing wedged can hold up a `dev clean`.
const closeWait = 5 * time.Second

// ParseForward reads a "listen:host:target" specification.
func ParseForward(spec string) (*Forward, error) {
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("netpolicy: forward %q: want listen:host:port", spec)
	}
	listen, err := strconv.Atoi(parts[0])
	if err != nil || listen < 1 || listen > 65535 {
		return nil, fmt.Errorf("netpolicy: forward %q: bad listen port", spec)
	}
	target, err := strconv.Atoi(parts[2])
	if err != nil || target < 1 || target > 65535 {
		return nil, fmt.Errorf("netpolicy: forward %q: bad target port", spec)
	}
	if strings.TrimSpace(parts[1]) == "" {
		return nil, fmt.Errorf("netpolicy: forward %q: no target host", spec)
	}
	return &Forward{ListenPort: listen, TargetHost: parts[1], TargetPort: target}, nil
}

// String renders the forward as it was specified.
func (f *Forward) String() string {
	return fmt.Sprintf("%d:%s:%d", f.ListenPort, f.TargetHost, f.TargetPort)
}

// Listen starts accepting connections.
func (f *Forward) Listen() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", f.ListenPort))
	if err != nil {
		return fmt.Errorf("netpolicy: forward %s: %w", f, err)
	}
	f.ln = ln
	go f.accept()
	return nil
}

func (f *Forward) accept() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		// Counted and registered together, under the lock teardown takes.
		// A connection accepted a moment before Close has to be either
		// fully tracked or never started: adding to the WaitGroup after the
		// wait has begun is a race, and a relay that started without being
		// registered is one Close cannot interrupt.
		if !f.hold(conn, true) {
			_ = conn.Close()
			return
		}
		go func() {
			defer f.release(conn, true)
			f.handle(conn)
		}()
	}
}

func (f *Forward) handle(client net.Conn) {
	target := net.JoinHostPort(f.TargetHost, strconv.Itoa(f.TargetPort))
	// A refused upstream closes the accepted connection at once. The
	// workload may simply not be listening yet — a server takes a moment to
	// start — and there is no protocol here to say so in: this end of the
	// forward is raw TCP, so an immediate close is the whole vocabulary.
	// The comment here used to claim the client was told; it was not, and
	// could not be.
	// A dial with no timeout waits on the operating system's, which is
	// minutes. The target is a container on the same host: it answers at
	// once or it is not listening, and holding the accepted connection
	// meanwhile tells the developer nothing while looking like a hang.
	upstream, err := net.DialTimeout("tcp", target, forwardDialTimeout)
	if err != nil {
		return
	}
	defer f.release(upstream, false)
	if !f.hold(upstream, false) {
		// Teardown began while this was dialling. Relaying now would start
		// a copy after the wait that was supposed to cover it.
		return
	}

	// An idle timer, refreshed by traffic in either direction, exactly as
	// the proxy relay does it.
	//
	// Deliberately generous, and generous for a different reason than the
	// proxy's: a published port carries websockets, debugger sessions and
	// long polls that are legitimately silent for a long time. A blanket
	// deadline would kill precisely the traffic port forwarding exists for,
	// which is why the earlier pass left this alone. An hour bounds a
	// wedged connection without touching a working one.
	idle := f.IdleTimeout
	if idle == 0 {
		idle = defaultForwardIdle
	}
	keepalive := newIdleTimer(idle, client, upstream)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, keepalive.reader(client))
		if c, ok := upstream.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, keepalive.reader(upstream))
		if c, ok := client.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
	}()
	wg.Wait()
}

// hold registers a connection as one teardown must interrupt, reporting
// false when teardown has already begun. count says whether it also stands
// for a relay the WaitGroup is waiting on.
func (f *Forward) hold(c net.Conn, count bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closing {
		return false
	}
	if f.live == nil {
		f.live = map[net.Conn]struct{}{}
	}
	f.live[c] = struct{}{}
	if count {
		f.wg.Add(1)
	}
	return true
}

// release forgets a connection and closes it.
func (f *Forward) release(c net.Conn, count bool) {
	f.mu.Lock()
	delete(f.live, c)
	f.mu.Unlock()
	_ = c.Close()
	if count {
		f.wg.Done()
	}
}

// Close stops the listener and waits for connections in flight. It is
// safe to call twice: teardown paths overlap, and a second close reporting
// "use of closed network connection" would be noise on an error path where
// people are already looking for the real failure.
//
// The relays are closed rather than left to finish. One idle forwarded
// connection — a browser tab holding a keep-alive, an editor's language
// server — used to block teardown for as long as it stayed open, which is
// indefinitely, and the user's `dev clean` hung on it. The wait after that
// is bounded too: a copy that does not return from a closed socket is a
// copy nothing here can wait for usefully.
func (f *Forward) Close() error {
	if f.ln == nil {
		return nil
	}
	f.closeOnce.Do(func() {
		f.closeErr = f.ln.Close()
	})

	f.mu.Lock()
	f.closing = true
	for c := range f.live {
		_ = c.Close()
	}
	f.mu.Unlock()

	done := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeWait):
	}
	return f.closeErr
}

// Addr reports the bound address, for tests using port 0.
func (f *Forward) Addr() net.Addr {
	if f.ln == nil {
		return nil
	}
	return f.ln.Addr()
}
