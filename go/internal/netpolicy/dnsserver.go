package netpolicy

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/miekg/dns"
)

// DNSServer exposes a Resolver on the wire. It answers A and AAAA queries
// for allowlisted names and returns REFUSED for everything else.
//
// REFUSED rather than NXDOMAIN: NXDOMAIN asserts the name does not exist,
// which is a lie the resolver has no business telling, and it teaches
// clients to cache a false negative. REFUSED says "policy", which is what
// actually happened.
type DNSServer struct {
	Resolver *Resolver
	// Timeout bounds one upstream lookup.
	Timeout time.Duration

	udp *dns.Server
	tcp *dns.Server
}

// NewDNSServer wraps a resolver.
func NewDNSServer(r *Resolver) *DNSServer {
	return &DNSServer{Resolver: r, Timeout: 5 * time.Second}
}

// ServeDNS implements dns.Handler.
func (s *DNSServer) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.RecursionAvailable = true

	if len(req.Question) != 1 {
		resp.Rcode = dns.RcodeFormatError
		_ = w.WriteMsg(resp)
		return
	}

	q := req.Question[0]
	name := dns.Fqdn(q.Name)

	switch q.Qtype {
	case dns.TypeA, dns.TypeAAAA:
	default:
		// TXT and friends are the classic tunnelling record types. Nothing
		// in a dev container legitimately needs them through this path.
		s.Resolver.deny(trimDot(name))
		resp.Rcode = dns.RcodeRefused
		_ = w.WriteMsg(resp)
		return
	}

	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ips, err := s.Resolver.Resolve(ctx, trimDot(name))
	if err != nil {
		var refused *ErrRefused
		if errors.As(err, &refused) {
			resp.Rcode = dns.RcodeRefused
		} else {
			resp.Rcode = dns.RcodeServerFailure
		}
		_ = w.WriteMsg(resp)
		return
	}

	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil && q.Qtype == dns.TypeA {
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   v4,
			})
			continue
		}
		if ip.To4() == nil && q.Qtype == dns.TypeAAAA {
			resp.Answer = append(resp.Answer, &dns.AAAA{
				Hdr:  dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60},
				AAAA: ip,
			})
		}
	}
	_ = w.WriteMsg(resp)
}

func trimDot(name string) string {
	if len(name) > 1 && name[len(name)-1] == '.' {
		return name[:len(name)-1]
	}
	return name
}

// ListenAndServe starts the server on addr for both UDP and TCP. It returns
// once both listeners are bound; errors from serving arrive on the channel.
func (s *DNSServer) ListenAndServe(addr string) (<-chan error, error) {
	errs := make(chan error, 2)
	ready := make(chan struct{}, 2)

	s.udp = &dns.Server{Addr: addr, Net: "udp", Handler: s,
		NotifyStartedFunc: func() { ready <- struct{}{} }}
	s.tcp = &dns.Server{Addr: addr, Net: "tcp", Handler: s,
		NotifyStartedFunc: func() { ready <- struct{}{} }}

	go func() { errs <- s.udp.ListenAndServe() }()
	go func() { errs <- s.tcp.ListenAndServe() }()

	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case err := <-errs:
			return errs, err
		case <-time.After(5 * time.Second):
			return errs, errors.New("netpolicy: DNS server did not start")
		}
	}
	return errs, nil
}

// Shutdown stops both listeners.
func (s *DNSServer) Shutdown() {
	if s.udp != nil {
		_ = s.udp.Shutdown()
	}
	if s.tcp != nil {
		_ = s.tcp.Shutdown()
	}
}

// LocalAddrs reports the bound addresses, which is how tests find the port
// when addr used :0.
func (s *DNSServer) LocalAddrs() (udp net.Addr, tcp net.Addr) {
	if s.udp != nil && s.udp.PacketConn != nil {
		udp = s.udp.PacketConn.LocalAddr()
	}
	if s.tcp != nil && s.tcp.Listener != nil {
		tcp = s.tcp.Listener.Addr()
	}
	return udp, tcp
}
