package netpolicy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func startDNS(t *testing.T, allow *Allowlist, upstream func(context.Context, string) ([]net.IP, error)) string {
	t.Helper()
	r := NewResolver(allow)
	r.Upstream = upstream
	s := NewDNSServer(r)
	if _, err := s.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(s.Shutdown)

	udp, _ := s.LocalAddrs()
	if udp == nil {
		t.Fatal("no UDP address")
	}
	return udp.String()
}

func query(t *testing.T, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	c := &dns.Client{Timeout: 5 * time.Second}
	resp, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("Exchange(%s): %v", name, err)
	}
	return resp
}

func fixedUpstream(ip string) func(context.Context, string) ([]net.IP, error) {
	return func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP(ip)}, nil
	}
}

func TestDNSAnswersAllowedName(t *testing.T) {
	addr := startDNS(t, mustParse(t, "api.anthropic.com"), fixedUpstream("203.0.113.5"))

	resp := query(t, addr, "api.anthropic.com", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %v", resp.Answer)
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok || a.A.String() != "203.0.113.5" {
		t.Fatalf("answer = %v", resp.Answer[0])
	}
}

func TestDNSRefusesUnlistedName(t *testing.T) {
	called := false
	addr := startDNS(t, mustParse(t, "api.anthropic.com"),
		func(context.Context, string) ([]net.IP, error) {
			called = true
			return nil, nil
		})

	resp := query(t, addr, "exfil.evil.example.com", dns.TypeA)
	if resp.Rcode != dns.RcodeRefused {
		t.Fatalf("rcode = %s, want REFUSED", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 0 {
		t.Errorf("refused query returned answers: %v", resp.Answer)
	}
	if called {
		t.Error("refused query was still forwarded upstream")
	}
}

func TestDNSRefusesRatherThanLyingWithNXDOMAIN(t *testing.T) {
	// NXDOMAIN would assert the name does not exist and invites clients to
	// cache that. REFUSED reports what actually happened: policy.
	addr := startDNS(t, mustParse(t), fixedUpstream("203.0.113.5"))
	resp := query(t, addr, "example.com", dns.TypeA)
	if resp.Rcode == dns.RcodeNameError {
		t.Fatal("policy denial reported as NXDOMAIN")
	}
	if resp.Rcode != dns.RcodeRefused {
		t.Fatalf("rcode = %s, want REFUSED", dns.RcodeToString[resp.Rcode])
	}
}

func TestDNSRefusesTunnellingRecordTypes(t *testing.T) {
	// TXT is the classic DNS-tunnel carrier. Even for an allowlisted name,
	// nothing in a dev container needs it through this path.
	addr := startDNS(t, mustParse(t, "api.anthropic.com"), fixedUpstream("203.0.113.5"))

	for _, qtype := range []uint16{dns.TypeTXT, dns.TypeNULL, dns.TypeMX} {
		resp := query(t, addr, "api.anthropic.com", qtype)
		if resp.Rcode != dns.RcodeRefused {
			t.Errorf("qtype %d: rcode = %s, want REFUSED",
				qtype, dns.RcodeToString[resp.Rcode])
		}
	}
}

func TestDNSWildcardName(t *testing.T) {
	addr := startDNS(t, mustParse(t, "*.githubusercontent.com"), fixedUpstream("203.0.113.9"))

	if resp := query(t, addr, "raw.githubusercontent.com", dns.TypeA); resp.Rcode != dns.RcodeSuccess {
		t.Errorf("wildcard name refused: %s", dns.RcodeToString[resp.Rcode])
	}
	if resp := query(t, addr, "githubusercontent.com", dns.TypeA); resp.Rcode != dns.RcodeRefused {
		t.Errorf("bare parent allowed: %s", dns.RcodeToString[resp.Rcode])
	}
}

func TestDNSAAAAQueryGetsOnlyV6Answers(t *testing.T) {
	addr := startDNS(t, mustParse(t, "example.com"),
		func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.5"), net.ParseIP("2001:db8::1")}, nil
		})

	resp := query(t, addr, "example.com", dns.TypeAAAA)
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %v", resp.Answer)
	}
	if _, ok := resp.Answer[0].(*dns.AAAA); !ok {
		t.Fatalf("A record returned for an AAAA query: %v", resp.Answer[0])
	}
}

func TestDNSUpstreamFailureIsServFail(t *testing.T) {
	addr := startDNS(t, mustParse(t, "example.com"),
		func(context.Context, string) ([]net.IP, error) {
			return nil, context.DeadlineExceeded
		})

	resp := query(t, addr, "example.com", dns.TypeA)
	if resp.Rcode != dns.RcodeServerFailure {
		t.Fatalf("rcode = %s, want SERVFAIL", dns.RcodeToString[resp.Rcode])
	}
}
