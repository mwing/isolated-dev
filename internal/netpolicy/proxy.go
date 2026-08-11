package netpolicy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
// It never terminates TLS. CONNECT targets are checked by hostname, the SNI
// inside the session is checked against that hostname as the opening record
// streams past (see clienthello.go), and the bytes are relayed untouched, so
// certificate pinning keeps working and the proxy never sees plaintext. The
// cost, accepted deliberately in ROADMAP 4.3, is that filtering is per-host
// and not per-path.
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

	// IdleTimeout bounds a relayed connection's inactivity: it is extended
	// whenever bytes move in either direction, so a busy connection lives
	// as long as it is busy and a silent one is closed.
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

	// transport serves the plain-HTTP path. One per request was one
	// connection pool per request, none of them ever closed: a loop of
	// requests leaked a socket and its goroutines each time round, and the
	// sidecar is the process that has to survive a long session.
	transportOnce sync.Once
	transport     *http.Transport

	mu      sync.Mutex
	denials map[string]int
	// refused are destinations the user said no to. Without recording the
	// answer, every retry would be held for the full timeout again while
	// the console stayed silent, which reads as a hang.
	refused map[string]bool
}

// maxTrackedDestinations bounds the maps keyed by destinations the workload
// chose. A client retrying against generated names would otherwise grow
// them without limit, inside the one process that has to survive a whole
// session — and the report they feed is unreadable long before that.
const maxTrackedDestinations = 2048

// overflowKey stands in for everything past the cap, so the summary says
// that it is incomplete rather than quietly being wrong.
const overflowKey = "(further destinations not recorded)"

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
	// Control is called with the resolved address, after DNS and before
	// the connection is made, which is the only place the check can be
	// both correct and unavoidable: checking the resolved IP beforehand
	// leaves a window in which the answer changes.
	d.Control = func(_, address string, _ syscall.RawConn) error {
		return p.permitAddress(address)
	}
	return d.DialContext(ctx, "tcp", addr)
}

// exfilShape applies the resolver's name-shape limits to a destination the
// workload asked to connect to, and reports why it was refused.
//
// The resolver checked these and this path did not, which left the primary
// channel open: under a `*.example.com` grant, CONNECT
// <payload>.example.com:443 resolves through the system resolver with no
// shape check at all, and the name has delivered its content before any
// answer comes back. Filtering one of two doors is filtering neither.
//
// Only names under a wildcard rule are checked. An exact grant names a
// destination the user chose, and its shape is not theirs to justify.
func (p *Proxy) exfilShape(host string) string {
	if net.ParseIP(host) != nil {
		return ""
	}
	a := p.Allowlist()
	if a == nil || !a.matchedByWildcard(host) {
		return ""
	}
	return suspiciousQuery(normalizeHost(host))
}

// permitAddress refuses to connect to the infrastructure around the
// sandbox, whatever name resolved to it.
//
// An allowlisted hostname whose DNS an attacker influences otherwise
// becomes a route to the docker gateway, the host, or a cloud metadata
// endpoint — none of which the allowlist was ever asked about, and all of
// which are reachable from the sidecar because it is the one container with
// a way out.
//
// A rule naming the address literally still works. Someone proxying to a
// service on their own network has said so explicitly, which is exactly the
// distinction the literal-IP rule already makes for the allowlist.
func (p *Proxy) permitAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !isInfrastructure(ip) {
		return nil
	}
	if a := p.Allowlist(); a != nil && a.AllowsIP(ip) {
		return nil
	}
	return &blockedAddress{Address: address, Host: host}
}

// blockedAddress is the verdict for a destination that resolved into the
// machinery around the sandbox. It is a distinct type so the handlers can
// tell a policy decision from a network fault: reported as "upstream
// error", a refusal reads as a flaky service, and the run's history records
// nothing about why the request failed.
type blockedAddress struct {
	// Address is host:port as dialled; Host is the address alone, which is
	// what a grant would have to name.
	Address string
	Host    string
}

func (e *blockedAddress) Error() string {
	// The remedy is named because this is the one refusal a legitimate
	// setup hits: an internal service on a private network, reached by a
	// name that resolves there. Granting the address says "I meant that
	// machine", which a hostname cannot say on its own.
	return fmt.Sprintf("refused to connect to %s: it is loopback, a private "+
		"network, or a metadata endpoint, and no rule names that address. "+
		"If you meant it, grant the address itself: dev allow %s", e.Host, e.Address)
}

// isInfrastructure reports whether an address belongs to the machinery
// around the sandbox rather than to the internet.
func isInfrastructure(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() ||
		// Carrier-grade NAT and the IPv6 unique-local range are neither
		// private by Go's definition nor public in any useful sense.
		ip.IsInterfaceLocalMulticast() || inCGNAT(ip) || isUniqueLocal(ip)
}

func inCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

func isUniqueLocal(ip net.IP) bool {
	return ip.To4() == nil && len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc
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
		// Bounded: the key is a destination the workload chose, so a client
		// retrying against generated names grows this without limit inside
		// the one process that must survive a long session. Past the cap,
		// known destinations still count — the tally stays accurate for
		// everything already seen, and stops learning new names.
		if len(p.denials) < maxTrackedDestinations {
			p.denials[key]++
		} else if _, known := p.denials[key]; known {
			p.denials[key]++
		} else {
			p.denials[overflowKey]++
		}
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
	// A refusal is a decision the user made, so it is kept even at the cap:
	// forgetting one would hold the next attempt for the full timeout again
	// over a question already answered. The bound is on how many distinct
	// ones can be recorded, which only a user clicking "no" thousands of
	// times can reach.
	if len(p.refused) < maxTrackedDestinations {
		p.refused[normalizeHost(host)] = true
	}
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
	if why := p.exfilShape(host); why != "" {
		p.emit(Event{Action: "deny", Host: host, Port: port, Method: r.Method, Reason: why})
		denyResponse(w, host, port)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxy: connection hijacking unsupported", http.StatusInternalServerError)
		return
	}

	upstream, err := p.dial(r.Context(), net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		// A refused address is a decision, and belongs in the denial
		// tally and the run's history with the rest of them.
		var blocked *blockedAddress
		if errors.As(err, &blocked) {
			p.emit(Event{Action: "deny", Host: host, Port: port, Method: r.Method,
				Reason: blocked.Error()})
			http.Error(w, "proxy: "+blocked.Error(), http.StatusForbidden)
			return
		}
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

	// The name inside the session has to be the name the allowlist approved.
	// Checking the CONNECT authority alone leaves the connection to an
	// allowed front open to being steered elsewhere by SNI, which is the one
	// bypass a per-host policy on a CDN cannot otherwise see.
	src := &sniffer{src: buf, verify: verifySNI(host)}

	start := p.now()
	n, verdict := relay(client, src, buf, upstream, p.IdleTimeout)
	if m, ok := verdict.(*sniMismatch); ok {
		// A fatal TLS alert rather than a bare close. The client is
		// mid-handshake and a dropped connection there is indistinguishable
		// from a flaky network; this reaches the developer staring at the
		// failed request, which is what denyResponse does for plain HTTP.
		_, _ = client.Write(tlsAccessDenied)
		p.emit(Event{Action: "deny", Host: m.Host(), Port: port, Method: r.Method,
			Bytes: n, Reason: m.Error()})
		return
	}
	p.emit(Event{Action: "allow", Host: host, Port: port, Method: r.Method,
		Bytes: n, Latency: p.now().Sub(start).String()})
}

// tlsAccessDenied is a TLS record carrying a fatal access_denied alert. It
// is sent in place of the ServerHello the client is waiting for. Writing an
// alert is not terminating the session: no handshake is performed, no key is
// negotiated, and nothing the client sent is decrypted.
var tlsAccessDenied = []byte{0x15, 0x03, 0x03, 0x00, 0x02, 0x02, 49}

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

	resp, err := p.roundTripper().RoundTrip(outreq)
	if err != nil {
		// A refused address is a policy decision, not a network fault.
		// Reported as "upstream error" it read as a flaky service, was
		// recorded as an error rather than a denial, and left nothing in
		// the run's history to explain what had happened.
		var blocked *blockedAddress
		if errors.As(err, &blocked) {
			p.emit(Event{Action: "deny", Host: host, Port: port, Method: r.Method,
				Reason: blocked.Error()})
			http.Error(w, "proxy: "+blocked.Error(), http.StatusForbidden)
			return
		}
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

// roundTripper returns the one transport the plain-HTTP path uses.
//
// Built on first use rather than in NewProxy because Dial is injected after
// construction, and a transport that captured the zero value would dial
// straight out, past the very substitution a test made to stop it.
func (p *Proxy) roundTripper() http.RoundTripper {
	p.transportOnce.Do(func() {
		p.transport = &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return p.dial(ctx, addr)
			},
			// Bounded: this process outlives every request through it, and a
			// workload naming many hosts must not grow the pool forever.
			MaxIdleConns:        64,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		}
	})
	return p.transport
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
//
// src is the client half, which the caller may wrap to inspect the opening
// bytes. When that wrapper refuses the connection its verdict is returned,
// and relay does not close the client's write side on the way out: the
// caller speaks last, and it can only do that if both copies have finished
// and nobody else is writing.
func relay(client net.Conn, src io.Reader, buf *bufio.ReadWriter,
	upstream net.Conn, idle time.Duration) (int64, error) {
	var total int64
	var mu sync.Mutex

	add := func(n int64) {
		mu.Lock()
		total += n
		mu.Unlock()
	}

	// A real idle timer, not a lifetime: both copies feed it, so the
	// connection lives as long as anything is moving in either direction.
	alive := newIdleTimer(idle, client, upstream)

	var aborting atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		n, _ := io.Copy(buf, alive.reader(upstream))
		add(n)
		_ = buf.Flush()
		if aborting.Load() {
			return
		}
		if c, ok := client.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		} else {
			_ = client.Close()
		}
	}()

	// The client half runs here rather than in a third goroutine so that a
	// refusal can order what follows: stop the upstream, wait for the other
	// direction to finish, and only then return to a caller that wants to
	// write one last thing to the client.
	n, err := io.Copy(upstream, alive.reader(src))
	add(n)
	// errors.As, not a type assertion: io.Copy hands the write side to
	// TCPConn.ReadFrom when it can, and that wraps whatever the reader
	// returned in a *net.OpError. The assertion silently missed it, and a
	// refusal that is silently missed is worse than one never written.
	var verdict *sniMismatch
	if errors.As(err, &verdict) {
		aborting.Store(true)
		_ = upstream.Close()
		<-done
		return total, verdict
	}
	if c, ok := upstream.(*net.TCPConn); ok {
		_ = c.CloseWrite()
	} else {
		_ = upstream.Close()
	}
	<-done
	return total, nil
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
