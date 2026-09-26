package netpolicy

import (
	"net"
	"strings"
	"testing"
)

func learningList(t *testing.T, deny ...string) *Allowlist {
	t.Helper()
	a, err := mustParse(t, "known.example.com").WithDeny(deny)
	if err != nil {
		t.Fatal(err)
	}
	return a.WithLearning()
}

func TestLearningAdmitsWhatNoRuleNames(t *testing.T) {
	a := learningList(t)
	for _, c := range []struct {
		host string
		port int
	}{
		{"unknown.example.org", 443},
		{"unknown.example.org", 8443},
	} {
		if !a.Allows(c.host, c.port) {
			t.Errorf("learning refused %s:%d", c.host, c.port)
		}
	}
	if !a.AllowsName("unknown.example.org") {
		t.Error("learning refused to resolve an unknown name")
	}
}

func TestLearningStillHonoursTheDeny(t *testing.T) {
	a := learningList(t, "forbidden.example.com")
	for _, h := range []string{"forbidden.example.com", "sub.forbidden.example.com"} {
		if a.Allows(h, 443) || a.AllowsName(h) {
			t.Errorf("learning admitted the denied %s", h)
		}
	}
}

// Denies are keyed by name, so a literal address would reach a denied host
// by its IP.
func TestLearningAdmitsNoLiteralAddress(t *testing.T) {
	a := learningList(t, "forbidden.example.com")
	for _, ip := range []string{"93.184.216.34", "2606:2800:220:1::1"} {
		if a.Allows(ip, 443) {
			t.Errorf("learning admitted the literal address %s", ip)
		}
	}
}

// The infrastructure override matches literal grants only. Learning must not
// widen it, or it would open loopback, the docker gateway and metadata
// endpoints — the addresses the check exists for.
func TestLearningDoesNotOpenInfrastructure(t *testing.T) {
	a := learningList(t)
	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "172.17.0.1"} {
		if a.AllowsIP(net.ParseIP(ip)) {
			t.Errorf("learning let the infrastructure address %s through", ip)
		}
	}
	p := NewProxy(a)
	if err := p.permitAddress("169.254.169.254:80"); err == nil {
		t.Error("the proxy would connect to the metadata endpoint while learning")
	}
}

// A learned name was chosen by the workload, so its shape is checked the
// way a wildcard match is; an exact rule is still exempt.
func TestALearnedNameIsShapeChecked(t *testing.T) {
	a := learningList(t)
	if !a.matchedByWildcard("anything.example.org") {
		t.Error("a learned name was treated as a chosen one")
	}
	if a.matchedByWildcard("known.example.com") {
		t.Error("an exact rule lost its exemption under learning")
	}
	long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example.org"
	if NewProxy(a).exfilShape(long) == "" {
		t.Error("an encoded-looking name passed the CONNECT shape check while learning")
	}
}

func TestCoversIgnoresLearning(t *testing.T) {
	a := learningList(t)
	if !a.Covers("known.example.com", 443) {
		t.Error("a named destination is not covered")
	}
	if a.Covers("unknown.example.org", 443) {
		t.Error("Covers counted a learned destination as named")
	}
	if !a.Allows("unknown.example.org", 443) {
		t.Error("Covers switched learning off on the allowlist it was asked about")
	}
}

// Parse drops the mode, so without re-applying it a mid-run grant would end
// learning.
func TestARuntimeGrantKeepsLearning(t *testing.T) {
	a := learningList(t)
	p := NewProxy(a)
	r := NewResolver(a)
	c := NewControl(p, r, []string{"known.example.com"}, nil)

	if resp := c.mutate("allow", "other.example.com", true); !resp.OK {
		t.Fatalf("grant failed: %s", resp.Error)
	}
	if !p.Allowlist().Allows("unknown.example.org", 443) {
		t.Error("a runtime grant ended learning on the proxy")
	}
	if !r.Allowlist().AllowsName("unknown.example.org") {
		t.Error("a runtime grant ended learning on the resolver")
	}
}

func TestWithoutLearningNothingChanges(t *testing.T) {
	a := mustParse(t, "known.example.com")
	if a.Allows("unknown.example.org", 443) || a.AllowsName("unknown.example.org") || a.Learning() {
		t.Error("an ordinary allowlist admitted an unnamed host")
	}
}

func TestReservedAndWrappedAddressesAreInfrastructure(t *testing.T) {
	for _, s := range []string{
		"0.250.250.254", // OrbStack's host.docker.internal: the Mac
		"0.0.0.1",
		"192.0.0.8",
		"240.0.0.1",
		"255.255.255.255",
		"64:ff9b::a9fe:a9fe", // NAT64 around 169.254.169.254
		"64:ff9b::a00:1",     // NAT64 around 10.0.0.1
		"2002:a9fe:a9fe::1",  // 6to4 around the metadata endpoint
		"::a9fe:a9fe",        // IPv4-compatible metadata endpoint
		"::ffff:10.0.0.1",    // IPv4-mapped private
		"fd07:b51a:cc66:f0::fe",
	} {
		if !isInfrastructure(net.ParseIP(s)) {
			t.Errorf("%s is not treated as infrastructure", s)
		}
	}
}

// NAT64 and 6to4 are refused by where they lead, not wholesale: on an
// IPv6-only network NAT64 is how public IPv4 is reached at all.
func TestWrappedPublicAddressesStayReachable(t *testing.T) {
	for _, s := range []string{
		"93.184.216.34", "64:ff9b::5db8:d822", "2002:5db8:d822::1", "2606:2800:220:1::1",
		"198.18.0.7", // fake-IP DNS answers public names from here
	} {
		if isInfrastructure(net.ParseIP(s)) {
			t.Errorf("%s was refused as infrastructure", s)
		}
	}
}

func TestLearningAdmitsOnlyGrantableNames(t *testing.T) {
	a := learningList(t)
	for _, h := range []string{"fe80::1%eth0", "::1%lo0", "a b", "exa*mple.com", "ехample.com"} {
		if a.Allows(h, 443) || a.AllowsName(h) {
			t.Errorf("learning admitted %q", h)
		}
	}
}

func TestAZonedAddressIsNeverDialled(t *testing.T) {
	p := NewProxy(learningList(t))
	for _, addr := range []string{"[fe80::1%eth0]:443", "[::1%lo0]:80", "[2606:2800:220:1::1%en0]:443"} {
		err := p.permitAddress(addr)
		if err == nil {
			t.Errorf("the proxy would dial %s", addr)
			continue
		}
		if strings.Contains(err.Error(), "dev allow") {
			t.Errorf("%s: offers a grant no rule can express: %v", addr, err)
		}
	}
}
