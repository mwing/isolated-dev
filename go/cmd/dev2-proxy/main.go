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
	)
	flag.Parse()

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
