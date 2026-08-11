package netpolicy

import (
	"crypto/tls"
	"encoding/binary"
	"testing"
)

// splitAcrossRecords re-frames one TLS record's payload as two records of
// the same content type.
//
// This is legal TLS and every implementation reassembles it: a handshake
// message is a stream, and record boundaries are not message boundaries.
// A client controls its own stack, so an attacker emits this deliberately.
func splitAcrossRecords(record []byte) []byte {
	payload := record[5:]
	half := len(payload) / 2

	out := make([]byte, 0, len(record)+5)
	head := func(n int) []byte {
		h := []byte{record[0], record[1], record[2], 0, 0}
		binary.BigEndian.PutUint16(h[3:5], uint16(n))
		return h
	}
	out = append(out, head(half)...)
	out = append(out, payload[:half]...)
	out = append(out, head(len(payload)-half)...)
	out = append(out, payload[half:]...)
	return out
}

// The bypass this check exists to close, in the form that got past it: a
// ClientHello fragmented across two TLS records cannot be parsed here,
// while the far end reassembles it and routes on the real name inside.
//
// Reported as "no SNI" — which is what an earlier version did — the
// connection was relayed and a shared front then delivered it to the
// denied host.
func TestARecordFragmentedClientHelloIsRefused(t *testing.T) {
	record := captureClientHello(t, &tls.Config{
		ServerName:         "denied.example.com",
		InsecureSkipVerify: true,
	})
	fragmented := splitAcrossRecords(record)

	// The production callback, not one written for the test. The last
	// version of this bug survived because those were different.
	_, err := runSniffer(fragmented, 512, verifySNI("allowed.example.com"))
	if err == nil {
		t.Fatal("a ClientHello split across two TLS records was relayed; " +
			"the far end reassembles it and routes on the name inside")
	}
}

// A hello that genuinely carries no SNI still passes: the far end serves
// whatever it serves by default, and that host is the one already approved.
func TestAHelloWithNoServerNameIsAllowed(t *testing.T) {
	// crypto/tls omits the extension when ServerName is empty and the peer
	// is addressed by IP.
	record := captureClientHello(t, &tls.Config{InsecureSkipVerify: true})

	name, ok := parseClientHello(record[5:])
	if !ok {
		t.Fatal("a ClientHello from crypto/tls did not parse")
	}
	if name != "" {
		t.Skipf("this build of crypto/tls sent an SNI (%q); nothing to test", name)
	}
	if err := verifySNI("allowed.example.com")(clientHello{
		TLS: true, Readable: true, ServerName: "",
	}); err != nil {
		t.Fatalf("a hello with no SNI was refused: %v", err)
	}
}

// The distinction the whole fix rests on, asserted directly.
func TestUnreadableAndAbsentAreDifferentDecisions(t *testing.T) {
	verify := verifySNI("allowed.example.com")

	if err := verify(clientHello{TLS: true, Readable: false}); err == nil {
		t.Error("an unreadable ClientHello was allowed")
	}
	if err := verify(clientHello{TLS: true, Readable: true}); err != nil {
		t.Errorf("a hello that carried no SNI was refused: %v", err)
	}
	if err := verify(clientHello{}); err != nil {
		t.Errorf("a non-TLS connection was refused: %v", err)
	}
	if err := verify(clientHello{
		TLS: true, Readable: true, ServerName: "allowed.example.com",
	}); err != nil {
		t.Errorf("a matching SNI was refused: %v", err)
	}
	if err := verify(clientHello{
		TLS: true, Readable: true, ServerName: "elsewhere.example.com",
	}); err == nil {
		t.Error("a mismatched SNI was allowed")
	}
}
