package netpolicy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mwing/isolated-dev/go/internal/container"
)

// Topology is the network arrangement from ROADMAP 4.3, verified against a
// real OrbStack daemon:
//
//   - the workload attaches ONLY to an internal network. Docker gives such
//     a network no gateway, so a direct connection out fails with "network
//     unreachable" rather than succeeding.
//   - the sidecar starts on that internal network and is then connected to
//     a second, ordinary network. It is the only host with a route out.
//   - the workload runs with --dns pointing at the sidecar's internal
//     address, which redirects the daemon's embedded resolver to forward
//     only there. Without this the embedded resolver would still try the
//     host's servers.
type Topology struct {
	InternalNetwork string
	EgressNetwork   string
	SidecarName     string
	SidecarIP       string
	ProxyPort       int
	DNSPort         int
}

// ProxyURL is the value for HTTP_PROXY/HTTPS_PROXY inside the workload.
// These variables are a convenience for well-behaved clients, never the
// boundary: a process that ignores them has no route out regardless.
func (t Topology) ProxyURL() string {
	return fmt.Sprintf("http://%s:%d", t.SidecarIP, t.ProxyPort)
}

// Env returns the proxy environment for the workload.
func (t Topology) Env() []string {
	url := t.ProxyURL()
	return []string{
		"HTTP_PROXY=" + url,
		"HTTPS_PROXY=" + url,
		"http_proxy=" + url,
		"https_proxy=" + url,
		// Loopback and the sidecar itself must not be proxied.
		"NO_PROXY=localhost,127.0.0.1," + t.SidecarIP,
		"no_proxy=localhost,127.0.0.1," + t.SidecarIP,
	}
}

// Sidecar manages the egress proxy container's lifecycle.
type Sidecar struct {
	Engine *container.Engine
	// Image carries the dev2-proxy binary as its entrypoint.
	Image string
	// Allow is the policy handed to the sidecar at startup. It can be
	// changed afterwards only through the control socket, which is
	// reachable from the host and not from the workload (see Control).
	Allow []string
	// AskTimeout holds denied connections while someone decides. Zero
	// fails them immediately.
	AskTimeout time.Duration
	// Forwards publish workload ports through the sidecar, since a
	// workload on an internal network cannot publish them itself.
	Forwards []string
	// Ports are the host ports the sidecar publishes for those forwards.
	Ports []int

	Topology Topology
}

// Start creates the networks, launches the sidecar, connects it to egress,
// and resolves its internal address.
//
// The order matters. The sidecar joins the internal network first so that
// its address there is stable before the workload is told to use it, and
// egress is attached second so a misconfigured policy cannot leak during
// the window in between.
func (s *Sidecar) Start(ctx context.Context) (Topology, error) {
	t := s.Topology
	if t.ProxyPort == 0 {
		t.ProxyPort = 3128
	}
	if t.DNSPort == 0 {
		t.DNSPort = 53
	}

	if err := s.Engine.NetworkCreate(ctx, t.InternalNetwork, true); err != nil {
		return t, err
	}
	if err := s.Engine.NetworkCreate(ctx, t.EgressNetwork, false); err != nil {
		return t, err
	}

	spec := container.RunSpec{
		Image:   s.Image,
		Name:    t.SidecarName,
		Network: t.InternalNetwork,
		Detach:  true,
		Remove:  true,
		// The sidecar needs no privileges of its own; it only moves bytes.
		User:        "1000:1000",
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		PidsLimit:   256,
		Labels:      map[string]string{"dev2.role": "egress-sidecar"},
		Command: []string{
			"--allow", strings.Join(s.Allow, ","),
			"--proxy-addr", fmt.Sprintf(":%d", t.ProxyPort),
			"--dns-addr", fmt.Sprintf(":%d", t.DNSPort),
			"--ask-timeout", s.AskTimeout.String(),
		},
	}
	for _, f := range s.Forwards {
		spec.Command = append(spec.Command, "--forward", f)
	}
	for _, port := range s.Ports {
		spec.Ports = append(spec.Ports, container.PortMap{Host: port, Container: port})
	}

	if _, err := s.Engine.Run(ctx, spec, nil, io.Discard, io.Discard); err != nil {
		return t, fmt.Errorf("starting egress sidecar: %w", err)
	}

	if err := s.Engine.NetworkConnect(ctx, t.EgressNetwork, t.SidecarName); err != nil {
		_ = s.Engine.Remove(ctx, t.SidecarName)
		return t, fmt.Errorf("connecting sidecar to egress: %w", err)
	}

	ip, err := s.Engine.ContainerIP(ctx, t.SidecarName, t.InternalNetwork)
	if err != nil {
		_ = s.Engine.Remove(ctx, t.SidecarName)
		return t, err
	}
	t.SidecarIP = ip
	s.Topology = t
	return t, nil
}

// Grant changes the policy of a running sidecar through the control
// socket, entering via docker exec because that is the only path in.
func (s *Sidecar) Grant(ctx context.Context, op, host string) error {
	res, err := s.Engine.Exec(ctx, s.Topology.SidecarName,
		[]string{"/dev2-proxy", "control", op, host})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("netpolicy: %s %s: %s", op, host,
			strings.TrimSpace(res.Stderr+res.Stdout))
	}
	return nil
}

// Stop tears down the sidecar and its networks. It returns the denial
// summary gathered from the sidecar's log, which is the report the user
// actually cares about: what did the workload try to reach?
func (s *Sidecar) Stop(ctx context.Context) ([]string, error) {
	logs, logErr := s.Engine.Logs(ctx, s.Topology.SidecarName)

	_ = s.Engine.Stop(ctx, s.Topology.SidecarName)
	_ = s.Engine.Remove(ctx, s.Topology.SidecarName)
	_ = s.Engine.NetworkRemove(ctx, s.Topology.InternalNetwork)
	_ = s.Engine.NetworkRemove(ctx, s.Topology.EgressNetwork)

	if logErr != nil {
		return nil, logErr
	}
	return Summary(ParseDenials(logs)), nil
}

// ParseDenials aggregates deny events from the sidecar's JSON log. Lines
// that are not events (the sidecar's own diagnostics) are ignored.
func ParseDenials(logs string) map[string]int {
	out := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(logs))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.Action != "deny" {
			continue
		}
		key := e.Host
		if e.Port != 0 {
			key = fmt.Sprintf("%s:%d", e.Host, e.Port)
		}
		if e.Method == "DNS" {
			key = e.Host + " (DNS)"
		}
		out[key]++
	}
	return out
}
