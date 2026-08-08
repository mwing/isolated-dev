package netpolicy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event is one decision the proxy made. Events are emitted as JSON lines so
// the parent process can aggregate them into the exit summary ("blocked:
// evil.example.com x3") without parsing prose.
type Event struct {
	Time    time.Time `json:"time"`
	Action  string    `json:"action"` // "allow" or "deny"
	Host    string    `json:"host"`
	Port    int       `json:"port"`
	Method  string    `json:"method"`
	Reason  string    `json:"reason,omitempty"`
	Bytes   int64     `json:"bytes,omitempty"`
	Latency string    `json:"latency,omitempty"`
}

// Proxy is an HTTP CONNECT proxy that enforces an allowlist.
//
// It never terminates TLS. CONNECT targets are checked by hostname and the
// bytes are then relayed untouched, so certificate pinning keeps working
// and the proxy never sees plaintext. The cost, accepted deliberately in
// ROADMAP 4.3, is that filtering is per-host and not per-path.
type Proxy struct {
	// allow is replaceable at runtime so a live console can widen the
	// policy without restarting the workload. It is unexported and guarded
	// because the whole point of the sidecar is that the policy has one
	// owner; see Control for who is permitted to change it.
	allow   *Allowlist
	allowMu sync.RWMutex

	// Dial establishes the upstream connection. Injected for tests.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Now is the clock, injected for deterministic tests.
	Now func() time.Time
	// Emit receives every decision. Defaults to discarding.
	Emit func(Event)

	// IdleTimeout bounds a relayed connection's inactivity.
	IdleTimeout time.Duration

	// AskTimeout makes a denial block rather than fail immediately: the
	// connection is held while someone decides, and proceeds if the policy
	// gains the destination within the window. This is firewall behavior —
	// the request waits for a verdict instead of failing and needing a
	// retry the user may not be watching for.
	//
	// Zero reports and fails immediately, which is the right default where
	// nobody is present to answer: blocking in CI is a hang.
	AskTimeout time.Duration
	// AskPoll is how often a held connection re-checks the policy.
	AskPoll time.Duration

	mu      sync.Mutex
	denials map[string]int
	// refused are destinations the user said no to. Without recording the
	// answer, every retry would be held for the full timeout again while
	// the console stayed silent, which reads as a hang.
	refused map[string]bool
}

// NewProxy returns a proxy enforcing allow.
func NewProxy(allow *Allowlist) *Proxy {
	return &Proxy{
		allow:       allow,
		Now:         time.Now,
		IdleTimeout: 10 * time.Minute,
		denials:     map[string]int{},
		refused:     map[string]bool{},
	}
}

// Allowlist returns the policy in force.
func (p *Proxy) Allowlist() *Allowlist {
	p.allowMu.RLock()
	defer p.allowMu.RUnlock()
	return p.allow
}

// SetAllowlist replaces the policy for subsequent connections. Connections
// already established are not torn down: revoking a destination stops new
// traffic, it does not kill a transfer in flight.
func (p *Proxy) SetAllowlist(a *Allowlist) {
	p.allowMu.Lock()
	p.allow = a
	p.allowMu.Unlock()
}

func (p *Proxy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Proxy) dial(ctx context.Context, addr string) (net.Conn, error) {
	if p.Dial != nil {
		return p.Dial(ctx, "tcp", addr)
	}
	d := net.Dialer{Timeout: 30 * time.Second}
	return d.DialContext(ctx, "tcp", addr)
}

func (p *Proxy) emit(e Event) {
	e.Time = p.now()
	if e.Action == "deny" {
		p.mu.Lock()
		if p.denials == nil {
			p.denials = map[string]int{}
		}
		key := e.Host
		if e.Port != 0 {
			key = net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
		}
		p.denials[key]++
		p.mu.Unlock()
	}
	if p.Emit != nil {
		p.Emit(e)
	}
}

// Denials returns a copy of the blocked-destination tally.
func (p *Proxy) Denials() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.denials))
	for k, v := range p.denials {
		out[k] = v
	}
	return out
}

// Refuse records that the user declined a destination, so later attempts
// fail at once instead of waiting for a decision already made.
func (p *Proxy) Refuse(host string) {
	p.mu.Lock()
	if p.refused == nil {
		p.refused = map[string]bool{}
	}
	p.refused[normalizeHost(host)] = true
	p.mu.Unlock()
}

// Unrefuse forgets a refusal, so allowing a destination later works.
func (p *Proxy) Unrefuse(host string) {
	p.mu.Lock()
	delete(p.refused, normalizeHost(host))
	p.mu.Unlock()
}

func (p *Proxy) isRefused(host string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refused[normalizeHost(host)]
}

// ServeHTTP implements the proxy. CONNECT is the interesting path; plain
// HTTP is supported because package managers still use it and it is better
// to allowlist that traffic than to have users disable the proxy.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, port, err := splitTarget(r.Host, 443)
	if err != nil {
		p.emit(Event{Action: "deny", Host: r.Host, Method: r.Method, Reason: err.Error()})
		http.Error(w, "invalid CONNECT target", http.StatusBadRequest)
		return
	}

	if !p.Allowlist().Allows(host, port) {
		if !p.awaitDecision(r.Context(), host, port, r.Method) {
			p.emit(Event{Action: "deny", Host: host, Port: port, Method: r.Method,
				Reason: "not in allowlist"})
			denyResponse(w, host, port)
			return
		}
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxy: connection hijacking unsupported", http.StatusInternalServerError)
		return
	}

	upstream, err := p.dial(r.Context(), net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		// An unreachable upstream is a network failure, not a policy
		// decision. Recording it as a denial made the exit summary claim
		// the allowlist blocked something it had just allowed.
		p.emit(Event{Action: "error", Host: host, Port: port, Method: r.Method,
			Reason: "upstream dial failed: " + err.Error()})
		http.Error(w, "proxy: upstream unreachable", http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

	client, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}

	start := p.now()
	n := relay(client, buf, upstream, p.IdleTimeout)
	p.emit(Event{Action: "allow", Host: host, Port: port, Method: r.Method,
		Bytes: n, Latency: p.now().Sub(start).String()})
}

func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		// A relative request means someone pointed a browser at the proxy
		// rather than configuring it as one.
		http.Error(w, "proxy: absolute URL required", http.StatusBadRequest)
		return
	}
	host, port, err := splitTarget(r.URL.Host, 80)
	if err != nil {
		p.emit(Event{Action: "deny", Host: r.URL.Host, Method: r.Method, Reason: err.Error()})
		http.Error(w, "invalid target", http.StatusBadRequest)
		return
	}
	// Plain HTTP holds for a decision exactly as CONNECT does. Anything
	// that asks for a host without a scheme lands here — `curl example.com`
	// is http:// — so leaving this path out made the prompt look broken
	// for the most ordinary command someone would type.
	if !p.Allowlist().Allows(host, port) {
		if !p.awaitDecision(r.Context(), host, port, r.Method) {
			p.emit(Event{Action: "deny", Host: host, Port: port, Method: r.Method,
				Reason: "not in allowlist"})
			denyResponse(w, host, port)
			return
		}
	}

	outreq := r.Clone(r.Context())
	outreq.RequestURI = ""
	removeHopByHop(outreq.Header)

	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return p.dial(ctx, addr)
	}}
	resp, err := transport.RoundTrip(outreq)
	if err != nil {
		p.emit(Event{Action: "error", Host: host, Port: port, Method: r.Method,
			Reason: "upstream error: " + err.Error()})
		http.Error(w, "proxy: upstream error", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	removeHopByHop(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	n, _ := io.Copy(w, resp.Body)
	p.emit(Event{Action: "allow", Host: host, Port: port, Method: r.Method, Bytes: n})
}

// awaitDecision holds a denied connection while someone decides, and
// reports whether the policy came to permit it.
//
// It polls rather than waiting on a signal: the decision arrives through
// the control socket, which knows nothing about connections in flight, and
// a poll keeps those two paths independent. The wait ends early when the
// client gives up, so a held connection cannot outlive its request.
func (p *Proxy) awaitDecision(ctx context.Context, host string, port int, method string) bool {
	if p.AskTimeout <= 0 {
		return false
	}
	// An answer already given is not a question. Asking again — or worse,
	// silently holding for the timeout — is what made a declined
	// destination look like a hang.
	if p.isRefused(host) {
		return false
	}
	p.emit(Event{Action: "pending", Host: host, Port: port, Method: method,
		Reason: "waiting for a decision"})

	poll := p.AskPoll
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := p.now().Add(p.AskTimeout)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if p.Allowlist().Allows(host, port) {
				p.emit(Event{Action: "granted", Host: host, Port: port, Method: method,
					Reason: "allowed while waiting"})
				return true
			}
			if p.now().After(deadline) {
				p.emit(Event{Action: "timeout", Host: host, Port: port, Method: method,
					Reason: "no decision within " + p.AskTimeout.String()})
				return false
			}
		}
	}
}

// denyResponse explains the block in a way that reaches the developer
// staring at a failed request, not just the log.
func denyResponse(w http.ResponseWriter, host string, port int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, "blocked by isolated-dev egress policy: %s:%d is not on the allowlist\n\n",
		host, port)
	fmt.Fprintf(w, "If this destination is needed, re-run with:\n  --allow-host %s\n", host)
}

// splitTarget parses host:port, applying a default port when absent.
func splitTarget(target string, defaultPort int) (string, int, error) {
	if target == "" {
		return "", 0, fmt.Errorf("empty target")
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		// No port present.
		return strings.Trim(target, "[]"), defaultPort, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", portStr)
	}
	return host, port, nil
}

var hopByHop = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopByHop(h http.Header) {
	for _, k := range hopByHop {
		h.Del(k)
	}
}

// relay copies bytes in both directions until either side closes, and
// returns the total relayed.
func relay(client net.Conn, buf *bufio.ReadWriter, upstream net.Conn, idle time.Duration) int64 {
	var wg sync.WaitGroup
	var total int64
	var mu sync.Mutex

	add := func(n int64) {
		mu.Lock()
		total += n
		mu.Unlock()
	}

	touch := func(c net.Conn) {
		if idle > 0 {
			_ = c.SetDeadline(time.Now().Add(idle))
		}
	}
	touch(client)
	touch(upstream)

	wg.Add(2)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(upstream, buf)
		add(n)
		if c, ok := upstream.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		} else {
			_ = upstream.Close()
		}
	}()
	go func() {
		defer wg.Done()
		n, _ := io.Copy(buf, upstream)
		add(n)
		_ = buf.Flush()
		if c, ok := client.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		} else {
			_ = client.Close()
		}
	}()
	wg.Wait()
	return total
}

// Summary renders the denial tally for the exit report, most-blocked
// destination first and alphabetically within a tie. The order is part of
// the contract: ranging over the map put the report in a different order on
// every run, which made the summary unreadable across runs and impossible
// to assert on.
func Summary(denials map[string]int) []string {
	if len(denials) == 0 {
		return nil
	}
	dests := make([]string, 0, len(denials))
	for dest := range denials {
		dests = append(dests, dest)
	}
	sort.Slice(dests, func(i, j int) bool {
		if denials[dests[i]] != denials[dests[j]] {
			return denials[dests[i]] > denials[dests[j]]
		}
		return dests[i] < dests[j]
	})

	out := make([]string, 0, len(dests))
	for _, dest := range dests {
		if n := denials[dest]; n != 1 {
			out = append(out, fmt.Sprintf("blocked: %s x%d", dest, n))
			continue
		}
		out = append(out, "blocked: "+dest)
	}
	return out
}

// MarshalEvent renders an event as a JSON line.
func MarshalEvent(e Event) ([]byte, error) { return json.Marshal(e) }
