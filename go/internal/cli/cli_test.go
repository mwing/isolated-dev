package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/runner"
	"github.com/mwing/isolated-dev/go/internal/trust"
)

type harness struct {
	env    *Env
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	fake   *runner.Fake
	paths  config.Paths
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	paths := config.DefaultPaths(filepath.Join(dir, "home"), filepath.Join(dir, "project"))
	if err := os.MkdirAll(paths.ProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := &harness{
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		fake:   runner.NewFake(),
		paths:  paths,
	}
	h.env = &Env{
		Stdout: h.stdout,
		Stderr: h.stderr,
		Env:    nil,
		Paths:  paths,
		Runner: h.fake,
	}
	return h
}

func (h *harness) run(t *testing.T, args ...string) error {
	t.Helper()
	cmd := NewRootCmd(h.env)
	cmd.SetArgs(args)
	cmd.SetOut(h.stdout)
	cmd.SetErr(h.stderr)
	return cmd.Execute()
}

func (h *harness) writeProject(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(h.paths.Project, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readyBackend makes the fake runner look like a healthy OrbStack VM so a
// test can reach the checks that come after the backend probe. The PATH
// lookup is injected too: whether the host running the suite has orb
// installed must not change the result.
func (h *harness) readyBackend() {
	h.env.LookPath = func(bin string) (string, bool) {
		if bin == "orb" {
			return "/usr/local/bin/orb", true
		}
		return "", false
	}
	h.fake.Response["orb list"] = runner.Result{
		Stdout: "NAME                STATE    DISTRO  ARCH\n" +
			"dev-vm-docker-host  running  ubuntu  arm64\n",
	}
	h.fake.Response["orb -m dev-vm-docker-host sudo docker version"] = runner.Result{
		Stdout: "27.1.1\n",
	}
}

const proxyInspect = "orb -m dev-vm-docker-host sudo docker image inspect dev2-proxy:latest"

func TestVersionShort(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "version", "--short"); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got := strings.TrimSpace(h.stdout.String()); got != Version {
		t.Fatalf("stdout = %q, want %q", got, Version)
	}
}

func TestVersionLongIncludesPlatform(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "version"); err != nil {
		t.Fatalf("version: %v", err)
	}
	out := h.stdout.String()
	for _, want := range []string{"dev2 ", "go:", "platform:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDoctorFailsWhenBackendNotReady(t *testing.T) {
	h := newHarness(t)
	// Fake returns a zero Result for everything: orb list produces no VMs.
	err := h.run(t, "doctor")
	if err == nil {
		t.Fatal("doctor should exit non-zero when the backend is not ready")
	}
	if !strings.Contains(h.stdout.String(), "Not ready") {
		t.Errorf("output did not explain the failure:\n%s", h.stdout.String())
	}
}

func TestDoctorReportsGrantsRequestedByProjectConfig(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, `mount_ssh_keys: true
pass_env_vars:
  patterns:
    - AWS_*
`)
	_ = h.run(t, "doctor")

	out := h.stdout.String()
	if !strings.Contains(out, "grants requested by config") {
		t.Fatalf("grants not surfaced:\n%s", out)
	}
	if !strings.Contains(out, "~/.ssh") || !strings.Contains(out, "AWS_*") {
		t.Errorf("grant detail missing:\n%s", out)
	}
}

func TestDoctorReportsNoGrantsForDefaultProject(t *testing.T) {
	h := newHarness(t)
	_ = h.run(t, "doctor")
	if !strings.Contains(h.stdout.String(), "grants:        none") {
		t.Errorf("expected the default posture to be stated:\n%s", h.stdout.String())
	}
}

func TestDoctorWarnsAboutDeadKeys(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, "network_mode: host\n")
	_ = h.run(t, "doctor")

	out := h.stdout.String()
	if !strings.Contains(out, "network_mode") || !strings.Contains(out, "never implemented") {
		t.Errorf("dead key not reported:\n%s", out)
	}
}

func TestDoctorSurfacesMalformedConfig(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, "vm_name: [unclosed\n")
	err := h.run(t, "doctor")
	if err == nil {
		t.Fatal("expected an error for malformed config")
	}
}

func TestDoctorReportsProxyImagePresent(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.fake.Response[proxyInspect] = runner.Result{Stdout: "[]\n"}

	if err := h.run(t, "doctor"); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "✓  image dev2-proxy:latest") {
		t.Errorf("sidecar image not reported as present:\n%s", out)
	}
	if !strings.Contains(out, "Ready.") {
		t.Errorf("expected a ready verdict:\n%s", out)
	}
}

func TestDoctorReportsMissingProxyImageWithRemedy(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.fake.Response[proxyInspect] = runner.Result{
		ExitCode: 1,
		Stderr:   "Error: No such image: dev2-proxy:latest\n",
	}

	err := h.run(t, "doctor")
	if err == nil {
		t.Fatal("a missing sidecar image blocks every agent run; doctor should exit non-zero")
	}
	out := h.stdout.String()
	if !strings.Contains(out, "✗  image dev2-proxy:latest") {
		t.Errorf("missing image not reported:\n%s", out)
	}
	if !strings.Contains(out, "make proxy-image") {
		t.Errorf("remedy not offered:\n%s", out)
	}
}

func TestDoctorChecksProxyImageWithoutBuilding(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.fake.Response[proxyInspect] = runner.Result{ExitCode: 1}
	_ = h.run(t, "doctor")

	inspected := false
	for _, line := range h.fake.Lines() {
		if line == proxyInspect {
			inspected = true
		}
		// doctor diagnoses; repairing the image is `make proxy-image`.
		if strings.Contains(line, "docker build") {
			t.Errorf("doctor built something: %s", line)
		}
	}
	if !inspected {
		t.Errorf("image was not checked through the backend, calls:\n%s",
			strings.Join(h.fake.Lines(), "\n"))
	}
}

func TestVerboseFlagEnablesCommandLogging(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	cmd := NewRootCmd(h.env)
	cmd.SetArgs([]string{"version", "--verbose"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !h.env.Verbose() {
		t.Fatal("--verbose did not reach the Env")
	}
}

func TestProjectAsksOnlyCountFromTheProjectFile(t *testing.T) {
	// The global file is the user's own machine and needs no consent from
	// them; a default is nobody's request. Only the project file asks.
	h := newHarness(t)
	h.writeProject(t, "network: open\n")
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	asks := projectAsks(cfg)
	if len(asks) != 1 || asks[0].Key != "network" || asks[0].Value != "open" {
		t.Fatalf("asks = %+v", asks)
	}
	if !strings.Contains(asks[0].Effect, "egress") {
		t.Errorf("effect should say what accepting does: %q", asks[0].Effect)
	}
}

func TestGlobalSettingIsNotAnAsk(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Dir(h.paths.Global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.paths.Global, []byte("network: open\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	if asks := projectAsks(cfg); len(asks) != 0 {
		t.Fatalf("global config produced asks: %+v", asks)
	}
}

func TestAskingForLessAccessNeedsNoConsent(t *testing.T) {
	// `network: none` removes access. Prompting for it would train users
	// to click through prompts that never mattered.
	h := newHarness(t)
	h.writeProject(t, "network: none\n")
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	if asks := projectAsks(cfg); len(asks) != 0 {
		t.Fatalf("restricting access prompted: %+v", asks)
	}
}

func TestRunIsBlockedUntilSettingsAreAccepted(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, "network: open\n")
	err := h.run(t, "run", "-c", "echo hi")
	if err == nil {
		t.Fatal("run proceeded with unaccepted project settings")
	}
	if !strings.Contains(h.stderr.String(), "dev2 accept") {
		t.Errorf("error should name the remedy:\n%s", h.stderr.String())
	}
}

func TestSearchScriptHandlesEachPackageManager(t *testing.T) {
	// The image family is not known in advance: it comes from the project,
	// or from whatever base a language template chose.
	script := searchScript("ripgrep", 25)
	for _, mgr := range []string{"apt-get", "apk", "dnf"} {
		if !strings.Contains(script, mgr) {
			t.Errorf("no %s branch:\n%s", mgr, script)
		}
	}
	if !strings.Contains(script, "head -n 25") {
		t.Errorf("limit not applied:\n%s", script)
	}
}

func TestSearchTermIsQuoted(t *testing.T) {
	// The term goes into a shell script. A term carrying a quote or a
	// semicolon would otherwise run as a command inside the container.
	script := searchScript("rip'; curl evil.example.com; #", 10)
	if strings.Contains(script, "curl evil.example.com;\n") {
		t.Fatalf("term escaped its quoting:\n%s", script)
	}
	if !strings.Contains(script, `'\''`) {
		t.Errorf("quote not escaped:\n%s", script)
	}
}

func TestToolsCommandsAreGroupedUnderTools(t *testing.T) {
	// The prefix says what you are doing; `dev2 add` alone did not.
	h := newHarness(t)
	root := NewRootCmd(h.env)

	var tools *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "tools" {
			tools = c
		}
		if c.Name() == "add" || c.Name() == "remove" {
			t.Errorf("%q is still a top-level command", c.Name())
		}
	}
	if tools == nil {
		t.Fatal("no tools command")
	}
	want := map[string]bool{"add": false, "remove": false, "list": false, "search": false}
	for _, c := range tools.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("dev2 tools %s missing", name)
		}
	}
}

func TestSharedToolsAreARequestNotAGrant(t *testing.T) {
	// A repository must not be able to add packages to an image silently:
	// they install during a build, which runs unfiltered.
	h := newHarness(t)
	h.writeProject(t, "tools:\n  - jq\n  - ripgrep\n")

	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	asks := projectAsks(cfg)
	if len(asks) != 1 || asks[0].Key != "tools" {
		t.Fatalf("asks = %+v", asks)
	}
	if !strings.Contains(asks[0].Effect, "jq") {
		t.Errorf("effect should name the packages: %q", asks[0].Effect)
	}
}

func TestProjectToolsApplyOnlyOnceAccepted(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, "tools:\n  - jq\n")
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}

	if got := effectiveTools(cfg, store); len(got) != 0 {
		t.Fatalf("unaccepted project tools applied: %v", got)
	}
	if _, err := store.AcceptSettings([]trust.Ask{{Key: "tools", Value: "jq"}}); err != nil {
		t.Fatal(err)
	}
	if got := effectiveTools(cfg, store); len(got) != 1 || got[0] != "jq" {
		t.Fatalf("accepted project tools = %v", got)
	}
}

func TestChangingTheSharedListAsksAgain(t *testing.T) {
	// Accepting one list is not accepting whatever it becomes later.
	h := newHarness(t)
	h.writeProject(t, "tools:\n  - jq\n")
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcceptSettings([]trust.Ask{{Key: "tools", Value: "jq"}}); err != nil {
		t.Fatal(err)
	}

	h.writeProject(t, "tools:\n  - jq\n  - curl\n")
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := effectiveTools(cfg, store); len(got) != 0 {
		t.Fatalf("a widened list applied without a new acceptance: %v", got)
	}
	if len(store.PendingSettings(projectAsks(cfg))) != 1 {
		t.Error("the widened list did not ask again")
	}
}

func TestWriteProjectToolsPreservesTheRestOfTheFile(t *testing.T) {
	// It is the team's file and may hold anything.
	h := newHarness(t)
	h.writeProject(t, "network: none\n\nagents:\n  default:\n    allow_hosts:\n      - example.com\n")

	if err := writeProjectTools(h.paths.Project, []string{"jq"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(h.paths.Project)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{"network: none", "allow_hosts", "example.com", "- jq"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}

	// And it must still parse.
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatalf("rewritten file does not parse: %v", err)
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0] != "jq" {
		t.Errorf("tools = %v", cfg.Tools)
	}
}

func TestRewritingToolsTwiceDoesNotDuplicate(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, "network: none\n")
	if err := writeProjectTools(h.paths.Project, []string{"jq"}); err != nil {
		t.Fatal(err)
	}
	if err := writeProjectTools(h.paths.Project, []string{"jq", "curl"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools) != 2 {
		t.Fatalf("tools = %v, want the list replaced rather than appended", cfg.Tools)
	}
}

func TestPinsSurviveAndParse(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, "network: none\n\ntools:\n  - jq\n")

	if err := writeProjectPins(h.paths.Project, map[string]string{
		"golang:1.26-alpine": "golang@sha256:abc",
		"node:22":            "node@sha256:def",
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatalf("rewritten file does not parse: %v", err)
	}
	if len(cfg.Pins) != 2 || cfg.Pins["node:22"] != "node@sha256:def" {
		t.Fatalf("pins = %v", cfg.Pins)
	}
	// The rest of the team's file must survive.
	if cfg.Network != "none" || len(cfg.Tools) != 1 {
		t.Errorf("other config lost: network=%q tools=%v", cfg.Network, cfg.Tools)
	}
}

func TestRewritingPinsTwiceDoesNotDuplicate(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, "network: none\n")
	for i := 0; i < 3; i++ {
		if err := writeProjectPins(h.paths.Project, map[string]string{"a:1": "a@sha256:x"}); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(h.paths.Project)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), "pins:"); n != 1 {
		t.Fatalf("pins block appears %d times:\n%s", n, body)
	}
}

func TestPinningNeedsNoAcceptance(t *testing.T) {
	// A digest narrows what a build fetches rather than widening it, and
	// the project already chooses its own base images. Prompting here
	// would be a prompt that always deserves yes, which is how prompts
	// stop being read.
	h := newHarness(t)
	h.writeProject(t, "pins:\n  \"golang:1.26\": \"golang@sha256:abc\"\n")
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	if asks := projectAsks(cfg); len(asks) != 0 {
		t.Fatalf("pinning asked for consent: %+v", asks)
	}
}

func TestCompletionCoversEveryCommand(t *testing.T) {
	// v1 shipped completions for its whole command set; a completion that
	// knows half the commands teaches the wrong half.
	h := newHarness(t)
	root := NewRootCmd(h.env)

	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{
		"run", "shell", "build", "console", "status", "clean", "tools",
		"pin", "scan", "agent", "accept", "doctor", "migrate", "vm", "version",
		"completion",
	} {
		if !names[want] {
			t.Errorf("%q is not in the command tree, so it cannot be completed", want)
		}
	}
}

func TestCompletionInstallIsAvailable(t *testing.T) {
	// Printing a script is not installing it, which was the complaint.
	h := newHarness(t)
	root := NewRootCmd(h.env)
	c, _, err := root.Find([]string{"completion", "install"})
	if err != nil || c == nil || c.Name() != "install" {
		t.Fatalf("no `completion install` subcommand: %v", err)
	}
}

func TestCompletionTargets(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		got, err := targetFor(shell, "/home/u")
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		if !strings.HasPrefix(got.Path, "/home/u/") {
			t.Errorf("%s writes outside home: %s", shell, got.Path)
		}
		if got.Note == "" {
			t.Errorf("%s: no note saying what the user may still need to do", shell)
		}
	}
	if _, err := targetFor("csh", "/home/u"); err == nil {
		t.Error("unsupported shell accepted")
	}
}

func TestDetectShellUsesTheChosenShell(t *testing.T) {
	if got := detectShell([]string{"SHELL=/bin/zsh"}); got != "zsh" {
		t.Errorf("detectShell = %q", got)
	}
	if got := detectShell(nil); got != "." && got != "" {
		t.Logf("no SHELL set resolves to %q, which targetFor rejects", got)
	}
}

func TestImageCompletionIsShortAndNotFiles(t *testing.T) {
	// Every image on the daemon would be noise; the working directory's
	// files would be wrong.
	got, directive := completeImage(nil, nil, "")
	if len(got) == 0 || len(got) > 10 {
		t.Fatalf("offered %d images, want a short curated list", len(got))
	}
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Error("image completion offers files")
	}
	var hasPlainBase bool
	for _, c := range got {
		if strings.HasPrefix(string(c), "debian:") {
			hasPlainBase = true
		}
	}
	if !hasPlainBase {
		t.Error("no plain distribution offered, which is the sandboxing starting point")
	}
}
