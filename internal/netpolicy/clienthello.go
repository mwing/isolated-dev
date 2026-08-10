package netpolicy

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// The proxy allowlists by CONNECT target and by the name inside the TLS
// session. ROADMAP 4.3 claimed both while only the first was checked, and
// the gap was real: on a CDN-heavy allowlist, CONNECT to a front the policy
// permits and put a different SNI inside the session, and the front routes
// the connection to the host that was denied. The CONNECT authority decides
// which IP is dialled; the SNI decides which site answers.
//
// Nothing here terminates TLS. The opening record is read as it streams past
// on its way upstream — not held, not decrypted, not rewritten — so
// certificate pinning keeps working and the no-interception property in
// ROADMAP 4.3 is untouched. What is read is the first record only.

const (
	recordHandshake      = 0x16
	handshakeClientHello = 0x01
	extensionServerName  = 0x0000
	nameTypeHostName     = 0x00
	// maxRecordPayload is the largest a TLS record may be. A length field
	// claiming more than this is not a record, so there is no point waiting
	// for bytes that will never make one.
	maxRecordPayload = 1 << 14
)

// clientHello is what the opening bytes of a relayed connection turned out
// to be.
type clientHello struct {
	// TLS reports that the connection opened with a TLS handshake record.
	// Anything else — ssh, a database protocol, plain text — carries no name
	// to check, and the CONNECT authority is the whole of the decision.
	TLS bool
	// ServerName is the SNI, empty when the client sent none. That is not a
	// gap: a session with no SNI reaches whatever the dialled host serves by
	// default, and that host is the one the allowlist already approved.
	ServerName string
}

// sniMismatch is the verdict that stops a relay. It is an error so it can
// travel out through io.Copy, which is where it is noticed.
type sniMismatch struct {
	// Target is the CONNECT authority — the name the allowlist approved.
	Target string
	// SNI is the name the ClientHello asked the far end for. Empty when the
	// opening record announced a handshake and then could not be read.
	SNI string
}

func (e *sniMismatch) Error() string {
	if e.SNI == "" {
		return fmt.Sprintf("CONNECT %s opened a TLS session whose ClientHello could not be read, "+
			"so the name inside it could not be checked", e.Target)
	}
	return fmt.Sprintf("CONNECT %s carried SNI %s: the name inside the session is not the one "+
		"the allowlist approved", e.Target, e.SNI)
}

// Host is the destination the workload was reaching for, which is what the
// exit summary should name. The CONNECT target is not it: that one was
// allowed.
func (e *sniMismatch) Host() string {
	if e.SNI != "" {
		return e.SNI
	}
	return e.Target
}

// sameServerName compares two hostnames the way DNS does.
func sameServerName(a, b string) bool {
	return strings.EqualFold(
		strings.TrimSuffix(strings.TrimSpace(a), "."),
		strings.TrimSuffix(strings.TrimSpace(b), "."))
}

// sniffer inspects the opening bytes of the client half of a relayed
// connection while they stream past.
//
// Nothing is held back: every Read hands its bytes on immediately, so the
// verdict lands a moment after the bytes it is about. That ordering is
// deliberate. Buffering until the opening record was complete would deadlock
// any protocol whose server speaks first — smtp, a database handshake — and
// would put the inspection in the latency path of every connection. The cost
// is that the far end may receive a ClientHello for a name it will never
// answer, which costs it one closed socket and reveals nothing the CONNECT
// had not already revealed.
type sniffer struct {
	src    io.Reader
	verify func(clientHello) error

	seen    []byte
	settled bool
	verdict error
}

func (s *sniffer) Read(p []byte) (int, error) {
	if s.verdict != nil {
		return 0, s.verdict
	}
	n, err := s.src.Read(p)
	if n > 0 && !s.settled {
		s.seen = append(s.seen, p[:n]...)
		s.inspect()
	}
	return n, err
}

// inspect decides as soon as it can, and otherwise waits for more bytes.
func (s *sniffer) inspect() {
	b := s.seen
	if len(b) < 5 {
		return
	}
	if b[0] != recordHandshake {
		// Not TLS. Nothing here names a destination, so the CONNECT
		// authority — already checked — is the whole decision.
		s.settle(clientHello{})
		return
	}
	length := int(binary.BigEndian.Uint16(b[3:5]))
	if length == 0 || length > maxRecordPayload {
		s.settle(clientHello{TLS: true})
		return
	}
	if len(b) < 5+length {
		return
	}
	name, ok := parseClientHello(b[5 : 5+length])
	if !ok {
		// A handshake record that holds no readable ClientHello is a name
		// that cannot be checked. Splitting a ClientHello across records is
		// a known way past filters that check it, so this is a refusal
		// rather than a pass.
		s.settle(clientHello{TLS: true})
		return
	}
	s.settle(clientHello{TLS: true, ServerName: name})
}

func (s *sniffer) settle(h clientHello) {
	s.settled = true
	s.seen = nil
	if s.verify != nil {
		s.verdict = s.verify(h)
	}
}

// parseClientHello extracts the SNI from one TLS handshake record's payload.
// It reports ok=false for anything it cannot read to the end, since a
// half-understood ClientHello is exactly the case this check exists for.
func parseClientHello(b []byte) (string, bool) {
	// msg_type(1) uint24 length(3)
	if len(b) < 4 || b[0] != handshakeClientHello {
		return "", false
	}
	length := int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	body := b[4:]
	if len(body) < length {
		return "", false
	}
	body = body[:length]

	// legacy_version(2) random(32)
	if len(body) < 34 {
		return "", false
	}
	body = body[34:]

	var ok bool
	if body, ok = skipVector8(body); !ok { // session_id
		return "", false
	}
	if body, ok = skipVector16(body); !ok { // cipher_suites
		return "", false
	}
	if body, ok = skipVector8(body); !ok { // compression_methods
		return "", false
	}
	// Extensions are optional in the wire format. A ClientHello without them
	// carries no SNI, which is a name the client did not send rather than one
	// it hid — the same case as an empty server_name list.
	if len(body) == 0 {
		return "", true
	}
	exts, ok := readVector16(body)
	if !ok {
		return "", false
	}

	for len(exts) >= 4 {
		typ := binary.BigEndian.Uint16(exts[:2])
		size := int(binary.BigEndian.Uint16(exts[2:4]))
		if len(exts) < 4+size {
			return "", false
		}
		data := exts[4 : 4+size]
		exts = exts[4+size:]
		if typ != extensionServerName {
			continue
		}
		return parseServerNameExtension(data)
	}
	if len(exts) != 0 {
		return "", false
	}
	return "", true
}

// parseServerNameExtension reads the first host_name in a server_name list.
// RFC 6066 permits a list; every implementation sends exactly one, and a
// second name would be a second destination, so only the first is honored
// and anything past it makes the record unreadable rather than ignorable.
func parseServerNameExtension(b []byte) (string, bool) {
	names, ok := readVector16(b)
	if !ok {
		return "", false
	}
	for len(names) >= 3 {
		typ := names[0]
		size := int(binary.BigEndian.Uint16(names[1:3]))
		if len(names) < 3+size {
			return "", false
		}
		if typ == nameTypeHostName {
			return string(names[3 : 3+size]), true
		}
		names = names[3+size:]
	}
	if len(names) != 0 {
		return "", false
	}
	return "", true
}

// readVector16 returns the contents of a uint16-length-prefixed vector.
func readVector16(b []byte) ([]byte, bool) {
	if len(b) < 2 {
		return nil, false
	}
	n := int(binary.BigEndian.Uint16(b[:2]))
	if len(b) < 2+n {
		return nil, false
	}
	return b[2 : 2+n], true
}

// skipVector8 steps over a uint8-length-prefixed vector.
func skipVector8(b []byte) ([]byte, bool) {
	if len(b) < 1 {
		return nil, false
	}
	n := int(b[0])
	if len(b) < 1+n {
		return nil, false
	}
	return b[1+n:], true
}

// skipVector16 steps over a uint16-length-prefixed vector.
func skipVector16(b []byte) ([]byte, bool) {
	if len(b) < 2 {
		return nil, false
	}
	n := int(binary.BigEndian.Uint16(b[:2]))
	if len(b) < 2+n {
		return nil, false
	}
	return b[2+n:], true
}
