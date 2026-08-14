package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/policy"
	"github.com/mwing/isolated-dev/internal/runner"
	"github.com/mwing/isolated-dev/internal/trust"
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
	// Stdin is left nil: no terminal, so egress prompting resolves to
	// reporting and nothing waits for an answer nobody is there to give.
	// Inheriting the test binary's stdin would tie the suite to how it was
	// invoked, which is not something a test should depend on.
	h.env = &Env{
		Stdout: h.stdout,
		Stderr: h.stderr,
		// The backend is named rather than left to the platform. These
		// tests fake orb's answers, so which driver they get must not
		// depend on which machine runs them — the same reason LookPath is
		// injected. Without it the suite passed on macOS and failed on
		// Linux the moment the default became platform-dependent.
		Env:    []string{"DEV_BACKEND=orbstack"},
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

// gitPassthrough runs git for real and fakes everything else.
//
// Some behaviour depends on what git actually says — whether a directory is
// a repository, what a clone contains — and a fake answers those with
// silence, which reads as "not a repository" and sends the code down a
// fallback path. Faking docker while letting git run is the combination
// those tests need.
type gitPassthrough struct {
	fake *runner.Fake
	real runner.Runner
}

func (g gitPassthrough) Run(ctx context.Context, cmd runner.Command) (runner.Result, error) {
	if cmd.Path == "git" {
		return g.real.Run(ctx, cmd)
	}
	return g.fake.Run(ctx, cmd)
}

// realGit makes git calls real for this harness, leaving docker faked.
func (h *harness) realGit() {
	h.env.Runner = gitPassthrough{fake: h.fake, real: runner.New(false)}
}

// writeGlobal writes the user's own config, which needs no consent from
// them: it is their machine and nobody else asked.
func (h *harness) writeGlobal(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(h.paths.Global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.paths.Global, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
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
// writeLanguage installs a language plugin into the temp home, so a test
// can make a project detect as something without depending on which plugins
// happen to be shipped.
func (h *harness) writeLanguage(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(h.paths.Languages, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "language.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile.template"),
		[]byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

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

// vmName is the VM the default configuration targets, and therefore what
// every rendered command line carries.
const vmName = "dev-vm-docker-host"

// dockerKey renders a Response key for a docker call the way the backend
// actually spawns it: the `orb -m <vm> sudo docker` wrapper included, and
// quoted exactly as the runner quotes it. Writing keys by hand meant
// guessing at that quoting, and a key that does not match falls through to
// Default — whose zero value is exit 0, which reads as success.
func dockerKey(args ...string) string {
	return runner.Command{
		Path: "orb",
		Args: append([]string{"-m", vmName, "sudo", "docker"}, args...),
	}.String()
}

var proxyInspect = dockerKey("image", "inspect", proxyImageTag)

// The sidecar and its networks are named after the project directory. The
// harness fixes that directory at <tmp>/project, and both `dev run` and
// `dev agent run` derive the same names from its base.
const (
	sidecarName     = "dev-project-proxy"
	internalNetwork = "dev-project-internal"
)

// readySidecar makes the fake runner look like a daemon that can bring the
// egress sidecar up: the image is present, the container reports itself
// listening, and it has an address on the internal network.
//
// Without this a run stops inside Sidecar.Start, which is below the glue
// layer these tests exist to cover.
func (h *harness) readySidecar() {
	h.fake.Response[proxyInspect] = runner.Result{Stdout: "[]\n"}
	h.fake.Response[dockerKey("logs", sidecarName)] = runner.Result{
		Stdout: netpolicy.ReadyLine + "\n",
	}
	h.fake.Response[dockerKey("inspect", "--format",
		fmt.Sprintf("{{ (index .NetworkSettings.Networks %q).IPAddress }}", internalNetwork),
		sidecarName)] = runner.Result{Stdout: "172.31.0.2\n"}
}

// writePolicy installs the machine policy. It is the one file the tool
// reads that answers to someone other than the person running the command,
// so a test that drives it has to write it where `loadPolicy` looks.
func (h *harness) writePolicy(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(h.paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy.DefaultPath(h.paths.Home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// acceptHosts records this user's acceptance of destinations the project
// requested, which is what `dev agent accept` writes.
func (h *harness) acceptHosts(t *testing.T, agentName string, hosts ...string) {
	t.Helper()
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept(agentName, hosts); err != nil {
		t.Fatal(err)
	}
}

// grantHosts records a user's own grant, which is what `dev allow` writes.
func (h *harness) grantHosts(t *testing.T, agentName string, hosts ...string) {
	t.Helper()
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(store.Project, agentName, hosts); err != nil {
		t.Fatal(err)
	}
}

// answer gives the prompt something to read. Stdin is a pipe rather than the
// test binary's own: whether it is a terminal decides whether prompting is
// possible at all, and a closed pipe makes an unanswered prompt resolve
// immediately instead of waiting.
func (h *harness) answer(t *testing.T, reply string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(reply); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	h.env.Stdin = r
}

// controlOps returns the [op, host] of every policy change pushed into a
// running sidecar through its control socket.
//
// Vectors, not rendered lines: these destinations differ by a port suffix,
// and "control allow example.com" is a substring of the line that grants
// example.com:8080 — so a substring assertion cannot tell the bug from the
// fix.
func (h *harness) controlOps() [][]string {
	var out [][]string
	for _, args := range h.dockerArgs() {
		for i, a := range args {
			if a == "control" && i+1 < len(args) {
				out = append(out, args[i+1:])
				break
			}
		}
	}
	return out
}

// dockerArgs returns the argument vector of every docker invocation, with
// the backend's wrapper stripped. Assertions work on the vector rather than
// on a rendered line: the argv is the behavior, and a rendered line has to
// be un-quoted before it can be compared.
func (h *harness) dockerArgs() [][]string {
	var out [][]string
	for _, c := range h.fake.Snapshot() {
		for i, a := range c.Args {
			if a == "docker" {
				out = append(out, c.Args[i+1:])
				break
			}
		}
	}
	return out
}

// dockerRuns returns the arguments of each `docker run`, without the
// leading "run".
func (h *harness) dockerRuns() [][]string {
	var out [][]string
	for _, args := range h.dockerArgs() {
		if len(args) > 0 && args[0] == "run" {
			out = append(out, args[1:])
		}
	}
	return out
}

// runsWithRole returns the runs carrying a given dev.role label. Every
// container the tool starts says what it is for, so a test can name the one
// it means rather than inferring it from the shape of the argv.
func (h *harness) runsWithRole(role string) [][]string {
	var out [][]string
	for _, args := range h.dockerRuns() {
		if containsPair(args, "--label", "dev.role="+role) {
			out = append(out, args)
		}
	}
	return out
}

// workloadRun returns the invocation that started the user's own container:
// their project, or the agent acting on it. The sidecar and the throwaway
// probes the tool runs for its own bookkeeping are excluded by role, and
// finding more or fewer than one is a change worth failing on rather than
// silently picking between.
func (h *harness) workloadRun(t *testing.T) []string {
	t.Helper()
	found := append(h.runsWithRole("workspace"), h.runsWithRole("agent")...)
	if len(found) != 1 {
		t.Fatalf("want exactly one workload `docker run`, got %d:\n%s",
			len(found), strings.Join(h.fake.Lines(), "\n"))
	}
	return found[0]
}

// sidecarAllow returns the allowlist the sidecar was started with. This is
// the value every egress promise rests on, and it is the one thing the spec
// builders cannot be asked about: it is assembled in the glue layer.
func (h *harness) sidecarAllow(t *testing.T) []string {
	t.Helper()
	for _, args := range h.runsWithRole("egress-sidecar") {
		for i, a := range args {
			if a == "--allow" && i+1 < len(args) {
				if args[i+1] == "" {
					return nil
				}
				return strings.Split(args[i+1], ",")
			}
		}
	}
	t.Fatalf("no sidecar was started with an allowlist:\n%s",
		strings.Join(h.fake.Lines(), "\n"))
	return nil
}

func contains(hay []string, needle string) bool {
	for _, v := range hay {
		if v == needle {
			return true
		}
	}
	return false
}

// argv renders an argument vector for a failure message.
func argv(args []string) string { return strings.Join(args, " ") }

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
	for _, want := range []string{"dev ", "go:", "platform:"} {
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
	h.writeProject(t, `mount_git_config: true
pass_env_vars:
  patterns:
    - AWS_*
`)
	_ = h.run(t, "doctor")

	out := h.stdout.String()
	if !strings.Contains(out, "grants requested by config") {
		t.Fatalf("grants not surfaced:\n%s", out)
	}
	if !strings.Contains(out, "~/.gitconfig") || !strings.Contains(out, "AWS_*") {
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
	if !strings.Contains(out, "✓  image dev-proxy:latest") {
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
		Stderr:   "Error: No such image: dev-proxy:latest\n",
	}

	err := h.run(t, "doctor")
	if err == nil {
		t.Fatal("a missing sidecar image blocks every agent run; doctor should exit non-zero")
	}
	out := h.stdout.String()
	if !strings.Contains(out, "✗  image dev-proxy:latest") {
		t.Errorf("missing image not reported:\n%s", out)
	}
	if !strings.Contains(out, "built automatically") {
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
		// doctor diagnoses; the image is built by the next run that needs it.
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
	asks := projectAsks(cfg, nil)
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
	if asks := projectAsks(cfg, nil); len(asks) != 0 {
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
	if asks := projectAsks(cfg, nil); len(asks) != 0 {
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
	if !strings.Contains(h.stderr.String(), "dev accept") {
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
	// The prefix says what you are doing; `dev add` alone did not.
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
			t.Errorf("dev tools %s missing", name)
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
	asks := projectAsks(cfg, nil)
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
	if len(store.PendingSettings(projectAsks(cfg, nil))) != 1 {
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
	if asks := projectAsks(cfg, nil); len(asks) != 0 {
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
		"completion", "allow", "revoke", "grants", "config",
	} {
		if !names[want] {
			t.Errorf("%q is not in the command tree, so it cannot be completed", want)
		}
	}
}

func TestGrantCommandsLiveAtTheRoot(t *testing.T) {
	// They apply to plain runs too: a blocked `dev run` prints the hint, and
	// a plain run consumes the grants. Under `dev agent` they named an owner
	// they do not have.
	h := newHarness(t)
	root := NewRootCmd(h.env)

	for _, name := range []string{"allow", "revoke", "grants", "config"} {
		c, _, err := root.Find([]string{name})
		if err != nil || c == nil || c.Name() != name {
			t.Fatalf("`dev %s` is not in the tree: %v", name, err)
		}
		if c.Hidden {
			t.Errorf("`dev %s` is hidden, so nothing will lead anyone to it", name)
		}
	}
	// The verbs that really are about agents stay where they are.
	for _, path := range [][]string{{"agent", "run"}, {"agent", "list"},
		{"agent", "logout"}, {"agent", "policy"}} {
		c, _, err := root.Find(path)
		if err != nil || c == nil || c.Name() != path[1] {
			t.Errorf("`dev %s` moved or vanished: %v", strings.Join(path, " "), err)
		}
	}
	// One `dev accept` for everything the project's file requests. Settings
	// and destinations are still separate decisions with separate records;
	// what was merged is the review, because the split was never the user's
	// — they cloned one repository and it asked for both.
	if c, _, err := root.Find([]string{"accept"}); err != nil || c == nil || c.Hidden {
		t.Errorf("`dev accept` is gone or hidden: %v", err)
	}
	if c, _, err := root.Find([]string{"agent", "accept"}); err != nil || c == nil || !c.Hidden {
		t.Errorf("`dev agent accept` should still work and stay out of the help: %v", err)
	}
}

func TestDevNullIsNotATerminal(t *testing.T) {
	// It is a character device, which is what the old check tested for, so
	// it was reported as a terminal. That is the single most common way of
	// running without one: it made `--tty auto` ask docker for a terminal it
	// could not attach, and it made bare `dev` open a full-screen menu with
	// nobody there to read it.
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("/dev/null reports as a terminal")
	}
	if wantTTY("auto", f) {
		t.Error("`--tty auto` would ask for a terminal with stdin on /dev/null")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Error("a pipe reports as a terminal")
	}
	if isTerminal(nil) {
		t.Error("no stdin at all reports as a terminal")
	}
}

func TestBareDevWithoutATerminalPrintsHelp(t *testing.T) {
	// The guided view is a full-screen program. Bare `dev` in a script or
	// piped into a pager has to print something readable instead of failing,
	// and it must not hang waiting for input nobody will send.
	h := newHarness(t)
	h.writeProject(t, "network: allowlist\n")

	if err := h.run(t); err != nil {
		t.Fatalf("bare dev: %v\n%s", err, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Usage:") {
		t.Errorf("bare dev printed no help:\n%s", h.stdout.String())
	}
}

func TestBareDevOutsideAProjectSaysSo(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t); err != nil {
		t.Fatalf("bare dev: %v", err)
	}
	if !strings.Contains(h.stdout.String(), "Usage:") {
		t.Errorf("no help was printed:\n%s", h.stdout.String())
	}
}

func TestAMistypedCommandIsNotTheGuidedView(t *testing.T) {
	// Giving the root a RunE means cobra will hand it an unrecognized
	// command as an argument unless it is told not to accept any, so `dev
	// buld` would silently open the menu instead of saying what is wrong.
	h := newHarness(t)

	err := h.run(t, "buld")
	if err == nil {
		t.Fatal("a mistyped command was accepted")
	}
	if !strings.Contains(err.Error(), "buld") {
		t.Errorf("the error does not name what was typed: %v", err)
	}
}

func TestTheOldGrantPathsStillWorkAndAreHidden(t *testing.T) {
	// Nothing should break mid-transition: these spellings are in people's
	// shell history and in scripts. They stay out of the help so no one
	// learns them now.
	h := newHarness(t)
	root := NewRootCmd(h.env)

	for _, name := range []string{"allow", "revoke", "grants", "config"} {
		c, _, err := root.Find([]string{"agent", name})
		if err != nil || c == nil || c.Name() != name {
			t.Fatalf("`dev agent %s` stopped working: %v", name, err)
		}
		if !c.Hidden {
			t.Errorf("`dev agent %s` is still advertised as a way to do this", name)
		}
	}

	// And the alias does the same work, saying once which name to use now.
	if err := h.run(t, "agent", "allow", "example.com"); err != nil {
		t.Fatalf("agent allow: %v\n%s", err, h.stderr.String())
	}
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Resolve("default").AllowHosts; !contains(got, "example.com") {
		t.Errorf("the alias recorded nothing: %v", got)
	}
	if !strings.Contains(h.stderr.String(), "`dev allow`") {
		t.Errorf("the alias does not say what to type instead:\n%s", h.stderr.String())
	}
	// On stderr, because `dev agent config path` is read by scripts.
	if strings.Contains(h.stdout.String(), "is now") {
		t.Errorf("the note went to stdout, where it corrupts output:\n%s", h.stdout.String())
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

func TestUpdateAndPinAreBothPresent(t *testing.T) {
	// They are the same trade from opposite ends: one fixes what a build
	// fetches, the other moves it deliberately and says what moved.
	h := newHarness(t)
	root := NewRootCmd(h.env)
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"pin", "update", "scan", "policy"} {
		if !names[want] {
			t.Errorf("%q missing from the command tree", want)
		}
	}
}

// acceptSettings records consent to project settings, which is what
// `dev accept` writes.
func (h *harness) acceptSettings(t *testing.T, asks ...trust.Ask) {
	t.Helper()
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcceptSettings(asks); err != nil {
		t.Fatal(err)
	}
}

func TestSSHHostOfEveryRemoteSpelling(t *testing.T) {
	// The two spellings git actually uses, and the ones an ssh-agent cannot
	// push over. A wrong answer here either opens a host the project does
	// not use or blocks the one it does.
	for _, tc := range []struct {
		url  string
		want string
	}{
		{"git@github.com:org/repo.git", "github.com:22"},
		{"git@git.example.com:team/repo", "git.example.com:22"},
		{"ssh://git@git.example.com/team/repo.git", "git.example.com:22"},
		{"ssh://git@git.example.com:2222/team/repo.git", "git.example.com:2222"},
		{"https://github.com/org/repo.git", ""},
		{"http://git.example.com/repo", ""},
		{"git://git.example.com/repo", ""},
		{"/srv/git/repo.git", ""},
		{"", ""},
	} {
		got, ok := sshHostOf(tc.url)
		if tc.want == "" {
			if ok {
				t.Errorf("%q was taken for an ssh remote (%q)", tc.url, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("sshHostOf(%q) = %q, %v; want %q", tc.url, got, ok, tc.want)
		}
	}
}

// A command added without a group lands under cobra's "Additional
// Commands", which is where things go to be missed. The grouping is only
// worth having if it stays complete, and that is not something anyone
// remembers when adding the thirtieth command.
func TestEveryVisibleCommandIsInAGroup(t *testing.T) {
	h := newHarness(t)
	root := NewRootCmd(h.env)

	groups := map[string]bool{}
	for _, g := range root.Groups() {
		groups[g.ID] = true
	}
	if len(groups) == 0 {
		t.Fatal("the root help has no groups at all")
	}

	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		if c.GroupID == "" {
			t.Errorf("`dev %s` is in no group, so it lands under Additional Commands", c.Name())
			continue
		}
		if !groups[c.GroupID] {
			t.Errorf("`dev %s` names group %q, which does not exist", c.Name(), c.GroupID)
		}
	}
}

// The help groups and docs/COMMANDS.md use the same words on purpose:
// someone moving between them should not have to learn the vocabulary
// twice.
func TestHelpGroupsMatchTheReferenceHeadings(t *testing.T) {
	h := newHarness(t)
	root := NewRootCmd(h.env)

	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "COMMANDS.md"))
	if err != nil {
		t.Skipf("no reference to compare against: %v", err)
	}
	for _, g := range root.Groups() {
		if !strings.Contains(string(doc), "## "+g.Title) {
			t.Errorf("help group %q has no matching heading in COMMANDS.md", g.Title)
		}
	}
}

// Two backends, two sentences for the same failure. Only orb's was
// recognized, so the docker backend passed docker's own wording through
// with no remedy attached.
func TestBothBackendsTTYFailuresAreExplained(t *testing.T) {
	for _, out := range []string{
		"the input device is not a TTY",
		"cannot attach stdin to a TTY-enabled container because stdin is not a terminal",
	} {
		hint := explainTTYFailure(out)
		if hint == "" {
			t.Errorf("no remedy offered for %q", out)
			continue
		}
		if !strings.Contains(hint, "--tty off") {
			t.Errorf("the remedy does not name the flag: %q", hint)
		}
	}
	if explainTTYFailure("some unrelated docker error") != "" {
		t.Error("an unrelated failure was explained as a TTY problem")
	}
}
