package cli

import (
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/agent"
	"github.com/mwing/isolated-dev/internal/runner"
)

// An agent brings its own base image, so a directory with nothing to
// detect must not be built for one.
//
// The image is reported absent deliberately: the fake answers every
// unlisted command with exit 0, which reads as "already built" and skips
// the build this is about.
//
// Reaching the terminal check, which sits well after the build, is what
// says the build never ran — this project has nothing it could build from,
// so any attempt is the failure the test names.
func TestAConsoleAgentDoesNotNeedAProjectImage(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()

	_, p, err := resolveProject(h.env)
	if err != nil {
		t.Fatal(err)
	}
	h.fake.Response[dockerKey("image", "inspect", p.Image)] = runner.Result{ExitCode: 1}

	err = h.run(t, "console", "--agent", "claude", "--in-place")
	if err == nil {
		t.Fatal("a console with no terminal should fail")
	}
	if strings.Contains(err.Error(), "no language detected") {
		t.Fatalf("console built the image the agent never uses: %v", err)
	}
	if !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("got %v, want the terminal check", err)
	}
}

// --rebuild promises "rebuild the image first", and with an agent the image
// that runs is the overlay. The console asked for it unconditionally
// unforced, so the flag rebuilt nothing an agent session would use.
//
// Asserted through the freshness check rather than the build: the overlay
// is built either way here, because the fake's label never matches the
// marker. Forcing is what skips asking, so the absent question is the
// signal that the flag arrived.
func TestAConsoleAgentHonoursRebuild(t *testing.T) {
	askedIfCurrent := func(t *testing.T, args ...string) bool {
		t.Helper()
		h := newHarness(t)
		h.readyBackend()
		h.readySidecar()
		if err := h.run(t, args...); err == nil {
			t.Fatal("a console with no terminal should fail")
		}
		for _, argv := range h.dockerArgs() {
			line := strings.Join(argv, " ")
			if strings.HasPrefix(line, "image inspect --format") &&
				strings.Contains(line, agent.SourceLabel) &&
				strings.Contains(line, "dev-agent-claude") {
				return true
			}
		}
		return false
	}

	if !askedIfCurrent(t, "console", "--agent", "claude", "--in-place") {
		t.Fatal("the overlay was never checked for freshness")
	}
	if askedIfCurrent(t, "console", "--agent", "claude", "--in-place", "--rebuild") {
		t.Fatal("--rebuild did not reach the agent overlay: it still asked")
	}
}
