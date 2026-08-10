package cli

import (
	"strings"
	"testing"
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
