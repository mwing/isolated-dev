package netpolicy

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, entries ...string) *Allowlist {
	t.Helper()
	a, err := Parse(entries)
	if err != nil {
		t.Fatalf("Parse(%v): %v", entries, err)
	}
	return a
}

func TestEmptyAllowlistDeniesEverything(t *testing.T) {
	// Deny-by-default has to survive the degenerate case: a missing or
	// empty policy must never read as "allow".
	a := mustParse(t)
	if !a.Empty() {
		t.Error("expected Empty()")
	}
	if a.Allows("example.com", 443) {
		t.Error("empty allowlist permitted a host")
	}
	var nilList *Allowlist
	if nilList.Allows("example.com", 443) || nilList.AllowsName("example.com") {
		t.Error("nil allowlist permitted a host")
	}
}

func TestExactHostMatching(t *testing.T) {
	a := mustParse(t, "api.anthropic.com")

	if !a.Allows("api.anthropic.com", 443) {
		t.Error("exact host denied")
	}
	if !a.Allows("API.Anthropic.COM", 443) {
		t.Error("host matching must be case-insensitive")
	}
	if !a.Allows("api.anthropic.com.", 443) {
		t.Error("trailing dot should normalize")
	}
	if a.Allows("anthropic.com", 443) {
		t.Error("parent domain must not match an exact rule")
	}
	if a.Allows("evil-api.anthropic.com", 443) {
		t.Error("sibling host must not match")
	}
	// The classic suffix-matching bug: a naive strings.HasSuffix would let
	// an attacker register notapi.anthropic.com.evil.com.
	if a.Allows("api.anthropic.com.evil.com", 443) {
		t.Error("suffix confusion: unrelated domain matched")
	}
}

func TestWildcardMatchesSubdomainsButNotParent(t *testing.T) {
	a := mustParse(t, "*.githubusercontent.com")

	for _, h := range []string{
		"raw.githubusercontent.com",
		"objects.raw.githubusercontent.com",
	} {
		if !a.Allows(h, 443) {
			t.Errorf("wildcard should match %q", h)
		}
	}
	if a.Allows("githubusercontent.com", 443) {
		t.Error("*.example.com must not match the bare parent")
	}
	if a.Allows("evilgithubusercontent.com", 443) {
		t.Error("wildcard matched without a label boundary")
	}
}

func TestDefaultPortsOnly(t *testing.T) {
	a := mustParse(t, "github.com")

	if !a.Allows("github.com", 443) || !a.Allows("github.com", 80) {
		t.Error("default ports should be permitted")
	}
	// "allow github.com" must not become an SSH tunnel.
	if a.Allows("github.com", 22) {
		t.Error("port 22 allowed by a bare hostname rule")
	}
	if a.Allows("github.com", 5432) {
		t.Error("arbitrary port allowed by a bare hostname rule")
	}
}

func TestExplicitPortRule(t *testing.T) {
	a := mustParse(t, "db.internal:5432")

	if !a.Allows("db.internal", 5432) {
		t.Error("explicit port denied")
	}
	if a.Allows("db.internal", 443) {
		t.Error("explicit port rule must not also permit the defaults")
	}
}

func TestLiteralAddressesRequireTheirOwnRule(t *testing.T) {
	// Otherwise a client bypasses every hostname rule by resolving the name
	// itself and dialling the address.
	a := mustParse(t, "api.anthropic.com")
	if a.Allows("160.79.104.10", 443) {
		t.Error("hostname rule authorized a literal address")
	}

	b := mustParse(t, "10.0.0.5:5432")
	if !b.Allows("10.0.0.5", 5432) {
		t.Error("literal address rule denied")
	}
	if b.Allows("10.0.0.6", 5432) {
		t.Error("wrong address allowed")
	}
	if b.Allows("10.0.0.5", 443) {
		t.Error("wrong port allowed")
	}
}

func TestIPv6Literal(t *testing.T) {
	a := mustParse(t, "[2606:4700::1111]:443")
	if !a.Allows("2606:4700::1111", 443) {
		t.Error("IPv6 literal rule denied")
	}
	if a.Allows("2606:4700::1112", 443) {
		t.Error("wrong IPv6 address allowed")
	}
}

func TestAllowsNameIgnoresPortsAndAddresses(t *testing.T) {
	// The resolver answers names; ports are the proxy's business. But an
	// address-only rule must not authorize resolving arbitrary names.
	a := mustParse(t, "db.internal:5432", "10.0.0.5")

	if !a.AllowsName("db.internal") {
		t.Error("name with an explicit-port rule should resolve")
	}
	if a.AllowsName("anything.else") {
		t.Error("unlisted name resolved")
	}
	if a.AllowsName("10.0.0.5") {
		t.Error("address rule authorized name resolution")
	}
}

func TestParseRejectsMalformedEntries(t *testing.T) {
	for _, bad := range []string{
		"*",
		"*.com",
		"*.",
		"host with space",
		"http://example.com/path",
		"example.com:0",
		"example.com:99999",
		"example.com:notaport",
	} {
		if _, err := Parse([]string{bad}); err == nil {
			t.Errorf("Parse(%q) should have failed", bad)
		}
	}
}

func TestParseSkipsBlanksAndComments(t *testing.T) {
	a, err := Parse([]string{"", "   ", "# a comment", "example.com"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := len(a.Rules()); got != 1 {
		t.Fatalf("got %d rules, want 1", got)
	}
}

func TestRuleStringRoundTrips(t *testing.T) {
	cases := map[string]string{
		"example.com":      "example.com",
		"*.example.com":    "*.example.com",
		"example.com:8443": "example.com:8443",
	}
	for in, want := range cases {
		a := mustParse(t, in)
		if got := a.Rules()[0].String(); got != want {
			t.Errorf("Rule(%q).String() = %q, want %q", in, got, want)
		}
	}
}

// A wildcard is only as narrow as its parent. Under a public suffix it is
// not narrow at all: it admits every name anyone can create there,
// including one created because the grant exists.
func TestWildcardsUnderAPublicSuffixAreRefused(t *testing.T) {
	for _, entry := range []string{
		"*.co.uk", "*.com", "*.github.io", "*.s3.amazonaws.com",
		"*.herokuapp.com", "*.storage.googleapis.com", "*.ngrok.io",
	} {
		if _, err := Parse([]string{entry}); err == nil {
			t.Errorf("%s was accepted; it allows anything anyone creates there", entry)
		}
	}
}

func TestOrdinaryWildcardsStillParse(t *testing.T) {
	for _, entry := range []string{
		"*.example.com", "*.githubusercontent.com", "*.internal.mycorp.net",
	} {
		a, err := Parse([]string{entry})
		if err != nil {
			t.Fatalf("%s was refused: %v", entry, err)
		}
		if !a.Allows("anything."+strings.TrimPrefix(entry, "*."), 443) {
			t.Fatalf("%s does not match its own subdomain", entry)
		}
	}
}

// The refusal has to say what the grant would have meant. "Public suffix"
// is jargon; "anyone can create a name there" is the consequence.
func TestThePublicSuffixRefusalExplainsItself(t *testing.T) {
	_, err := Parse([]string{"*.co.uk"})
	if err == nil {
		t.Fatal("accepted")
	}
	if !strings.Contains(err.Error(), "anyone can register") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}
