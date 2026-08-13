package netpolicy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// socksDial performs the greeting and CONNECT request, returning the reply
// code and the still-open connection.
func socksDial(t *testing.T, addr, host string, port int) (byte, net.Conn) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte{socksVersion, 1, socksNoAuth}); err != nil {
		t.Fatal(err)
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(c, greeting); err != nil {
		t.Fatal(err)
	}
	if greeting[0] != socksVersion || greeting[1] != socksNoAuth {
		t.Fatalf("greeting = %v", greeting)
	}

	req := []byte{socksVersion, socksConnect, 0x00, socksATYPDomain, byte(len(host))}
	req = append(req, host...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	return reply[1], c
}

// startSOCKS runs a SOCKS front end over a proxy whose upstream is fake.
func startSOCKS(t *testing.T, entries []string, upstream func() net.Conn) (string, *Proxy) {
	t.Helper()
	allow, err := Parse(entries)
	if err != nil {
		t.Fatal(err)
	}
	p := NewProxy(allow)
	p.Dial = func(_ context.Context, _, _ string) (net.Conn, error) {
		return upstream(), nil
	}
	s := &SOCKS{Proxy: p}
	l, _, err := s.ListenAndServe("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l.Addr().String(), p
}

// pipeUpstream is an upstream that echoes nothing and stays open.
func pipeUpstream(t *testing.T) func() net.Conn {
	t.Helper()
	return func() net.Conn {
		a, b := net.Pipe()
		t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
		go func() { _, _ = io.Copy(io.Discard, b) }()
		return a
	}
}

func TestSOCKSAllowsAGrantedDestination(t *testing.T) {
	addr, _ := startSOCKS(t, []string{"db.example.com:5432"}, pipeUpstream(t))
	code, c := socksDial(t, addr, "db.example.com", 5432)
	defer func() { _ = c.Close() }()
	if code != socksOK {
		t.Fatalf("reply = %d, want success for a granted destination", code)
	}
}

// The whole point of B10 was that this destination is permitted and
// unreachable. It has to be reachable now, and only through the policy.
func TestSOCKSRefusesWhatTheAllowlistDoesNot(t *testing.T) {
	addr, p := startSOCKS(t, []string{"db.example.com:5432"}, pipeUpstream(t))

	code, c := socksDial(t, addr, "evil.example.com", 5432)
	defer func() { _ = c.Close() }()
	if code != socksNotAllowed {
		t.Fatalf("reply = %d, want 'not allowed by ruleset'", code)
	}
	// And it lands in the same tally the CONNECT path feeds, or the run's
	// summary would be silent about a whole protocol.
	if p.Denials()["evil.example.com:5432"] == 0 {
		t.Errorf("the denial was not recorded: %v", p.Denials())
	}
}

// A granted host on a port nobody granted is still refused: the port is
// half the rule.
func TestSOCKSHonoursThePort(t *testing.T) {
	addr, _ := startSOCKS(t, []string{"db.example.com:5432"}, pipeUpstream(t))
	code, c := socksDial(t, addr, "db.example.com", 6379)
	defer func() { _ = c.Close() }()
	if code != socksNotAllowed {
		t.Fatalf("reply = %d, want a refusal on an ungranted port", code)
	}
}

// A hostname rule must not authorize a literal address, here as elsewhere.
// Otherwise SOCKS would be the way to walk around every hostname rule by
// resolving it yourself first.
func TestSOCKSDeniesALiteralAddressUnderAHostnameRule(t *testing.T) {
	addr, _ := startSOCKS(t, []string{"db.example.com:5432"}, pipeUpstream(t))

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte{socksVersion, 1, socksNoAuth}); err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadFull(c, make([]byte, 2))

	req := []byte{socksVersion, socksConnect, 0x00, socksATYPv4, 93, 184, 216, 34}
	req = binary.BigEndian.AppendUint16(req, 5432)
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != socksNotAllowed {
		t.Fatalf("reply = %d, want a refusal for a bare address", reply[1])
	}
}

// BIND and UDP ASSOCIATE are not implemented, and saying so precisely beats
// a generic failure a client renders as "the proxy is broken".
func TestSOCKSRefusesCommandsItDoesNotImplement(t *testing.T) {
	addr, _ := startSOCKS(t, []string{"db.example.com:5432"}, pipeUpstream(t))

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte{socksVersion, 1, socksNoAuth}); err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadFull(c, make([]byte, 2))

	// 0x02 is BIND.
	req := []byte{socksVersion, 0x02, 0x00, socksATYPDomain, byte(len("db.example.com"))}
	req = append(req, "db.example.com"...)
	req = binary.BigEndian.AppendUint16(req, 5432)
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != socksCmdUnsupported {
		t.Fatalf("reply = %d, want 'command not supported'", reply[1])
	}
}

// A client that cannot do "no authentication" is told so rather than left
// waiting for a reply that will not come.
func TestSOCKSRejectsAnUnusableGreeting(t *testing.T) {
	addr, _ := startSOCKS(t, []string{"db.example.com:5432"}, pipeUpstream(t))

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	// Offers only GSSAPI (0x01).
	if _, err := c.Write([]byte{socksVersion, 1, 0x01}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0xFF {
		t.Fatalf("reply = %v, want no-acceptable-method", reply)
	}
}

// The SNI check is what stops an approved CONNECT being steered elsewhere
// inside the session. A second front end that skipped it would be the way
// around it, so this is the property that matters most about SOCKS.
func TestSOCKSStillChecksTheNameInsideTheSession(t *testing.T) {
	served := make(chan struct{})
	addr, p := startSOCKS(t, []string{"allowed.example.com:443"}, func() net.Conn {
		a, b := net.Pipe()
		go func() { _, _ = io.Copy(io.Discard, b); close(served) }()
		return a
	})

	code, c := socksDial(t, addr, "allowed.example.com", 443)
	if code != socksOK {
		t.Fatalf("reply = %d", code)
	}
	defer func() { _ = c.Close() }()

	// A handshake naming a different host than the one approved.
	hello := captureClientHello(t, &tls.Config{ServerName: "elsewhere.example.com", InsecureSkipVerify: true})
	if _, err := c.Write(hello); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, _ := io.ReadAll(c)
	if len(got) == 0 || got[0] != 0x15 {
		t.Fatalf("expected a TLS alert on a mismatched name, got %x", got)
	}

	var found bool
	for host := range p.Denials() {
		if strings.Contains(host, "elsewhere.example.com") {
			found = true
		}
	}
	if !found {
		t.Errorf("the mismatch was not recorded as a denial: %v", p.Denials())
	}
	_ = served
}

// Reading the wire format is reading bytes someone else chose.
func FuzzSOCKSRequest(f *testing.F) {
	f.Add([]byte{socksVersion, socksConnect, 0x00, socksATYPDomain, 3, 'a', '.', 'b', 0x01, 0xBB})
	f.Add([]byte{socksVersion, socksConnect, 0x00, socksATYPv4, 1, 2, 3, 4, 0x01, 0xBB})
	f.Add([]byte{socksVersion, socksConnect, 0x00, socksATYPv6})
	f.Add([]byte{socksVersion, 0x02, 0x00, socksATYPDomain, 0})
	f.Add([]byte{})
	f.Add([]byte{socksVersion, socksConnect, 0x00, 0x09})
	f.Add([]byte{socksVersion, socksConnect, 0x00, socksATYPDomain, 255, 'a'})

	f.Fuzz(func(t *testing.T, data []byte) {
		host, port, err := socksRequest(bufio.NewReader(strings.NewReader(string(data))))
		if err != nil {
			return
		}
		// A destination that parsed has to be one the policy can judge: a
		// port in range, and a host that came from the input.
		if port < 0 || port > 65535 {
			t.Fatalf("port %d is not a port", port)
		}
		if host == "" {
			t.Fatal("accepted a request naming no host")
		}
	})
}
