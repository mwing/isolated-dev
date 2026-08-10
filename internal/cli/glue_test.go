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
