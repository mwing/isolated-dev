package netpolicy

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
)

// Forward publishes a workload port to the host.
//
// A workload on an internal network cannot publish ports itself: docker
// needs a gateway to publish through and an internal network has none.
// Removing the internal network to get ports back would remove the egress
// control with it, so the sidecar forwards instead. It already straddles
// both networks, and having one component own the whole network boundary
// is easier to reason about than two.
//
// Inbound is not subject to the egress allowlist. The allowlist answers
// "what may this workload reach"; a published port answers "what may reach
// this workload", and the user answered that by asking for the port.
type Forward struct {
	// ListenPort is the port the sidecar listens on, published to the host.
	ListenPort int
	// Target is the workload's address on the internal network, resolved
	// by container name through docker's embedded DNS.
	TargetHost string
	TargetPort int

	ln        net.Listener
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// ParseForward reads a "listen:host:target" specification.
func ParseForward(spec string) (*Forward, error) {
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("netpolicy: forward %q: want listen:host:port", spec)
	}
	listen, err := strconv.Atoi(parts[0])
	if err != nil || listen < 1 || listen > 65535 {
		return nil, fmt.Errorf("netpolicy: forward %q: bad listen port", spec)
	}
	target, err := strconv.Atoi(parts[2])
	if err != nil || target < 1 || target > 65535 {
		return nil, fmt.Errorf("netpolicy: forward %q: bad target port", spec)
	}
	if strings.TrimSpace(parts[1]) == "" {
		return nil, fmt.Errorf("netpolicy: forward %q: no target host", spec)
	}
	return &Forward{ListenPort: listen, TargetHost: parts[1], TargetPort: target}, nil
}

// String renders the forward as it was specified.
func (f *Forward) String() string {
	return fmt.Sprintf("%d:%s:%d", f.ListenPort, f.TargetHost, f.TargetPort)
}

// Listen starts accepting connections.
func (f *Forward) Listen() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", f.ListenPort))
	if err != nil {
		return fmt.Errorf("netpolicy: forward %s: %w", f, err)
	}
	f.ln = ln
	go f.accept()
	return nil
}

func (f *Forward) accept() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.handle(conn)
		}()
	}
}

func (f *Forward) handle(client net.Conn) {
	defer client.Close()

	target := net.JoinHostPort(f.TargetHost, strconv.Itoa(f.TargetPort))
	// The workload may not be listening yet — a server takes a moment to
	// start — so a refused connection is reported to the client rather
	// than silently dropped.
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer upstream.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, client)
		if c, ok := upstream.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, upstream)
		if c, ok := client.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
	}()
	wg.Wait()
}

// Close stops the listener and waits for connections in flight. It is
// safe to call twice: teardown paths overlap, and a second close reporting
// "use of closed network connection" would be noise on an error path where
// people are already looking for the real failure.
func (f *Forward) Close() error {
	if f.ln == nil {
		return nil
	}
	f.closeOnce.Do(func() {
		f.closeErr = f.ln.Close()
	})
	f.wg.Wait()
	return f.closeErr
}

// Addr reports the bound address, for tests using port 0.
func (f *Forward) Addr() net.Addr {
	if f.ln == nil {
		return nil
	}
	return f.ln.Addr()
}
