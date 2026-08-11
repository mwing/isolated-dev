package netpolicy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

// The resolver refused these and this path did not, which left the primary
// channel open: CONNECT <payload>.example.com carries the name to the
// system resolver with no shape check, and the name has delivered its
// content before any answer comes back.
func TestTheProxyRefusesAnEncodedNameUnderAWildcardGrant(t *testing.T) {
	p := NewProxy(mustParse(t, "*.example.com"))
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("an encoded name was dialled")
		return nil, nil
	}

	payload := strings.Repeat("qz7x", 20) + ".example.com"
	if status := statusOf(t, p, payload+":443"); !strings.Contains(status, "403") {
		t.Fatalf("status = %s, want a refusal", status)
	}
	if p.Denials()[payload+":443"] == 0 {
		t.Fatalf("the refusal was not recorded: %v", p.Denials())
	}
}

// An exact grant names a destination the user chose. Its shape is not
// theirs to justify, however unusual it looks.
func TestAnExactGrantIsNotShapeChecked(t *testing.T) {
	long := strings.Repeat("a", 60) + ".example.com"
	p := NewProxy(mustParse(t, long))

	var dialled bool
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		dialled = true
		return nil, fmt.Errorf("no upstream in this test")
	}
	statusOf(t, p, long+":443")
	if !dialled {
		t.Fatal("a destination the user named exactly was refused for its shape")
	}
}

// Ordinary names under a wildcard keep working, which is the whole reason
// the limits are generous.
func TestOrdinaryNamesUnderAWildcardStillConnect(t *testing.T) {
	p := NewProxy(mustParse(t, "*.example.com"))
	var dialled bool
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		dialled = true
		return nil, fmt.Errorf("no upstream in this test")
	}
	statusOf(t, p, "api.example.com:443")
	if !dialled {
		t.Fatal("an ordinary name under a wildcard was refused")
	}
}

// The resolver's own tally was missed when the others were bounded.
func TestTheResolverDenialTallyIsBounded(t *testing.T) {
	r := &Resolver{}
	r.SetAllowlist(mustParse(t, "allowed.example.com"))

	for i := 0; i < maxTrackedDestinations*2; i++ {
		_, _ = r.Resolve(context.Background(), fmt.Sprintf("h%d.denied.example", i))
	}
	got := r.Denials()
	if len(got) > maxTrackedDestinations+1 {
		t.Fatalf("resolver tally holds %d entries, cap is %d", len(got), maxTrackedDestinations)
	}
	if got[overflowKey] == 0 {
		t.Fatalf("nothing recorded that the tally is incomplete: %d entries", len(got))
	}
}

// A homoglyph in a wildcard's parent admits every name beneath it, so it
// matters more there than in an exact host — and the check used to sit
// after the branch that returns.
func TestAWildcardWithANonASCIIParentIsRefused(t *testing.T) {
	if _, err := Parse([]string{"*.еxample.com"}); err == nil { // Cyrillic е
		t.Fatal("a wildcard over a homoglyph domain was accepted")
	}
	if _, err := Parse([]string{"*.example.com"}); err != nil {
		t.Fatalf("the ordinary wildcard was refused: %v", err)
	}
}

// statusOf sends one CONNECT through a proxy and returns the status line.
func statusOf(t *testing.T, p *Proxy, target string) string {
	t.Helper()
	status, conn, _ := connectThrough(t, startProxy(t, p), target)
	t.Cleanup(func() { _ = conn.Close() })
	return status
}
