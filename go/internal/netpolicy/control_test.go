package netpolicy

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortTempDir returns a directory with a short path. t.TempDir() embeds
// the test name, which pushes a socket path over the sockaddr_un limit for
// any test with a descriptive name.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "d2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newControl(t *testing.T, entries ...string) (*Control, *Proxy, *Resolver) {
	t.Helper()
	allow := mustParse(t, entries...)
	p := NewProxy(allow)
	r := NewResolver(allow)
	return NewControl(p, r, entries), p, r
}

func TestAllowTakesEffectWithoutRestart(t *testing.T) {
	// The point of the whole mechanism: a denial becomes a question the
	// user answers while the process is still running.
	c, p, r := newControl(t, "api.anthropic.com")

	if p.Allowlist().Allows("internal.example.com", 443) {
		t.Fatal("host allowed before the grant")
	}
	resp := c.Apply(Request{Op: "allow", Host: "internal.example.com"})
	if !resp.OK {
		t.Fatalf("allow failed: %+v", resp)
	}
	if !p.Allowlist().Allows("internal.example.com", 443) {
		t.Error("proxy policy did not change")
	}
	if !r.Allowlist().AllowsName("internal.example.com") {
		t.Error("resolver policy did not change; a name would still be refused")
	}
	if !p.Allowlist().Allows("api.anthropic.com", 443) {
		t.Error("existing rules lost")
	}
}

func TestRevokeTakesEffect(t *testing.T) {
	c, p, _ := newControl(t, "api.anthropic.com", "extra.example.com")

	if resp := c.Apply(Request{Op: "revoke", Host: "extra.example.com"}); !resp.OK {
		t.Fatalf("revoke failed: %+v", resp)
	}
	if p.Allowlist().Allows("extra.example.com", 443) {
		t.Error("revoked host still allowed")
	}
	if !p.Allowlist().Allows("api.anthropic.com", 443) {
		t.Error("revoke removed the wrong rule")
	}
}

func TestBadEntryDoesNotTakeThePolicyDown(t *testing.T) {
	// A malformed grant mid-run must not empty the allowlist: that would
	// fail every subsequent connection rather than just the bad request.
	c, p, _ := newControl(t, "api.anthropic.com")

	resp := c.Apply(Request{Op: "allow", Host: "not a host"})
	if resp.OK {
		t.Fatal("malformed host accepted")
	}
	if !p.Allowlist().Allows("api.anthropic.com", 443) {
		t.Fatal("existing policy was dropped by a bad request")
	}
}

func TestEmptyAndUnknownOps(t *testing.T) {
	c, _, _ := newControl(t, "a.example.com")

	if resp := c.Apply(Request{Op: "allow"}); resp.OK {
		t.Error("empty host accepted")
	}
	if resp := c.Apply(Request{Op: "destroy"}); resp.OK {
		t.Error("unknown op accepted")
	}
}

func TestListAndDenials(t *testing.T) {
	c, p, _ := newControl(t, "b.example.com", "a.example.com")

	resp := c.Apply(Request{Op: "list"})
	if !resp.OK || len(resp.Rules) != 2 {
		t.Fatalf("list = %+v", resp)
	}

	p.emit(Event{Action: "deny", Host: "evil.example.com", Port: 443})
	d := c.Apply(Request{Op: "denials"})
	if !d.OK || d.Denials["evil.example.com:443"] != 1 {
		t.Fatalf("denials = %+v", d)
	}
}

func TestControlIsAUnixSocketNotANetworkListener(t *testing.T) {
	// The workload shares the internal network with the sidecar, so any
	// TCP port the sidecar opened would be reachable from it. A socket in
	// a filesystem the workload does not have is not addressable at all.
	path := filepath.Join(shortTempDir(t), "control.sock")

	c, _, _ := newControl(t, "a.example.com")
	if err := c.Listen(path); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	addr := c.ln.Addr()
	if addr.Network() != "unix" {
		t.Fatalf("control listener is %s, want unix", addr.Network())
	}
	if _, ok := c.ln.(*net.TCPListener); ok {
		t.Fatal("control is a TCP listener and would be reachable from the workload")
	}
}

func TestControlOverTheSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "control.sock")

	c, p, _ := newControl(t, "a.example.com")
	if err := c.Listen(path); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	resp, err := (Client{Path: path}).Do(Request{Op: "allow", Host: "new.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("resp = %+v", resp)
	}
	if !p.Allowlist().Allows("new.example.com", 443) {
		t.Error("grant over the socket did not apply")
	}
}

func TestListenReplacesAStaleSocket(t *testing.T) {
	// A crashed sidecar leaves the socket file behind; bind would fail and
	// the next run would have no control channel at all.
	path := filepath.Join(shortTempDir(t), "control.sock")

	first, _, _ := newControl(t, "a.example.com")
	if err := first.Listen(path); err != nil {
		t.Fatal(err)
	}
	_ = first.ln.Close()

	second, _, _ := newControl(t, "a.example.com")
	if err := second.Listen(path); err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	defer second.Close()
}

func TestOverlongSocketPathIsExplained(t *testing.T) {
	// The kernel reports only "invalid argument", which sends the reader
	// looking for a permissions problem that is not there.
	c, _, _ := newControl(t, "a.example.com")
	err := c.Listen("/tmp/" + strings.Repeat("x", 120) + ".sock")
	if err == nil {
		t.Fatal("overlong path accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error does not explain the cause: %v", err)
	}
}

func TestChangesAreReported(t *testing.T) {
	var ops []string
	c, _, _ := newControl(t, "a.example.com")
	c.OnChange = func(op, host string, _ []string) {
		ops = append(ops, op+" "+host)
	}

	c.Apply(Request{Op: "allow", Host: "b.example.com"})
	c.Apply(Request{Op: "revoke", Host: "b.example.com"})
	c.Apply(Request{Op: "allow", Host: "bad host"})

	if len(ops) != 2 {
		t.Fatalf("ops = %v, want the two accepted changes only", ops)
	}
	if ops[0] != "allow b.example.com" || ops[1] != "revoke b.example.com" {
		t.Errorf("ops = %v", ops)
	}
}

func TestLiveGrantUnblocksARealConnection(t *testing.T) {
	// End to end through the proxy: denied, granted, allowed — with no
	// restart in between.
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			conn, err := upstream.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	c, p, _ := newControl(t, "api.anthropic.com")
	p.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Addr().String())
	}
	addr := startProxy(t, p)

	status, conn, _ := connectThrough(t, addr, "late.example.com:443")
	conn.Close()
	if !strings.Contains(status, "403") {
		t.Fatalf("expected a denial first, got %q", status)
	}

	if resp := c.Apply(Request{Op: "allow", Host: "late.example.com"}); !resp.OK {
		t.Fatalf("grant failed: %+v", resp)
	}

	status2, conn2, _ := connectThrough(t, addr, "late.example.com:443")
	conn2.Close()
	if !strings.Contains(status2, "200") {
		t.Fatalf("after the grant: %q, want 200", status2)
	}
}

func TestConcurrentChangesAndRequests(t *testing.T) {
	// The policy is read on every connection and written by the console;
	// -race would catch an unguarded swap.
	c, p, _ := newControl(t, "a.example.com")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			c.Apply(Request{Op: "allow", Host: fmt.Sprintf("h%d.example.com", i)})
		}
	}()
	for i := 0; i < 200; i++ {
		p.Allowlist().Allows("a.example.com", 443)
	}
	<-done
}

func TestBlockingModeHoldsUntilGranted(t *testing.T) {
	// Firewall behavior: the request waits for a verdict instead of
	// failing and needing a retry nobody is watching for.
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			conn, err := upstream.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	c, p, _ := newControl(t, "a.example.com")
	p.AskTimeout = 10 * time.Second
	p.AskPoll = 20 * time.Millisecond
	p.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Addr().String())
	}
	addr := startProxy(t, p)

	// The grant arrives after the request is already waiting.
	go func() {
		time.Sleep(150 * time.Millisecond)
		c.Apply(Request{Op: "allow", Host: "held.example.com"})
	}()

	start := time.Now()
	status, conn, _ := connectThrough(t, addr, "held.example.com:443")
	conn.Close()

	if !strings.Contains(status, "200") {
		t.Fatalf("status = %q, want the held request to proceed", status)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Error("request did not actually wait")
	}
}

func TestBlockingModeTimesOutIntoADenial(t *testing.T) {
	// Nobody answers: the request must fail rather than hang forever.
	c := &collector{}
	p := NewProxy(mustParse(t, "a.example.com"))
	p.Emit = c.emit
	p.AskTimeout = 120 * time.Millisecond
	p.AskPoll = 20 * time.Millisecond
	addr := startProxy(t, p)

	status, conn, _ := connectThrough(t, addr, "nobody.example.com:443")
	conn.Close()
	if !strings.Contains(status, "403") {
		t.Fatalf("status = %q, want a denial after the timeout", status)
	}

	var sawPending, sawTimeout bool
	for _, e := range c.all() {
		switch e.Action {
		case "pending":
			sawPending = true
		case "timeout":
			sawTimeout = true
		}
	}
	if !sawPending {
		t.Error("no pending event; the console would have nothing to prompt on")
	}
	if !sawTimeout {
		t.Error("no timeout event")
	}
}

func TestReportingModeFailsImmediately(t *testing.T) {
	// The default where nobody is present to answer: blocking in CI is a
	// hang, which is worse than a clear failure.
	p := NewProxy(mustParse(t, "a.example.com"))
	addr := startProxy(t, p)

	start := time.Now()
	status, conn, _ := connectThrough(t, addr, "nope.example.com:443")
	conn.Close()

	if !strings.Contains(status, "403") {
		t.Fatalf("status = %q", status)
	}
	if time.Since(start) > time.Second {
		t.Error("reporting mode waited; it should fail at once")
	}
}

func TestHeldConnectionEndsWhenTheClientGivesUp(t *testing.T) {
	// A held connection must not outlive the request that caused it.
	p := NewProxy(mustParse(t, "a.example.com"))
	p.AskTimeout = 30 * time.Second
	p.AskPoll = 20 * time.Millisecond
	addr := startProxy(t, p)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "CONNECT gone.example.com:443 HTTP/1.1\r\nHost: gone.example.com\r\n\r\n")
	time.Sleep(80 * time.Millisecond)
	conn.Close()

	// If the wait ignored the client going away, the proxy would still be
	// holding this at the end of the test; the race detector and the
	// server's Close in cleanup surface that.
	time.Sleep(80 * time.Millisecond)
}
