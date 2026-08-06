package netpolicy

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestResolverRefusesUnlistedNameWithoutLookingItUp(t *testing.T) {
	// The DNS-tunnel case: the query name carries the data, so an answer is
	// never needed. Forwarding it would be the leak.
	called := false
	r := NewResolver(mustParse(t, "api.anthropic.com"))
	r.Upstream = func(context.Context, string) ([]net.IP, error) {
		called = true
		return nil, nil
	}

	_, err := r.Resolve(context.Background(), "data-exfil.evil.example.com")
	var refused *ErrRefused
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
	if called {
		t.Fatal("resolver forwarded a denied query upstream")
	}
	if got := r.Denials()["data-exfil.evil.example.com"]; got != 1 {
		t.Errorf("denial tally = %d, want 1", got)
	}
}

func TestResolverAnswersAllowedName(t *testing.T) {
	r := NewResolver(mustParse(t, "api.anthropic.com"))
	r.Upstream = func(_ context.Context, name string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.5")}, nil
	}

	ips, err := r.Resolve(context.Background(), "api.anthropic.com")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "203.0.113.5" {
		t.Fatalf("ips = %v", ips)
	}
}

func TestResolverWildcardAndNormalization(t *testing.T) {
	r := NewResolver(mustParse(t, "*.githubusercontent.com"))
	r.Upstream = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.9")}, nil
	}

	for _, name := range []string{
		"raw.githubusercontent.com",
		"RAW.GithubUserContent.com",
		"raw.githubusercontent.com.",
	} {
		if _, err := r.Resolve(context.Background(), name); err != nil {
			t.Errorf("Resolve(%q): %v", name, err)
		}
	}
	if _, err := r.Resolve(context.Background(), "githubusercontent.com"); err == nil {
		t.Error("bare parent should be refused by a wildcard rule")
	}
}

func TestResolverEmptyNameRefused(t *testing.T) {
	r := NewResolver(mustParse(t, "example.com"))
	if _, err := r.Resolve(context.Background(), "   "); err == nil {
		t.Fatal("empty name should be refused")
	}
}

func TestResolverEmptyAllowlistRefusesEverything(t *testing.T) {
	r := NewResolver(mustParse(t))
	r.Upstream = func(context.Context, string) ([]net.IP, error) {
		t.Fatal("upstream called with an empty allowlist")
		return nil, nil
	}
	if _, err := r.Resolve(context.Background(), "example.com"); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestResolverEmitsEvents(t *testing.T) {
	c := &collector{}
	r := NewResolver(mustParse(t, "example.com"))
	r.Emit = c.emit
	r.Upstream = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.1")}, nil
	}

	_, _ = r.Resolve(context.Background(), "example.com")
	_, _ = r.Resolve(context.Background(), "blocked.example.org")

	events := c.all()
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Action != "allow" || events[0].Method != "DNS" {
		t.Errorf("first event = %+v", events[0])
	}
	if events[1].Action != "deny" || events[1].Host != "blocked.example.org" {
		t.Errorf("second event = %+v", events[1])
	}
}

func TestResolverUpstreamFailureIsReported(t *testing.T) {
	r := NewResolver(mustParse(t, "example.com"))
	r.Upstream = func(context.Context, string) ([]net.IP, error) {
		return nil, errors.New("server misbehaving")
	}
	if _, err := r.Resolve(context.Background(), "example.com"); err == nil {
		t.Fatal("expected the upstream error to surface")
	}
}
