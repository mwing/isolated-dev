package netpolicy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// SOCKS is a SOCKS5 front end onto the same policy the HTTP proxy enforces.
//
// It exists because a grant and a route are different things. The workload
// has no way out except this sidecar, and the HTTP proxy can only be used by
// a client that speaks HTTP CONNECT — so `dev allow db.example.com:5432`
// permitted a destination that psql, redis-cli and mysql could not reach,
// and they failed with "network is unreachable", which names the network
// rather than the policy. SOCKS5 is what those clients, and the wrappers
// people put in front of them, already know how to speak.
//
// It weakens nothing. There is no new decision here: every connection goes
// through Proxy.authorizeTunnel, the same function the CONNECT path calls,
// so the allowlist, the held-request prompt, the infrastructure guard and
// the SNI check all apply unchanged, and the events land in the same log.
// What changes is only who can ask.
//
// No authentication, deliberately. The listener is on the internal network,
// which has exactly one other member — the workload — and a credential the
// workload would have to be given is a credential it could read. The
// boundary is the topology, as everywhere else here.
type SOCKS struct {
	Proxy *Proxy
	// Now is the clock, injected for deterministic tests.
	Now func() time.Time
}

// SOCKS5 wire constants (RFC 1928).
const (
	socksVersion = 0x05
	socksNoAuth  = 0x00
	socksConnect = 0x01

	socksATYPv4     = 0x01
	socksATYPDomain = 0x03
	socksATYPv6     = 0x04

	socksOK             = 0x00
	socksGeneralFailure = 0x01
	socksNotAllowed     = 0x02
	socksHostUnreach    = 0x04
	socksCmdUnsupported = 0x07
	socksATYPUnsupport  = 0x08
)

// Serve accepts connections until l is closed.
func (s *SOCKS) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer func() { _ = conn.Close() }()
			s.handle(conn)
		}()
	}
}

// ListenAndServe starts a SOCKS5 listener on addr.
func (s *SOCKS) ListenAndServe(addr string) (net.Listener, <-chan error, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	errs := make(chan error, 1)
	go func() { errs <- s.Serve(l) }()
	return l, errs, nil
}

func (s *SOCKS) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *SOCKS) handle(conn net.Conn) {
	br := bufio.NewReader(conn)
	bw := bufio.NewWriter(conn)

	if err := socksGreet(br, conn); err != nil {
		return
	}

	host, port, err := socksRequest(br)
	if err != nil {
		var unsup *socksUnsupported
		if errors.As(err, &unsup) {
			_ = socksReply(conn, unsup.code, nil)
			s.Proxy.emit(Event{Action: "deny", Method: "SOCKS5", Reason: unsup.reason})
			return
		}
		_ = socksReply(conn, socksGeneralFailure, nil)
		return
	}

	// The same decision the CONNECT path makes, made by the same code.
	upstream, err := s.Proxy.authorizeTunnel(context.Background(), host, port, "SOCKS5")
	if err != nil {
		_ = socksReply(conn, socksReplyFor(err), nil)
		return
	}
	defer func() { _ = upstream.Close() }()

	if err := socksReply(conn, socksOK, upstream.LocalAddr()); err != nil {
		return
	}

	// The name inside the session still has to be the name that was
	// approved. A tunnel opened through SOCKS is as steerable by SNI as one
	// opened through CONNECT, and dropping the check here would have made
	// the new door the weaker one.
	src := &sniffer{src: br, verify: verifySNI(host)}
	rw := bufio.NewReadWriter(br, bw)

	start := s.now()
	n, verdict := relay(conn, src, rw, upstream, s.Proxy.IdleTimeout)
	if m, ok := verdict.(*sniMismatch); ok {
		_, _ = conn.Write(tlsAccessDenied)
		s.Proxy.emit(Event{Action: "deny", Host: m.Host(), Port: port, Method: "SOCKS5",
			Bytes: n, Reason: m.Error()})
		return
	}
	s.Proxy.emit(Event{Action: "allow", Host: host, Port: port, Method: "SOCKS5",
		Bytes: n, Latency: s.now().Sub(start).String()})
}

// socksReplyFor maps a refusal onto the closest SOCKS5 reply code, so a
// client reports something truthful rather than a generic failure.
func socksReplyFor(err error) byte {
	var ref *refusal
	if !errors.As(err, &ref) {
		return socksGeneralFailure
	}
	switch ref.kind {
	case refusedUnreachable:
		return socksHostUnreach
	default:
		// Both policy cases are "not allowed by ruleset", which is exactly
		// what happened and is a code clients render as a refusal rather
		// than as a broken network.
		return socksNotAllowed
	}
}

// socksGreet performs the method-negotiation exchange. Only "no
// authentication" is offered: see SOCKS.
func socksGreet(br *bufio.Reader, w io.Writer) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		return err
	}
	if header[0] != socksVersion {
		return fmt.Errorf("socks: version %d not supported", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return err
	}
	for _, m := range methods {
		if m == socksNoAuth {
			_, err := w.Write([]byte{socksVersion, socksNoAuth})
			return err
		}
	}
	// 0xFF: no acceptable method.
	_, _ = w.Write([]byte{socksVersion, 0xFF})
	return fmt.Errorf("socks: client offered no usable auth method")
}

// socksUnsupported is a request this proxy will not serve, carrying the
// reply code that says so precisely.
type socksUnsupported struct {
	code   byte
	reason string
}

func (e *socksUnsupported) Error() string { return e.reason }

// socksRequest reads the connect request and returns its destination.
//
// A domain name is the case worth having: with socks5h the client sends the
// name and the allowlist matches on it, exactly as CONNECT does. A literal
// address means the client resolved it first, and is treated the way the
// allowlist treats every literal — permitted only if a rule names that
// address — so nothing is reachable through this door that was not
// reachable through the other one.
func socksRequest(br *bufio.Reader) (string, int, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(br, head); err != nil {
		return "", 0, err
	}
	if head[0] != socksVersion {
		return "", 0, fmt.Errorf("socks: version %d not supported", head[0])
	}
	if head[1] != socksConnect {
		return "", 0, &socksUnsupported{code: socksCmdUnsupported,
			reason: "socks: only CONNECT is supported (no BIND, no UDP associate)"}
	}

	var host string
	switch head[3] {
	case socksATYPv4:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case socksATYPv6:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case socksATYPDomain:
		n, err := br.ReadByte()
		if err != nil {
			return "", 0, err
		}
		// A zero-length name is a well-formed frame naming nothing. Refused
		// here rather than passed on: the allowlist would deny it, but a
		// destination the policy cannot describe should never reach the
		// policy at all — found by fuzzing, in under a second.
		if n == 0 {
			return "", 0, fmt.Errorf("socks: request names no host")
		}
		b := make([]byte, int(n))
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	default:
		return "", 0, &socksUnsupported{code: socksATYPUnsupport,
			reason: "socks: unknown address type"}
	}

	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(pb)), nil
}

// socksReply writes a reply. The bound address is informational for a
// CONNECT reply, and clients do not route on it, so an unusable local
// address is reported as the unspecified one rather than guessed at.
func socksReply(w io.Writer, code byte, bound net.Addr) error {
	reply := []byte{socksVersion, code, 0x00, socksATYPv4, 0, 0, 0, 0, 0, 0}
	if tcp, ok := bound.(*net.TCPAddr); ok && tcp != nil {
		if v4 := tcp.IP.To4(); v4 != nil {
			copy(reply[4:8], v4)
			binary.BigEndian.PutUint16(reply[8:10], uint16(tcp.Port))
		}
	}
	_, err := w.Write(reply)
	return err
}

// SOCKSURL is the value for ALL_PROXY inside the workload.
//
// socks5h, not socks5: the "h" keeps name resolution on the proxy side, so
// the allowlist sees the name the client asked for rather than an address
// the client resolved on its own. That is the same reason the CONNECT path
// matches on the authority.
func (t Topology) SOCKSURL() string {
	return fmt.Sprintf("socks5h://%s:%d", t.SidecarIP, t.SOCKSPort)
}

// DefaultSOCKSPort is where the sidecar listens for SOCKS5.
const DefaultSOCKSPort = 1080
