package netpolicy

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// captureClientHello returns the opening record a real Go TLS client sends,
// so the parser is tested against the bytes it will actually meet rather
// than against a fixture written from the RFC by the same hand that wrote
// the parser.
func captureClientHello(t *testing.T, cfg *tls.Config) []byte {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	go func() { _ = tls.Client(client, cfg).Handshake() }()

	_ = server.SetReadDeadline(time.Now().Add(5 * time.Second))
	head := make([]byte, 5)
	if _, err := io.ReadFull(server, head); err != nil {
		t.Fatalf("reading record header: %v", err)
	}
	body := make([]byte, binary.BigEndian.Uint16(head[3:5]))
	if _, err := io.ReadFull(server, body); err != nil {
		t.Fatalf("reading record: %v", err)
	}
	return append(head, body...)
}

func TestParseClientHelloFindsTheServerName(t *testing.T) {
	record := captureClientHello(t, &tls.Config{
		ServerName:         "allowed.example.com",
		InsecureSkipVerify: true,
	})
	if record[0] != recordHandshake {
		t.Fatalf("record type = %#x, want a handshake", record[0])
	}
	name, ok := parseClientHello(record[5:])
	if !ok {
		t.Fatal("a ClientHello from crypto/tls did not parse")
	}
	if name != "allowed.example.com" {
		t.Fatalf("server name = %q", name)
	}
}

func TestParseClientHelloWithoutSNI(t *testing.T) {
	// A client that sends no name reaches whatever the dialled host serves
	// by default, which is the host the allowlist already approved. That is
	// a permitted case, not an unreadable one.
	record := captureClientHello(t, &tls.Config{InsecureSkipVerify: true})
	name, ok := parseClientHello(record[5:])
	if !ok {
		t.Fatal("a ClientHello with no SNI was treated as unreadable")
	}
	if name != "" {
		t.Fatalf("server name = %q, want none", name)
	}
}

func TestParseClientHelloRejectsTruncation(t *testing.T) {
	// Every prefix of a real ClientHello has to be refused rather than
	// half-read: splitting the record is the way past a check like this one.
	record := captureClientHello(t, &tls.Config{
		ServerName:         "allowed.example.com",
		InsecureSkipVerify: true,
	})
	body := record[5:]
	for _, cut := range []int{1, 4, 20, 40, len(body) / 2, len(body) - 1} {
		if cut <= 0 || cut >= len(body) {
			continue
		}
		if name, ok := parseClientHello(body[:cut]); ok {
			t.Errorf("a ClientHello cut to %d bytes parsed as %q", cut, name)
		}
	}
}

// runSniffer feeds b through a sniffer in chunks of size n, the way a
// stream arrives, and returns the verdict.
func runSniffer(b []byte, chunk int, verify func(clientHello) error) (clientHello, error) {
	var got clientHello
	s := &sniffer{
		src: &chunkReader{b: b, chunk: chunk},
		verify: func(h clientHello) error {
			got = h
			if verify != nil {
				return verify(h)
			}
			return nil
		},
	}
	_, err := io.Copy(io.Discard, s)
	return got, err
}

// chunkReader hands out at most chunk bytes per Read, so a test can arrive
// the way a network does.
type chunkReader struct {
	b     []byte
	chunk int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.chunk {
		n = r.chunk
	}
	if n > len(r.b) {
		n = len(r.b)
	}
	copy(p, r.b[:n])
	r.b = r.b[n:]
	return n, nil
}

func TestSnifferReadsANameThatArrivesInPieces(t *testing.T) {
	record := captureClientHello(t, &tls.Config{
		ServerName:         "allowed.example.com",
		InsecureSkipVerify: true,
	})
	for _, chunk := range []int{1, 3, 5, 64, len(record)} {
		h, err := runSniffer(record, chunk, nil)
		if err != nil {
			t.Fatalf("chunk %d: %v", chunk, err)
		}
		if !h.TLS || h.ServerName != "allowed.example.com" {
			t.Errorf("chunk %d: hello = %+v", chunk, h)
		}
	}
}

func TestSnifferPassesOverAProtocolThatIsNotTLS(t *testing.T) {
	// ssh through the proxy is a supported case: --allow-push grants the git
	// host on port 22. Nothing there names a destination, so there is
	// nothing to check and the CONNECT authority is the whole decision.
	h, err := runSniffer([]byte("SSH-2.0-OpenSSH_9.6\r\n"), 8, nil)
	if err != nil {
		t.Fatalf("an ssh connection was refused: %v", err)
	}
	if h.TLS {
		t.Errorf("ssh was taken for TLS: %+v", h)
	}
}

func TestSnifferRefusesAHandshakeItCannotRead(t *testing.T) {
	// A record that announces a handshake and then holds something else is a
	// name that cannot be checked, which is the case this exists for.
	garbled := append([]byte{recordHandshake, 0x03, 0x01, 0x00, 0x08},
		[]byte{0x01, 0x00, 0xff, 0xff, 0, 0, 0, 0}...)
	_, err := runSniffer(garbled, 4, func(h clientHello) error {
		if h.TLS && h.ServerName == "" {
			return &sniMismatch{Target: "allowed.example.com"}
		}
		return nil
	})
	if err == nil {
		t.Fatal("an unreadable handshake was relayed")
	}
}

// --- Through the proxy, with a real handshake ---

func TestSNIThatDiffersFromTheConnectTargetIsRefused(t *testing.T) {
	// The bypass: CONNECT to a front the policy permits, then ask for a
	// different site inside the session. On a CDN the front routes by SNI,
	// so the CONNECT check alone never sees where the traffic went.
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
			// Read and answer nothing: the handshake must fail at the proxy,
			// not for want of a server.
			go func() {
				defer conn.Close()
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()

	c := &collector{}
	p := NewProxy(mustParse(t, "allowed.example.com"))
	p.Emit = c.emit
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Addr().String())
	}
	addr := startProxy(t, p)

	status, conn, br := connectThrough(t, addr, "allowed.example.com:443")
	defer conn.Close()
	if !strings.Contains(status, "200") {
		t.Fatalf("status = %q: the CONNECT itself is permitted", status)
	}
	drainHeaders(t, br)

	// The handshake starts on the tunnel the proxy just opened. br holds
	// bytes already read from it, so the TLS client has to read through it.
	err = tls.Client(&prefixedConn{Conn: conn, r: br}, &tls.Config{
		ServerName:         "evil.example.com",
		InsecureSkipVerify: true,
	}).HandshakeContext(context.Background())
	if err == nil {
		t.Fatal("a session opened under a name the allowlist never approved")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("the refusal did not reach the client as one: %v", err)
	}

	// The client learns first: the alert is written before the decision is
	// recorded, so a read of the log the moment the handshake fails can
	// legitimately be empty.
	denied := awaitDeny(t, c)
	// The summary has to name where the workload was actually going. The
	// CONNECT target is the wrong answer: that one was allowed.
	if denied.Host != "evil.example.com" {
		t.Errorf("blocked host = %q, want the name inside the session", denied.Host)
	}
	if !strings.Contains(denied.Reason, "SNI") {
		t.Errorf("reason does not say what happened: %q", denied.Reason)
	}
	if got := p.Denials()["evil.example.com:443"]; got != 1 {
		t.Errorf("denial tally = %d, want 1", got)
	}
}

func TestMatchingSNICompletesTheHandshake(t *testing.T) {
	// The check must not cost the ordinary case anything. A real handshake
	// with the upstream's own certificate, through the proxy, unchanged.
	cert, err := selfSignedCert("allowed.example.com")
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := tls.Listen("tcp", "127.0.0.1:0",
		&tls.Config{Certificates: []tls.Certificate{cert}})
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
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Addr().String())
	}
	addr := startProxy(t, p)

	status, conn, br := connectThrough(t, addr, "allowed.example.com:443")
	defer conn.Close()
	if !strings.Contains(status, "200") {
		t.Fatalf("status = %q", status)
	}
	drainHeaders(t, br)

	tc := tls.Client(&prefixedConn{Conn: conn, r: br}, &tls.Config{
		RootCAs:    certPool(t, cert),
		ServerName: "allowed.example.com",
	})
	if err := tc.HandshakeContext(context.Background()); err != nil {
		t.Fatalf("an allowed session was refused: %v", err)
	}
	// And it is still the upstream's own certificate, not one the proxy
	// minted: reading the name off the peer proves no interception.
	if got := tc.ConnectionState().PeerCertificates[0].Subject.CommonName; got != "allowed.example.com" {
		t.Errorf("peer certificate = %q, want the upstream's own", got)
	}
	if _, err := tc.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 5)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(tc, line); err != nil {
		t.Fatalf("reading the echo: %v", err)
	}
	if string(line) != "ping\n" {
		t.Errorf("echo = %q", line)
	}
}

func TestAnUpstreamThatSpeaksFirstIsNotHeldUp(t *testing.T) {
	// The inspection must not wait for the client to say something: a
	// protocol whose server sends the first line — smtp, a database — would
	// deadlock against a check that buffered until it had a record.
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
		_, _ = fmt.Fprintf(conn, "220 mail.example.com ESMTP\r\n")
		_, _ = io.Copy(io.Discard, conn)
	}()

	p := NewProxy(mustParse(t, "allowed.example.com:25"))
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Addr().String())
	}
	addr := startProxy(t, p)

	status, conn, br := connectThrough(t, addr, "allowed.example.com:25")
	defer conn.Close()
	if !strings.Contains(status, "200") {
		t.Fatalf("status = %q", status)
	}
	drainHeaders(t, br)

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	banner, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("the banner never arrived: %v", err)
	}
	if !strings.Contains(banner, "ESMTP") {
		t.Fatalf("banner = %q", banner)
	}
}

// awaitDeny waits for the proxy to record a refusal. Polling rather than
// reading once: the alert reaches the client before the handler that sent
// it gets to the log, so the two are ordered the way they should be and the
// test has to follow rather than assume.
func awaitDeny(t *testing.T, c *collector) Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, e := range c.all() {
			if e.Action == "deny" {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing was recorded as blocked; events = %+v", c.all())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// prefixedConn lets a TLS client read through bytes another reader has
// already buffered off the same connection.
type prefixedConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
