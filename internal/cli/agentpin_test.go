package cli

import (
	"io"
	"strings"

	"github.com/mwing/isolated-dev/internal/runner"
	"testing"
)

// buildDockerfile returns the Dockerfile handed on stdin to the build whose
// tag contains want. The overlay has no build context, so the file itself is
// only visible here — asserting on the argv would prove nothing about what
// was built.
func buildDockerfile(t *testing.T, h *harness, want string) string {
	t.Helper()
	for _, c := range h.fake.Snapshot() {
		var isBuild, tagged bool
		for i, a := range c.Args {
			if a == "build" {
				isBuild = true
			}
			if (a == "-t" || a == "--tag") && i+1 < len(c.Args) &&
				strings.Contains(c.Args[i+1], want) {
				tagged = true
			}
		}
		if !isBuild || !tagged || c.Stdin == nil {
			continue
		}
		body, err := io.ReadAll(c.Stdin)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	t.Fatalf("no build tagged %q ran", want)
	return ""
}

// The review's finding: the agent overlay is built FROM mutable tags —
// `debian:bookworm-slim` for the base, `node:22-bookworm-slim` for the
// runtime — while the tool asks every project to pin its own. The one build
// the tool performs itself was the one exempt from its own rule.
func TestAnAgentOverlayHonoursTheProjectsPins(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeGlobal(t, "agent_clone: false\n")
	// The overlay is only built when its tag is absent, which is the state
	// this test is about.
	h.fake.Response[dockerKey("image", "inspect", "dev-agent-claude")] =
		runner.Result{ExitCode: 1}
	h.writeProject(t, "pins:\n"+
		"  \"debian:bookworm-slim\": \"debian@sha256:aaa\"\n"+
		"  \"node:22-bookworm-slim\": \"node@sha256:bbb\"\n")

	if err := h.run(t, "agent", "run", "claude", "--tty", "off",
		"--image", "debian:bookworm-slim"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, h.stderr.String())
	}

	df := buildDockerfile(t, h, "dev-agent-claude")
	for _, want := range []string{"FROM debian@sha256:aaa", "FROM node@sha256:bbb"} {
		if !strings.Contains(df, want) {
			t.Errorf("the overlay was built from a tag, not the pinned digest "+
				"(missing %q):\n%s", want, df)
		}
	}
}

// And the pins have to be resolvable in the first place: `dev pin` reads the
// project's Dockerfile, which never mentions the agent's images, so pinning
// them was impossible however much the build honoured them.
func TestPinResolvesTheAgentsOwnImages(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	writeDockerfile(t, h, "FROM alpine:3.20\n")
	// Every inspect answers with a digest, so the command reaches every
	// image rather than stopping at the first one the fake cannot resolve.
	h.fake.Response[dockerKey("inspect", "--format")] = runner.Result{
		Stdout: "example@sha256:aaa\n",
	}

	var pulled []string
	if err := h.run(t, "pin"); err != nil {
		t.Fatalf("pin: %v\n%s", err, h.stderr.String())
	}
	for _, args := range h.dockerArgs() {
		if len(args) > 1 && args[0] == "pull" {
			pulled = append(pulled, args[len(args)-1])
		}
	}
	for _, want := range []string{"debian:bookworm-slim", "node:22-bookworm-slim"} {
		var found bool
		for _, p := range pulled {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("pin never resolved %s; pulled %v", want, pulled)
		}
	}
}
