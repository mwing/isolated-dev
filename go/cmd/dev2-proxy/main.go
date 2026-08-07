// Command dev2-proxy is the egress sidecar. It runs inside the container
// network as the only host with a route out, enforcing the allowlist for
// both connections and name resolution.
//
// It is deliberately a separate binary from dev2: it runs in a different
// trust domain (reachable by the workload) and should carry none of the
// CLI's filesystem or config access. Its policy arrives once at startup and
// cannot be changed at runtime — there is no control plane to reach.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mwing/isolated-dev/go/internal/netpolicy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dev2-proxy:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		proxyAddr = flag.String("proxy-addr", ":3128", "listen address for the HTTP CONNECT proxy")
		dnsAddr   = flag.String("dns-addr", ":53", "listen address for the filtering resolver")
		allowFlag = flag.String("allow", "", "comma-separated allowlist entries")
		allowFile = flag.String("allow-file", "", "file with one allowlist entry per line")
		noDNS     = flag.Bool("no-dns", false, "do not serve DNS")
		ctlPath   = flag.String("control-socket", "/run/dev2-control.sock",
			"unix socket for live policy changes; empty disables it")
		askTimeout = flag.Duration("ask-timeout", 0,
			"hold a denied connection this long while someone decides; 0 fails immediately")
		forwards multiFlag
	)
	flag.Var(&forwards, "forward",
		"publish a workload port as listen:container:port (repeatable)")
	flag.Parse()

	// Control-client mode, invoked through `docker exec` into this same
	// container: `dev2-proxy control allow example.com`. It exists here
	// rather than as a second binary so the runtime image stays a single
	// static file with no shell.
	if args := flag.Args(); len(args) > 0 && args[0] == "control" {
		return control(*ctlPath, args[1:])
	}

	entries, err := gatherEntries(*allowFlag, *allowFile)
	if err != nil {
		return err
	}
	allow, err := netpolicy.Parse(entries)
	if err != nil {
		return err
	}

	// An empty policy is legal and means "deny everything". Say so out
	// loud: a silent deny-all looks identical to a broken proxy.
	rules := allow.Rules()
	if len(rules) == 0 {
		fmt.Fprintln(os.Stderr, "dev2-proxy: empty allowlist; all egress will be denied")
	} else {
		names := make([]string, 0, len(rules))
		for _, r := range rules {
			names = append(names, r.String())
		}
		fmt.Fprintf(os.Stderr, "dev2-proxy: allowing %s\n", strings.Join(names, " "))
	}

	emit := jsonEmitter(os.Stdout)

	proxy := netpolicy.NewProxy(allow)
	proxy.Emit = emit
	proxy.AskTimeout = *askTimeout
	if *askTimeout > 0 {
		fmt.Fprintf(os.Stderr, "dev2-proxy: holding denied connections up to %s for a decision\n",
			*askTimeout)
	}

	srv := &http.Server{
		Addr:              *proxyAddr,
		Handler:           proxy,
		ReadHeaderTimeout: 30 * time.Second,
	}

	errs := make(chan error, 3)
	go func() { errs <- srv.ListenAndServe() }()
	fmt.Fprintf(os.Stderr, "dev2-proxy: proxy listening on %s\n", *proxyAddr)

	var dnsSrv *netpolicy.DNSServer
	if !*noDNS {
		resolver := netpolicy.NewResolver(allow)
		resolver.Emit = emit
		dnsSrv = netpolicy.NewDNSServer(resolver)
		dnsErrs, err := dnsSrv.ListenAndServe(*dnsAddr)
		if err != nil {
			return fmt.Errorf("starting DNS: %w", err)
		}
		go func() { errs <- <-dnsErrs }()
		fmt.Fprintf(os.Stderr, "dev2-proxy: resolver listening on %s\n", *dnsAddr)
	}

	if *ctlPath != "" {
		ctl := netpolicy.NewControl(proxy, resolverFor(dnsSrv), entries)
		ctl.OnChange = func(op, host string, rules []string) {
			// Policy changes belong in the same log as the decisions they
			// affect, or a later denial looks inexplicable.
			fmt.Fprintf(os.Stderr, "dev2-proxy: %s %s; policy now: %s\n",
				op, host, strings.Join(rules, " "))
		}
		if err := ctl.Listen(*ctlPath); err != nil {
			return err
		}
		defer ctl.Close()
		fmt.Fprintf(os.Stderr, "dev2-proxy: control socket at %s\n", *ctlPath)
	}

	for _, spec := range forwards {
		fwd, err := netpolicy.ParseForward(spec)
		if err != nil {
			return err
		}
		if err := fwd.Listen(); err != nil {
			return err
		}
		defer fwd.Close()
		fmt.Fprintf(os.Stderr, "dev2-proxy: forwarding %s\n", fwd)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errs:
		return err
	case <-sig:
	}

	if dnsSrv != nil {
		dnsSrv.Shutdown()
	}
	_ = srv.Close()

	// The exit summary is the point of the log: what did the workload try
	// to reach that policy stopped?
	for _, line := range netpolicy.Summary(proxy.Denials()) {
		fmt.Fprintln(os.Stderr, "dev2-proxy:", line)
	}
	return nil
}

// resolverFor returns the DNS server's resolver, or nil when DNS is off,
// so a policy change reaches name resolution as well as connections.
func resolverFor(s *netpolicy.DNSServer) *netpolicy.Resolver {
	if s == nil {
		return nil
	}
	return s.Resolver
}

// control is the client half, run inside the sidecar via docker exec.
func control(path string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: dev2-proxy control <allow|revoke|list|denials> [host]")
	}
	req := netpolicy.Request{Op: args[0]}
	if len(args) > 1 {
		req.Host = args[1]
	}
	resp, err := (netpolicy.Client{Path: path}).Do(req)
	if err != nil {
		return err
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func gatherEntries(flagValue, file string) ([]string, error) {
	var entries []string
	for _, e := range strings.Split(flagValue, ",") {
		if e = strings.TrimSpace(e); e != "" {
			entries = append(entries, e)
		}
	}
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading allowlist: %w", err)
		}
		entries = append(entries, strings.Split(string(raw), "\n")...)
	}
	return entries, nil
}

// jsonEmitter writes one JSON object per decision, so the parent can
// aggregate without parsing prose.
func jsonEmitter(w *os.File) func(netpolicy.Event) {
	enc := json.NewEncoder(w)
	return func(e netpolicy.Event) {
		_ = enc.Encode(e)
	}
}
