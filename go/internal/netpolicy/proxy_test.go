package netpolicy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// startProxy runs p on a loopback listener and returns its address.
func startProxy(t *testing.T, p *Proxy) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: p}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
	})
	return ln.Addr().String()
}

// collector records emitted events.
type collector struct {
	mu     sync.Mutex
	events []Event
}

func (c *collector) emit(e Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *collector) all() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

// connectThrough performs a raw CONNECT and returns the status line plus
// the reader that consumed it. The reader must be reused for anything that
// follows: discarding it would drop bytes it has already buffered.
func connectThrough(t *testing.T, proxyAddr, target string) (string, net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading status: %v", err)
	}
	return strings.TrimSpace(line), conn, br
}

// drainHeaders consumes up to the blank line ending a response head.
func drainHeaders(t *testing.T, br *bufio.Reader) {
	t.Helper()
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("draining headers: %v", err)
		}
		if strings.TrimSpace(line) == "" {
			return
		}
	}
}

func TestConnectToDeniedHostIsRefused(t *testing.T) {
	c := &collector{}
	p := NewProxy(mustParse(t, "api.anthropic.com"))
	p.Emit = c.emit
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("proxy dialled upstream for a denied host")
		return nil, fmt.Errorf("must not dial")
	}
	addr := startProxy(t, p)

	status, conn, _ := connectThrough(t, addr, "evil.example.com:443")
	defer conn.Close()

	if !strings.Contains(status, "403") {
		t.Fatalf("status = %q, want 403", status)
	}
	events := c.all()
	if len(events) != 1 || events[0].Action != "deny" || events[0].Host != "evil.example.com" {
		t.Fatalf("events = %+v", events)
	}
	if got := p.Denials()["evil.example.com:443"]; got != 1 {
		t.Errorf("denial tally = %d, want 1", got)
	}
}

func TestConnectToAllowedHostRelaysBytes(t *testing.T) {
	// A stand-in upstream that echoes, so the test asserts the tunnel is
	// truly bidirectional rather than just that a 200 was written.
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	c := &collector{}
	p := NewProxy(mustParse(t, "allowed.example.com"))
	p.Emit = c.emit
	p.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Redirect the allowed name to the local echo server.
		return net.Dial("tcp", upstream.Addr().String())
	}
	addr := startProxy(t, p)

	status, conn, br := connectThrough(t, addr, "allowed.example.com:443")
	defer conn.Close()
	if !strings.Contains(status, "200") {
		t.Fatalf("status = %q, want 200", status)
	}
	drainHeaders(t, br)

	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading echo: %v", err)
	}
	if strings.TrimSpace(got) != "ping" {
		t.Fatalf("echo = %q", got)
	}
}

func TestProxyDoesNotTerminateTLS(t *testing.T) {
	// ROADMAP 4.3: no MITM. The client must complete a TLS handshake with
	// the upstream's own certificate, which only works if the proxy relays
	// bytes untouched.
	cert, err := selfSignedCert("allowed.example.com")
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
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
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	p := NewProxy(mustParse(t, "allowed.example.com"))
	p.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Addr().String())
	}
	proxyAddr := startProxy(t, p)

	proxyURL, _ := url.Parse("http://" + proxyAddr)
	pool := certPool(t, cert)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				ServerName: "allowed.example.com",
			},
		},
		Timeout: 5 * time.Second,
	}

	// The handshake is what is under test; the echo server speaks no HTTP,
	// so a protocol error afterwards is fine. A certificate error is not.
	_, err = client.Get("https://allowed.example.com/")
	if err != nil && strings.Contains(err.Error(), "x509") {
		t.Fatalf("TLS was intercepted: %v", err)
	}
}

func TestConnectToDeniedPortOnAllowedHost(t *testing.T) {
	c := &collector{}
	p := NewProxy(mustParse(t, "github.com"))
	p.Emit = c.emit
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("dialled a denied port")
		return nil, fmt.Errorf("must not dial")
	}
	addr := startProxy(t, p)

	status, conn, _ := connectThrough(t, addr, "github.com:22")
	defer conn.Close()
	if !strings.Contains(status, "403") {
		t.Fatalf("status = %q, want 403 for port 22", status)
	}
}

func TestConnectToRawIPIsRefused(t *testing.T) {
	// The bypass that matters: resolve the name yourself, then dial the
	// address to sidestep every hostname rule.
	p := NewProxy(mustParse(t, "api.anthropic.com"))
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("dialled a raw IP")
		return nil, fmt.Errorf("must not dial")
	}
	addr := startProxy(t, p)

	status, conn, _ := connectThrough(t, addr, "160.79.104.10:443")
	defer conn.Close()
	if !strings.Contains(status, "403") {
		t.Fatalf("status = %q, want 403 for a raw IP", status)
	}
}

func TestDenyResponseTellsUserHowToAllow(t *testing.T) {
	p := NewProxy(mustParse(t, "api.anthropic.com"))
	addr := startProxy(t, p)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "GET http://blocked.example.com/x HTTP/1.1\r\nHost: blocked.example.com\r\nConnection: close\r\n\r\n")

	body, err := io.ReadAll(conn)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "403") {
		t.Fatalf("expected 403, got:\n%s", text)
	}
	if !strings.Contains(text, "--allow-host blocked.example.com") {
		t.Errorf("deny response should name the fix:\n%s", text)
	}
}

func TestPlainHTTPIsFilteredToo(t *testing.T) {
	// Denying only CONNECT would leave a wide-open channel on port 80.
	c := &collector{}
	p := NewProxy(mustParse(t, "allowed.example.com"))
	p.Emit = c.emit
	addr := startProxy(t, p)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "GET http://denied.example.com/ HTTP/1.1\r\nHost: denied.example.com\r\nConnection: close\r\n\r\n")
	br := bufio.NewReader(conn)
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "403") {
		t.Fatalf("plain HTTP not filtered: %q", line)
	}

	events := c.all()
	if len(events) == 0 || events[0].Action != "deny" {
		t.Fatalf("events = %+v", events)
	}
}

func TestDenialTallyAggregatesRepeats(t *testing.T) {
	p := NewProxy(mustParse(t))
	addr := startProxy(t, p)

	for i := 0; i < 3; i++ {
		_, conn, _ := connectThrough(t, addr, "evil.example.com:443")
		conn.Close()
	}
	got := Summary(p.Denials())
	if len(got) != 1 || !strings.Contains(got[0], "x3") {
		t.Fatalf("Summary = %v, want a single x3 entry", got)
	}
}

func TestSummaryEmptyWhenNothingBlocked(t *testing.T) {
	if got := Summary(nil); got != nil {
		t.Errorf("Summary(nil) = %v", got)
	}
}
