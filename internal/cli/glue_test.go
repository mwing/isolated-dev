package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/runner"
	"github.com/mwing/isolated-dev/internal/trust"
)

// These tests drive the real cobra command tree and assert on the docker
// invocations it would have produced. The tier exists because every P0 bug
// in the work queue survived a green suite: the tests stopped at the spec
// builders, and the bugs were all in the glue above them.

func TestWorkspaceRunRendersAHardenedContainerOnTheInternalNetwork(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, strings.Join(h.fake.Lines(), "\n"))
	}

	args := h.workloadRun(t)
	for _, want := range []string{
		"--user", "1000:1000", // the tool sets the uid, not the image
		"--network", internalNetwork, // no route out except the sidecar
		"--dns", "172.31.0.2", // the sidecar's filtering resolver
	} {
		if !contains(args, want) {
			t.Errorf("missing %q in:\n%s", want, argv(args))
		}
	}

	// A project that asks for nothing gets nothing: closed by default, and
	// said out loud rather than looking like a broken network later.
	if allow := h.sidecarAllow(t); len(allow) != 0 {
		t.Errorf("allowlist for a bare project = %v, want empty", allow)
	}
	if !strings.Contains(h.stderr.String(), "nothing is allowed") {
		t.Errorf("an empty allowlist was not explained:\n%s", h.stderr.String())
	}
}

// existingNetwork makes `network create --internal` report that the network
// is already there, which is the state a crashed run leaves behind.
func (h *harness) existingNetwork(internal bool) {
	h.fake.Response[dockerKey("network", "create", "--internal", internalNetwork)] =
		runner.Result{ExitCode: 1, Stderr: "Error response from daemon: network with name " +
			internalNetwork + " already exists\n"}
	h.fake.Response[dockerKey("network", "inspect", "--format", "{{.Internal}}", internalNetwork)] =
		runner.Result{Stdout: fmt.Sprintf("%v\n", internal)}
}

func TestARunRefusesAPreExistingNetworkThatIsNotInternal(t *testing.T) {
	// The worst outcome the tool can produce is this one: no error, no
	// warning, `network: allowlist` printed, and the workload holding a
	// default route the proxy never sees.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.existingNetwork(false)

	err := h.run(t, "run", "--tty", "off", "-c", "true")
	if err == nil {
		t.Fatal("the run proceeded on a network with a route out")
	}
	if !strings.Contains(err.Error(), "dev clean --all") {
		t.Errorf("the error does not name the fix: %v", err)
	}
	for _, args := range h.dockerRuns() {
		t.Errorf("something was started anyway: %s", argv(args))
	}
}

func TestARunReusesAPreExistingInternalNetwork(t *testing.T) {
	// The check must not turn a normal second run into a failure: an
	// internal network left behind is exactly what should be reused.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.existingNetwork(true)

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, strings.Join(h.fake.Lines(), "\n"))
	}
	h.workloadRun(t)
}

func TestAnUnreadableInternalFlagRefuses(t *testing.T) {
	// A network whose isolation cannot be read is a network whose isolation
	// cannot be relied on.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.existingNetwork(false)
	h.fake.Response[dockerKey("network", "inspect", "--format", "{{.Internal}}", internalNetwork)] =
		runner.Result{Stdout: "<no value>\n"}

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err == nil {
		t.Fatal("the run proceeded without knowing whether the network was internal")
	}
}

// requestOneHost is a project file asking for a single destination on
// behalf of every agent — the "default" key, which is what a plain run
// resolves against.
const requestOneHost = `agents:
  default:
    allow_hosts:
      - internal.example.com
`

func TestAcceptedHostsApplyToPlainRuns(t *testing.T) {
	// `dev agent accept` records a decision; `dev run` has to honor it.
	// Two commands disagreeing about what is permitted is the bug, and it
	// looks to the user like the acceptance did nothing.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeProject(t, requestOneHost)
	h.acceptHosts(t, "default", "internal.example.com")

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, strings.Join(h.fake.Lines(), "\n"))
	}
	if allow := h.sidecarAllow(t); !contains(allow, "internal.example.com") {
		t.Fatalf("accepted host absent from the allowlist for `dev run`: %v", allow)
	}
}

func TestAnUnacceptedRequestGrantsNothingToAPlainRun(t *testing.T) {
	// The project file is a request. A clone must not widen its own egress
	// by being run.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeProject(t, requestOneHost)

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if allow := h.sidecarAllow(t); contains(allow, "internal.example.com") {
		t.Fatalf("an unaccepted request applied itself: %v", allow)
	}
}

func TestRunAndConsoleResolveTheSameAllowlist(t *testing.T) {
	// Both commands go through workspaceAllowlist, which is the point: the
	// divergence was two call sites assembling the set independently, and
	// only one of them including what the user had accepted.
	h := newHarness(t)
	h.writeProject(t, requestOneHost)
	h.acceptHosts(t, "default", "internal.example.com")
	h.grantHosts(t, "default", "granted.example.com")

	cfg, p, err := resolveProject(h.env)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trust.Load(h.paths.Home, p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	got := workspaceAllowlist(cfg, p, store)
	for _, want := range []string{"internal.example.com", "granted.example.com"} {
		if !contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}

// autoApprove is the argument the built-in claude definition carries. It is
// what --safe exists to drop.
const autoApprove = "--dangerously-skip-permissions"

func TestSafeFlagReachesTheContainer(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()

	if err := h.run(t, "agent", "run", "claude", "--safe", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, strings.Join(h.fake.Lines(), "\n"))
	}
	if args := h.workloadRun(t); contains(args, autoApprove) {
		t.Fatalf("--safe did not drop the auto-approve argument:\n%s", argv(args))
	}
}

func TestWithoutSafeTheAgentAutoApproves(t *testing.T) {
	// The inverse matters as much: the auto-approve default is defensible
	// only because the sandbox is the boundary, and a --safe fix that
	// dropped it unconditionally would be the same bug facing the other way.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()

	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, strings.Join(h.fake.Lines(), "\n"))
	}
	if args := h.workloadRun(t); !contains(args, autoApprove) {
		t.Fatalf("the agent's own default arguments were dropped:\n%s", argv(args))
	}
}

func TestSafeFlagShowsInTheDryRun(t *testing.T) {
	// --dry-run is how a cautious person checks before running, so the
	// printed invocation has to agree with the real one.
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"with --safe", []string{"agent", "run", "claude", "--safe", "--dry-run"}, false},
		{"without --safe", []string{"agent", "run", "claude", "--dry-run"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.readyBackend()
			if err := h.run(t, tc.args...); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if got := strings.Contains(h.stdout.String(), autoApprove); got != tc.want {
				t.Fatalf("auto-approve present = %v, want %v, in:\n%s",
					got, tc.want, h.stdout.String())
			}
		})
	}
}
