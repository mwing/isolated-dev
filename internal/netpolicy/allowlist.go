// Package netpolicy implements v2's egress control: the allowlist, the
// CONNECT proxy that enforces it, and the filtering resolver beside it.
//
// The security boundary is the network topology (ROADMAP 4.3): the workload
// sits on an internal network with no route out, and this proxy is the only
// path to the outside. Proxy environment variables are a convenience for
// well-behaved clients, not the control. A process that ignores them does
// not escape; it simply fails to connect.
package netpolicy

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// DefaultPorts are the ports a bare hostname rule permits. Anything else
// must be requested explicitly as host:port, so that "allow github.com"
// cannot quietly become an SSH or database tunnel.
var DefaultPorts = []int{80, 443}

// Rule is one parsed allowlist entry.
type Rule struct {
	// Host is the exact hostname, or the parent domain when Wildcard is set.
	Host string
	// Wildcard means the rule was written *.example.com and matches
	// subdomains at any depth, but not example.com itself.
	Wildcard bool
	// IP is set when the rule names a literal address rather than a name.
	IP net.IP
	// Ports restricts the rule. Empty means DefaultPorts.
	Ports []int
}

// Allowlist decides whether a destination may be reached.
type Allowlist struct {
	rules []Rule
}

// Parse builds an allowlist from entries such as:
//
//	api.anthropic.com          exact host, ports 80 and 443
//	*.githubusercontent.com    any subdomain, ports 80 and 443
//	registry.npmjs.org:443     exact host and port
//	10.0.0.5:5432              literal address and port
//
// An empty list denies everything, which is the correct default for a
// deny-by-default tool: a missing policy must never mean "allow".
func Parse(entries []string) (*Allowlist, error) {
	a := &Allowlist{}
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		r, err := parseEntry(entry)
		if err != nil {
			return nil, err
		}
		a.rules = append(a.rules, r)
	}
	return a, nil
}

func parseEntry(entry string) (Rule, error) {
	host := entry
	var ports []int

	// A colon may be a port separator or part of a bare IPv6 literal.
	if h, p, err := net.SplitHostPort(entry); err == nil {
		port, perr := strconv.Atoi(p)
		if perr != nil || port < 1 || port > 65535 {
			return Rule{}, fmt.Errorf("netpolicy: %q: invalid port %q", entry, p)
		}
		host, ports = h, []int{port}
	}

	// Before the wildcard branch, not after it: the branch returns, so a
	// check placed below it never saw `*.<non-ascii>` at all — and a
	// homoglyph in a wildcard's parent is worse than in an exact host,
	// since it admits every name beneath it.
	if err := checkASCIIHost(strings.TrimPrefix(host, "*.")); err != nil {
		return Rule{}, fmt.Errorf("netpolicy: %w", err)
	}

	if strings.HasPrefix(host, "*.") {
		parent := strings.TrimPrefix(host, "*.")
		if parent == "" {
			return Rule{}, fmt.Errorf("netpolicy: %q: wildcard needs a parent domain (e.g. *.example.com)", entry)
		}
		// A wildcard is only as narrow as its parent. Under a public
		// suffix it is not narrow at all: it admits every name anyone can
		// create there, including one created because the grant exists.
		if isPublicSuffix(parent) {
			return Rule{}, fmt.Errorf(
				"netpolicy: %q would allow far more than it looks: %s.\n"+
					"Name the hosts you need, or the specific subdomain",
				entry, suffixAdvice(parent))
		}
		return Rule{Host: normalizeHost(parent), Wildcard: true, Ports: ports}, nil
	}

	if ip := net.ParseIP(host); ip != nil {
		return Rule{IP: ip, Ports: ports}, nil
	}

	if host == "" || strings.ContainsAny(host, "*/ ") {
		return Rule{}, fmt.Errorf("netpolicy: %q: not a valid host", entry)
	}
	return Rule{Host: normalizeHost(host), Ports: ports}, nil
}

// normalizeHost lowercases and strips a trailing dot so that
// "API.Example.com." and "api.example.com" are the same destination.
func normalizeHost(h string) string {
	return strings.ToLower(strings.TrimSuffix(h, "."))
}

// Allows reports whether host:port may be reached. host may be a name or a
// literal address.
//
// Literal addresses are denied unless a rule names that address. Allowing
// them by default would let any client bypass every hostname rule by
// resolving a name itself and dialling the result.
func (a *Allowlist) Allows(host string, port int) bool {
	if a == nil {
		return false
	}
	h := normalizeHost(host)
	ip := net.ParseIP(h)

	for _, r := range a.rules {
		if !portAllowed(r, port) {
			continue
		}
		switch {
		case r.IP != nil:
			if ip != nil && r.IP.Equal(ip) {
				return true
			}
		case ip != nil:
			// A hostname rule never authorizes a literal address.
			continue
		case r.Wildcard:
			if strings.HasSuffix(h, "."+r.Host) {
				return true
			}
		default:
			if h == r.Host {
				return true
			}
		}
	}
	return false
}

// AllowsName reports whether a name may be resolved. The resolver uses this
// so DNS cannot become an exfiltration channel of its own: a denied name is
// never even looked up.
func (a *Allowlist) AllowsName(name string) bool {
	if a == nil {
		return false
	}
	h := normalizeHost(name)
	for _, r := range a.rules {
		switch {
		case r.IP != nil:
			continue
		case r.Wildcard:
			if strings.HasSuffix(h, "."+r.Host) {
				return true
			}
		default:
			if h == r.Host {
				return true
			}
		}
	}
	return false
}

func portAllowed(r Rule, port int) bool {
	ports := r.Ports
	if len(ports) == 0 {
		ports = DefaultPorts
	}
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

// Rules returns the parsed rules, for display in confirmations and logs.
func (a *Allowlist) Rules() []Rule {
	if a == nil {
		return nil
	}
	return append([]Rule(nil), a.rules...)
}

// String renders a rule the way it was written.
func (r Rule) String() string {
	var host string
	switch {
	case r.IP != nil:
		host = r.IP.String()
	case r.Wildcard:
		host = "*." + r.Host
	default:
		host = r.Host
	}
	if len(r.Ports) == 0 {
		return host
	}
	parts := make([]string, 0, len(r.Ports))
	for _, p := range r.Ports {
		parts = append(parts, net.JoinHostPort(host, strconv.Itoa(p)))
	}
	return strings.Join(parts, ",")
}

// Empty reports whether the allowlist permits nothing at all.
func (a *Allowlist) Empty() bool { return a == nil || len(a.rules) == 0 }

// matchedByWildcard reports whether a name is permitted only because a
// wildcard rule covers it, rather than by a rule naming it exactly.
//
// The distinction decides whether the name's shape is the user's choice or
// the workload's: an exact grant is a destination someone chose, a wildcard
// admits names nobody has seen.
func (a *Allowlist) matchedByWildcard(host string) bool {
	if a == nil {
		return false
	}
	h := normalizeHost(host)
	var wild bool
	for _, r := range a.rules {
		if r.IP != nil {
			continue
		}
		if !r.Wildcard && h == r.Host {
			return false
		}
		if r.Wildcard && strings.HasSuffix(h, "."+r.Host) {
			wild = true
		}
	}
	return wild
}

// AllowsIP reports whether a rule names this address literally.
//
// Used by the proxy to let a deliberate grant of an address on a private
// network through the infrastructure check. A hostname rule never matches
// here: that is the same distinction Allows makes, for the same reason —
// resolving a name to an address must not be a way to reach an address
// nobody granted.
func (a *Allowlist) AllowsIP(ip net.IP) bool {
	if a == nil {
		return false
	}
	for _, r := range a.rules {
		if r.IP != nil && r.IP.Equal(ip) {
			return true
		}
	}
	return false
}
