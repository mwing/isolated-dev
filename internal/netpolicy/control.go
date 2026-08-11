package netpolicy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
)

// Control lets the policy be changed while a workload is running, which is
// what turns a denial from a notice printed afterwards into a question
// answered at the time — which is what the console renders.
//
// Where it listens is the entire security design. Section 4.3 fixes the
// sidecar's policy at startup precisely so a compromised workload cannot
// widen its own allowlist; a live control plane relaxes that, so it must
// be unreachable from the workload by construction rather than by rule:
//
//   - It is a unix socket inside the sidecar's OWN filesystem. It is not
//     bind-mounted from anywhere, so no other container shares it.
//   - It is never a network listener. The workload sits on the internal
//     network with the sidecar and could reach any port it opened there;
//     a socket in a filesystem the workload does not have is not
//     addressable from the network at all.
//   - Reaching it from outside requires `docker exec` into the sidecar,
//     which requires access to the docker API — host privilege the
//     workload does not have, since no socket is mounted into it.
//
// The consequence to keep in mind: anything that CAN talk to the docker
// daemon can change the policy. That is the same authority that could
// stop the sidecar outright, so it grants nothing new.
type Control struct {
	Proxy    *Proxy
	Resolver *Resolver
	// OnChange is called after every accepted change, so the sidecar can
	// log what was altered and by which operation.
	OnChange func(op, host string, rules []string)

	mu    sync.Mutex
	entry []string
	ln    net.Listener
}

// NewControl returns a control server over the current policy entries.
func NewControl(p *Proxy, r *Resolver, entries []string) *Control {
	return &Control{Proxy: p, Resolver: r, entry: append([]string(nil), entries...)}
}

// Request is one control operation.
type Request struct {
	// Op is "allow", "revoke", "refuse", "list" or "denials".
	Op   string `json:"op"`
	Host string `json:"host,omitempty"`
}

// Response is the reply.
type Response struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Rules   []string       `json:"rules,omitempty"`
	Denials map[string]int `json:"denials,omitempty"`
}

// maxSocketPath is the practical limit for a unix socket path: the
// sockaddr_un field is 104 bytes on macOS and 108 on Linux. Exceeding it
// fails with a bare "invalid argument", which says nothing about the
// cause.
const maxSocketPath = 100

// Listen serves the control protocol on a unix socket at path.
func (c *Control) Listen(path string) error {
	if len(path) > maxSocketPath {
		return fmt.Errorf("netpolicy: control socket path is %d bytes, over the %d-byte limit: %s",
			len(path), maxSocketPath, path)
	}
	// A stale socket from a crashed sidecar would make bind fail; the
	// container filesystem is ours alone, so removing it is safe.
	_ = os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("netpolicy: control socket: %w", err)
	}
	c.ln = ln
	go c.serve(ln)
	return nil
}

// Close stops the control server.
func (c *Control) Close() error {
	if c.ln == nil {
		return nil
	}
	return c.ln.Close()
}

func (c *Control) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go c.handle(conn)
	}
}

func (c *Control) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	dec := json.NewDecoder(bufio.NewReader(conn))
	enc := json.NewEncoder(conn)

	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			return
		}
		if err := enc.Encode(c.Apply(req)); err != nil {
			return
		}
	}
}

// Apply performs one operation. Exported so tests can exercise the
// protocol without a socket.
func (c *Control) Apply(req Request) Response {
	switch strings.ToLower(strings.TrimSpace(req.Op)) {
	case "allow":
		return c.mutate("allow", req.Host, true)
	case "revoke":
		return c.mutate("revoke", req.Host, false)
	case "refuse":
		// A decision, not a policy change: the destination was never in
		// the allowlist, and recording the "no" stops later attempts from
		// waiting for an answer that has already been given.
		if strings.TrimSpace(req.Host) == "" {
			return Response{Error: "no host given"}
		}
		c.Proxy.Refuse(req.Host)
		if c.OnChange != nil {
			c.OnChange("refuse", req.Host, nil)
		}
		return Response{OK: true}
	case "list":
		c.mu.Lock()
		defer c.mu.Unlock()
		return Response{OK: true, Rules: append([]string(nil), c.entry...)}
	case "denials":
		return Response{OK: true, Denials: c.Proxy.Denials()}
	default:
		return Response{Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

func (c *Control) mutate(op, host string, add bool) Response {
	host = strings.TrimSpace(host)
	if host == "" {
		return Response{Error: "no host given"}
	}

	c.mu.Lock()
	next := make([]string, 0, len(c.entry)+1)
	found := false
	for _, e := range c.entry {
		if e == host {
			found = true
			if !add {
				continue
			}
		}
		next = append(next, e)
	}
	if add && !found {
		next = append(next, host)
	}
	sort.Strings(next)

	// Parse before committing: a bad entry must not take the policy down
	// mid-run, which would fail every subsequent connection.
	allow, err := Parse(next)
	if err != nil {
		c.mu.Unlock()
		return Response{Error: err.Error()}
	}
	c.entry = next
	rules := append([]string(nil), next...)
	c.mu.Unlock()

	if add {
		// Allowing something previously declined must undo the refusal,
		// or the grant would have no effect.
		c.Proxy.Unrefuse(host)
	}
	c.Proxy.SetAllowlist(allow)
	if c.Resolver != nil {
		c.Resolver.SetAllowlist(allow)
	}
	if c.OnChange != nil {
		c.OnChange(op, host, rules)
	}
	return Response{OK: true, Rules: rules}
}

// Client talks to a control socket. It runs inside the sidecar, invoked
// through `docker exec`, because that is the only path in.
type Client struct{ Path string }

// Do sends one request and returns the response.
func (c Client) Do(req Request) (Response, error) {
	conn, err := net.Dial("unix", c.Path)
	if err != nil {
		return Response{}, fmt.Errorf("netpolicy: control: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}
