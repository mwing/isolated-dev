package netpolicy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Resolver is the filtering DNS server that sits beside the proxy.
//
// It allowlists rather than forwards (ROADMAP 4.3). Forwarding every query
// and relying on the proxy to block connections would leave DNS tunnelling
// wide open: the query name itself carries the data, and an answer is never
// needed. A denied name is refused, never looked up.
//
// Clients that use the proxy do not need this resolver at all — the proxy
// resolves on their behalf — so it exists as a compatibility affordance for
// tools that resolve before they connect.
type Resolver struct {
	allow   *Allowlist
	allowMu sync.RWMutex

	// Upstream resolves an allowed name. Injected for tests.
	Upstream func(ctx context.Context, name string) ([]net.IP, error)
	// Emit receives every decision.
	Emit func(Event)
	// Now is the clock.
	Now func() time.Time

	mu      sync.Mutex
	denials map[string]int
}

// NewResolver returns a resolver enforcing allow.
func NewResolver(allow *Allowlist) *Resolver {
	return &Resolver{allow: allow, denials: map[string]int{}}
}

// Allowlist returns the policy in force.
func (r *Resolver) Allowlist() *Allowlist {
	r.allowMu.RLock()
	defer r.allowMu.RUnlock()
	return r.allow
}

// SetAllowlist replaces the policy for subsequent queries.
func (r *Resolver) SetAllowlist(a *Allowlist) {
	r.allowMu.Lock()
	r.allow = a
	r.allowMu.Unlock()
}

func (r *Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// ErrRefused reports a name the policy would not resolve.
type ErrRefused struct{ Name string }

func (e *ErrRefused) Error() string {
	return fmt.Sprintf("netpolicy: refused to resolve %q: not on the allowlist", e.Name)
}

// Resolve answers a query, or refuses it.
func (r *Resolver) Resolve(ctx context.Context, name string) ([]net.IP, error) {
	q := normalizeHost(strings.TrimSpace(name))
	if q == "" {
		return nil, &ErrRefused{Name: name}
	}

	if !r.Allowlist().AllowsName(q) {
		r.deny(q)
		return nil, &ErrRefused{Name: q}
	}

	// A wildcard grant reopens the channel the allowlist closed. With
	// *.example.com granted, the query name itself carries data out:
	// <payload>.example.com needs no answer to have delivered it, and it
	// is logged as an allow because the name is, strictly, permitted.
	//
	// The shape is what gives it away. Real names are short and few;
	// encoded ones are long, deep, and arrive in a stream. Refusing on
	// shape is a heuristic and is treated as one — it refuses the query
	// rather than revoking the grant, and says which limit was crossed.
	if why := suspiciousQuery(q); why != "" {
		r.emit(Event{Action: "deny", Host: q, Method: "DNS", Reason: why})
		return nil, &ErrRefused{Name: q}
	}

	up := r.Upstream
	if up == nil {
		up = defaultUpstream
	}
	ips, err := up(ctx, q)
	if err != nil {
		r.emit(Event{Action: "deny", Host: q, Method: "DNS",
			Reason: "upstream resolution failed: " + err.Error()})
		return nil, err
	}
	r.emit(Event{Action: "allow", Host: q, Method: "DNS"})
	return ips, nil
}

func defaultUpstream(ctx context.Context, name string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", name)
}

func (r *Resolver) deny(name string) {
	r.mu.Lock()
	if r.denials == nil {
		r.denials = map[string]int{}
	}
	// Bounded like the proxy's tally, and for the same reason: the key is a
	// name the workload chose, so a client spraying random names grows this
	// for the life of the run. This one was missed when the others were
	// capped, which is the whole argument for the overflow key — a map that
	// silently stops counting reports the wrong thing, and one that never
	// stops is the failure being prevented.
	switch {
	case len(r.denials) < maxTrackedDestinations:
		r.denials[name]++
	default:
		if _, known := r.denials[name]; known {
			r.denials[name]++
		} else {
			r.denials[overflowKey]++
		}
	}
	r.mu.Unlock()
	r.emit(Event{Action: "deny", Host: name, Method: "DNS", Reason: "not in allowlist"})
}

func (r *Resolver) emit(e Event) {
	e.Time = r.now()
	if r.Emit != nil {
		r.Emit(e)
	}
}

// Denials returns the refused-name tally, for the exit summary.
func (r *Resolver) Denials() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.denials))
	for k, v := range r.denials {
		out[k] = v
	}
	return out
}
