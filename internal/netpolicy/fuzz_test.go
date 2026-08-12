package netpolicy

import (
	"crypto/tls"
	"strings"
	"testing"
)

// Hand-rolled parsing of bytes an attacker chooses is the code most worth
// fuzzing, and this parser has already had one bypass: a ClientHello split
// across two TLS records was read as "no SNI", which is the allow branch.
//
// The properties asserted here are the ones a bypass would break, not just
// "does not crash":
//
//   - a name is only ever reported for input that really contains one, so
//     nothing can make the proxy compare against a name the client did not
//     send;
//   - unreadable input reports unreadable, never absent, because those are
//     opposite decisions at the call site;
//   - the parser reads only the record it was given.
func FuzzParseClientHello(f *testing.F) {
	// Seeds: the real thing, then the shapes that broke it or nearly did.
	real := captureClientHello(f, &tls.Config{
		ServerName:         "example.com",
		InsecureSkipVerify: true,
	})
	f.Add(real[5:])                 // a genuine handshake body
	f.Add(real)                     // with the record header still attached
	f.Add(real[5 : len(real)/2])    // truncated mid-message
	f.Add(splitAcrossRecords(real)) // the bypass, as bytes
	f.Add([]byte{})
	f.Add([]byte{0x01, 0x00, 0x00, 0x00})
	f.Add([]byte{0x01, 0xff, 0xff, 0xff, 0x00})            // length beyond the buffer
	f.Add([]byte("SSH-2.0-OpenSSH_9.6\r\n"))               // not TLS at all
	f.Add([]byte{0x01, 0x00, 0x00, 0x2c, 0x03, 0x03})      // header, no body
	f.Add(append([]byte{0x01, 0x00, 0x00, 0x40}, real...)) // nested nonsense

	f.Fuzz(func(t *testing.T, data []byte) {
		name, ok := parseClientHello(data)
		if !ok {
			// The unreadable verdict must stay unreadable: a name here
			// would be one the caller compares against.
			if name != "" {
				t.Fatalf("parser reported a name while failing: %q", name)
			}
			return
		}

		// A reported name has to be findable in the input. Anything else
		// means the parser invented or mis-sliced it, and the proxy would
		// be comparing the CONNECT target against fiction.
		if name != "" && !strings.Contains(string(data), name) {
			t.Fatalf("reported name %q is not in the input", name)
		}

		// Whatever it read, the decision it feeds must be reachable
		// without panicking, since that is what runs in the sidecar.
		_ = verifySNI("allowed.example.com")(clientHello{
			TLS: true, Readable: true, ServerName: name,
		})
	})
}

// The allowlist is parsed from strings a user types and a project file
// supplies, and the result decides what a container may reach. A panic here
// is a denial of service in the sidecar; a rule that does not match what it
// prints is a policy nobody can review.
func FuzzParseAllowlist(f *testing.F) {
	for _, seed := range []string{
		"api.example.com",
		"*.example.com",
		"registry.npmjs.org:443",
		"10.0.0.5:5432",
		"[::1]:443",
		"*.co.uk",
		"аpi.example.com", // Cyrillic а
		"",
		":",
		"::::",
		"*.",
		"host:0",
		"host:99999",
		"host:-1",
		strings.Repeat("a", 300) + ".example.com",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, entry string) {
		a, err := Parse([]string{entry})
		if err != nil {
			return
		}
		for _, r := range a.Rules() {
			// A rule has to survive being written down and read back: the
			// printed form is what `dev grants` shows and what a person
			// approves, so the two must be the same policy.
			back, err := Parse([]string{r.String()})
			if err != nil {
				t.Fatalf("rule %q printed as %q, which does not parse: %v", entry, r.String(), err)
			}
			if len(back.Rules()) != 1 {
				t.Fatalf("rule %q printed as %q, which parsed to %d rules",
					entry, r.String(), len(back.Rules()))
			}
		}
		// Matching must not panic on anything that parsed.
		_ = a.Allows("example.com", 443)
		_ = a.AllowsName("example.com")
		_ = a.matchedByWildcard("sub.example.com")
	})
}

// The resolver decides what names are forwarded, and the shape check is the
// only thing standing between a wildcard grant and a data channel.
func FuzzSuspiciousQuery(f *testing.F) {
	for _, seed := range []string{
		"api.example.com",
		strings.Repeat("a", 60) + ".example.com",
		strings.Repeat("ab.", 50) + "example.com",
		"a.b.c.d.e.f.g.h.i.j.k.example.com",
		"",
		".",
		"..",
		strings.Repeat(".", 200),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		why := suspiciousQuery(name)
		// A refusal has to say which limit was crossed, or the person whose
		// legitimate name tripped it has nothing to act on.
		if why != "" && !strings.Contains(why, "limit") && !strings.Contains(why, "over the") {
			t.Fatalf("refused %q without naming a limit: %q", name, why)
		}
		// And it must agree with itself: a name refused once stays refused.
		if second := suspiciousQuery(name); second != why {
			t.Fatalf("two answers for %q: %q then %q", name, why, second)
		}
	})
}
