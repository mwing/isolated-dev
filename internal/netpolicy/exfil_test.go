package netpolicy

import (
	"context"
	"net"
	"strings"
	"testing"
)

// The names a wildcard grant is for.
func TestOrdinaryNamesAreForwarded(t *testing.T) {
	for _, name := range []string{
		"api.example.com",
		"raw.githubusercontent.com",
		"objects.githubusercontent.com",
		"a.very.deeply.nested.but.real.example.com",
		"registry-1.docker.io",
		"s3.eu-central-1.amazonaws.com",
	} {
		if why := suspiciousQuery(name); why != "" {
			t.Errorf("%s was refused: %s", name, why)
		}
	}
}

// The shapes an encoder produces. None of these needs an answer to have
// delivered its payload.
func TestEncodedNamesAreRefused(t *testing.T) {
	cases := map[string]string{
		"one long label":  strings.Repeat("a", 60) + ".example.com",
		"whole name long": strings.Repeat("ab.", 50) + "example.com",
		"deep":            "a.b.c.d.e.f.g.h.i.j.k.example.com",
	}
	for what, name := range cases {
		if suspiciousQuery(name) == "" {
			t.Errorf("%s was forwarded: %s", what, name)
		}
	}
}

// The refusal has to name the limit that was crossed, or the person whose
// legitimate name tripped it has nothing to act on.
func TestTheRefusalNamesTheLimit(t *testing.T) {
	why := suspiciousQuery(strings.Repeat("a", 60) + ".example.com")
	if !strings.Contains(why, "over the limit") {
		t.Fatalf("unhelpful: %q", why)
	}
	// A 60-character base32 label should not be echoed in full.
	if strings.Contains(why, strings.Repeat("a", 40)) {
		t.Fatalf("echoed the whole label: %q", why)
	}
}

// End to end through the resolver: the grant is real, the name is allowed
// by it, and the query is still not forwarded.
func TestTheResolverRefusesAnEncodedNameUnderAWildcardGrant(t *testing.T) {
	var asked []string
	r := &Resolver{
		Upstream: func(_ context.Context, name string) ([]net.IP, error) {
			asked = append(asked, name)
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
	}
	r.SetAllowlist(mustParse(t, "*.example.com"))

	if _, err := r.Resolve(context.Background(), "api.example.com"); err != nil {
		t.Fatalf("an ordinary name was refused: %v", err)
	}
	payload := strings.Repeat("qz7x", 20) + ".example.com"
	if _, err := r.Resolve(context.Background(), payload); err == nil {
		t.Fatal("an encoded name was resolved")
	}
	if len(asked) != 1 || asked[0] != "api.example.com" {
		t.Fatalf("upstream saw %v; the payload must never leave", asked)
	}
}
