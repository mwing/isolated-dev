package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/policy"
	"github.com/mwing/isolated-dev/internal/project"
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

// --- Host access: the grants that used to authorize nothing (T4) ---

// statSocket answers the throwaway container that reads the group owning a
// mounted socket. Its spec is the only `docker run` with no --interactive.
func (h *harness) statSocket(gid string) {
	h.fake.Response[dockerKey("run", "--rm", "--network", "none")] =
		runner.Result{Stdout: gid + "\n"}
}

// writeHostGitConfig writes the user's own gitconfig, which lives next to
// ~/.dev-envs in the harness's fake home.
func (h *harness) writeHostGitConfig(t *testing.T, body string) {
	t.Helper()
	home := filepath.Dir(h.paths.Home)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAProjectMountIsRefusedUntilAccepted(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeProject(t, "mount_git_config: true\n")

	err := h.run(t, "run", "--tty", "off", "-c", "true")
	if err == nil {
		t.Fatal("a project's mount request applied itself")
	}
	if !strings.Contains(h.stderr.String(), "dev accept") {
		t.Errorf("the refusal does not name the remedy:\n%s", h.stderr.String())
	}
}

func TestAnAcceptedMountReachesTheContainer(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeProject(t, "mount_git_config: true\n")
	h.writeHostGitConfig(t, "[user]\n\tname = Real Person\n")
	h.acceptSettings(t, trust.Ask{Key: "mount_git_config", Value: "true"})

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, h.stderr.String())
	}
	args := h.workloadRun(t)
	if !mountedAt(args, project.SystemGitConfig) {
		t.Fatalf("the accepted gitconfig never reached the container:\n%s", argv(args))
	}
}

func TestTheGrantedGitConfigIsFiltered(t *testing.T) {
	// A gitconfig is not identity. It can name a signing key the container
	// cannot use, a credential helper that hands out tokens, and insteadOf
	// rules that rewrite a remote — which would route a push past the
	// allowlist by changing the destination rather than reaching it.
	h := newHarness(t)
	h.writeHostGitConfig(t, `[user]
	name = Real Person
	email = real@example.com
	signingkey = ABCD1234
[commit]
	gpgsign = true
[credential]
	helper = osxkeychain
[url "git@evil.example:"]
	insteadOf = https://github.com/
`)

	path, err := filterGitConfig(h.env)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{"Real Person", "real@example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("identity was lost: %q missing from\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"signingkey", "gpgsign", "helper", "insteadOf", "evil.example"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("%q survived filtering:\n%s", unwanted, got)
		}
	}
}

func TestAnAcceptedDockerSocketArrivesUsable(t *testing.T) {
	// The mount alone is not the grant: the socket is owned by a group the
	// fixed uid is not in, so without --group-add it is present and
	// unusable — the same half-honored grant in a new shape.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.statSocket("999")
	h.writeProject(t, "mount_docker_socket: true\n")
	h.acceptSettings(t, trust.Ask{Key: "mount_docker_socket", Value: "true"})

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, h.stderr.String())
	}
	args := h.workloadRun(t)
	if !mountedAt(args, project.DockerSocketPath) {
		t.Errorf("the socket was not mounted:\n%s", argv(args))
	}
	if !containsPair(args, "--group-add", "999") {
		t.Errorf("the socket's group was not added, so the mount is unusable:\n%s", argv(args))
	}
	if !strings.Contains(h.stderr.String(), "root on the docker host") {
		t.Errorf("the worst grant in the tool was applied quietly:\n%s", h.stderr.String())
	}
}

func TestAcceptedEnvPassthroughReachesTheContainer(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.env.Env = []string{"AWS_PROFILE=dev", "SECRET_TOKEN=hunter2"}
	h.writeProject(t, "pass_env_vars:\n  patterns:\n    - AWS_*\n")
	h.acceptSettings(t, trust.Ask{Key: "pass_env_vars", Value: "AWS_*"})

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, h.stderr.String())
	}
	args := h.workloadRun(t)
	if !containsPair(args, "--env", "AWS_PROFILE=dev") {
		t.Errorf("the accepted variable never arrived:\n%s", argv(args))
	}
	if containsPair(args, "--env", "SECRET_TOKEN=hunter2") {
		t.Errorf("a variable nobody asked for was copied in:\n%s", argv(args))
	}
}

func TestAGrantedVariableCannotTurnOffEgressFiltering(t *testing.T) {
	// A project could ask to pass HTTP_PROXY. docker takes the last --env
	// for a name, so the sidecar's setting has to be the one that lands.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.env.Env = []string{"HTTP_PROXY=http://attacker.example"}
	h.writeProject(t, "pass_env_vars:\n  explicit:\n    - HTTP_PROXY\n")
	h.acceptSettings(t, trust.Ask{Key: "pass_env_vars", Value: "HTTP_PROXY"})

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, h.stderr.String())
	}
	args := h.workloadRun(t)
	var last string
	for i, a := range args {
		if a == "--env" && i+1 < len(args) && strings.HasPrefix(args[i+1], "HTTP_PROXY=") {
			last = args[i+1]
		}
	}
	if last != "HTTP_PROXY=http://172.31.0.2:3128" {
		t.Fatalf("effective HTTP_PROXY = %q, want the sidecar's", last)
	}
}

func TestTheUsersOwnGlobalConfigNeedsNoAcceptance(t *testing.T) {
	// The global file is the user's own machine and nobody else asked. A
	// prompt there would be the tool asking permission of the person who
	// already gave it.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeHostGitConfig(t, "[user]\n\tname = Real Person\n")
	if err := os.MkdirAll(filepath.Dir(h.paths.Global), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.paths.Global, []byte("mount_git_config: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, h.stderr.String())
	}
	if !mountedAt(h.workloadRun(t), project.SystemGitConfig) {
		t.Error("the user's own setting was ignored")
	}
}

// mountedAt reports whether a rendered invocation mounts anything at target.
func mountedAt(args []string, target string) bool {
	for i, a := range args {
		if a == "--mount" && i+1 < len(args) &&
			strings.Contains(args[i+1], "target="+target) {
			return true
		}
	}
	return false
}

func containsPair(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

// --- Machine policy: the routes it did not cover (T3) ---

// denyEvil is the smallest policy that matters here. `internal/policy`
// documents itself as enforced at every route in; before this it was
// reachable from `dev agent allow` and nowhere else.
const denyEvil = "deny_hosts: [evil.example]\n"

// requestEvil is a project asking for the destination the policy denies —
// the shape `dev agent accept` walks the user through.
const requestEvil = `agents:
  default:
    allow_hosts:
      - evil.example
`

func TestPolicyDeniesAHostOnEveryRouteIn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project string
		args    []string
	}{
		{"dev agent allow", "", []string{"agent", "allow", "evil.example"}},
		{"a plain run's --allow-host", "",
			[]string{"run", "--tty", "off", "--allow-host", "evil.example", "-c", "true"}},
		{"an agent run's --allow-host", "",
			[]string{"agent", "run", "claude", "--tty", "off", "--allow-host", "evil.example"}},
		{"the policy an agent run would enforce", "",
			[]string{"agent", "policy", "claude", "--allow-host", "evil.example"}},
		{"dev console's --allow-host", "",
			[]string{"console", "--allow-host", "evil.example", "-c", "true"}},
		{"dev agent accept", requestEvil, []string{"agent", "accept", "--all"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.readyBackend()
			h.readySidecar()
			h.writePolicy(t, denyEvil)
			if tc.project != "" {
				h.writeProject(t, tc.project)
			}

			err := h.run(t, tc.args...)
			if err == nil {
				t.Fatalf("the denied destination got in:\n%s", strings.Join(h.fake.Lines(), "\n"))
			}
			if !strings.Contains(err.Error(), "evil.example") {
				t.Errorf("the refusal does not name the destination: %v", err)
			}
			for _, allow := range h.startedAllowlists() {
				if contains(allow, "evil.example") {
					t.Errorf("a sidecar was started with it anyway: %v", allow)
				}
			}
		})
	}
}

// startedAllowlists returns the allowlist of every sidecar that was
// started, or nothing when none was. Unlike sidecarAllow it does not fail
// the test on finding none: a refusal is expected to start nothing at all.
func (h *harness) startedAllowlists() [][]string {
	var out [][]string
	for _, args := range h.runsWithRole("egress-sidecar") {
		for i, a := range args {
			if a == "--allow" && i+1 < len(args) {
				out = append(out, strings.Split(args[i+1], ","))
			}
		}
	}
	return out
}

func TestAForbiddenSettingCannotBeAccepted(t *testing.T) {
	// "A project requesting something forbidden is refused rather than
	// offered for acceptance" — USE-CASES said so while `dev accept`
	// recorded it happily and left the refusal to the next run.
	h := newHarness(t)
	h.writeProject(t, "mount_docker_socket: true\n")
	h.writePolicy(t, "forbid: [mount_docker_socket]\n")

	if err := h.run(t, "accept", "--all"); err == nil {
		t.Fatal("a forbidden setting was accepted")
	}
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := resolveProject(h.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.PendingSettings(projectAsks(cfg))) == 0 {
		t.Fatal("the acceptance was recorded anyway")
	}

	// And it is named rather than silently dropped from the listing.
	h.stdout.Reset()
	if err := h.run(t, "accept"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout.String(), "cannot be accepted") {
		t.Errorf("the listing still offers it:\n%s", h.stdout.String())
	}
}

func TestAGrantOlderThanTheRuleDoesNotReachTheSidecar(t *testing.T) {
	// Policy outranks a decision already recorded — the same reasoning
	// enforceConsent applies to accepted settings. A rule that bound only
	// new decisions would leave the machines that most need it untouched,
	// and the routes above would be theatre: grant first, write the rule
	// later, keep the destination.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.grantHosts(t, "default", "evil.example")
	h.writePolicy(t, denyEvil)

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v\n%s", err, h.stderr.String())
	}
	if allow := h.sidecarAllow(t); contains(allow, "evil.example") {
		t.Fatalf("a grant recorded before the rule outlived it: %v", allow)
	}
	if !strings.Contains(h.stderr.String(), "evil.example") {
		t.Errorf("the destination was dropped silently:\n%s", h.stderr.String())
	}
}

func TestAnAgentRunDoesNotReportADroppedGrantAsGranted(t *testing.T) {
	// The run tells the user what it permits. Printing "granted:
	// evil.example" one line under "dropped from this run's allowlist" is
	// the report and the behavior disagreeing — a smaller version of
	// exactly the problem this queue exists to close.
	h := newHarness(t)
	h.readyBackend()
	h.grantHosts(t, "claude", "evil.example")
	h.writePolicy(t, denyEvil)

	if err := h.run(t, "agent", "run", "claude", "--dry-run"); err != nil {
		t.Fatalf("agent run: %v", err)
	}
	if strings.Contains(h.stdout.String(), "granted: evil.example") {
		t.Errorf("a dropped destination was reported as granted:\n%s", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), "dropped from this run's allowlist") {
		t.Errorf("the drop was not reported at all:\n%s", h.stderr.String())
	}
	if strings.Contains(h.stdout.String(), "evil.example") {
		t.Errorf("it survived into the printed allowlist:\n%s", h.stdout.String())
	}
}

func TestAnAgentRunHonorsTheNetworkModesPolicy(t *testing.T) {
	// An agent run is always an allowlist run, so a machine permitting only
	// `none` permits no agent at all. Before this the policy was not loaded
	// on this path at all: agent runs, the runs with the most reason to be
	// constrained, were the ones it did not reach.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writePolicy(t, "network_modes: [none]\n")

	err := h.run(t, "agent", "run", "claude", "--tty", "off")
	if err == nil {
		t.Fatal("the agent ran on a machine that permits no network mode it uses")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("the refusal does not say which mode was refused: %v", err)
	}
}

func TestAnAgentRunHonorsForbid(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeProject(t, "mount_git_config: true\n")
	h.acceptSettings(t, trust.Ask{Key: "mount_git_config", Value: "true"})
	h.writePolicy(t, "forbid: [mount_git_config]\n")

	err := h.run(t, "agent", "run", "claude", "--tty", "off")
	if err == nil {
		t.Fatal("an agent run honored a setting the machine forbids")
	}
	if !strings.Contains(err.Error(), "mount_git_config") {
		t.Errorf("the refusal does not name the setting: %v", err)
	}
}

func TestAnAgentRunHonorsRequiredLimits(t *testing.T) {
	// Applied last, so nothing lower can relax them: the project asks for
	// 16g and the machine's ceiling is what lands.
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeProject(t, "agents:\n  default:\n    memory: 16g\n")
	h.writePolicy(t, "require:\n  memory: 2g\n")

	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, h.stderr.String())
	}
	args := h.workloadRun(t)
	if !containsPair(args, "--memory", "2g") {
		t.Errorf("the required limit did not reach the container:\n%s", argv(args))
	}
	if containsPair(args, "--memory", "16g") {
		t.Errorf("the project's own limit outranked the machine's:\n%s", argv(args))
	}
}

func TestTheInteractiveGrantRefusesADeniedHost(t *testing.T) {
	// The prompt is the one place a user widens policy while a run is in
	// flight, and it persists what it grants. Asking a question whose only
	// permitted answer is "no" would be worse than not asking: the honest
	// move is to refuse it at the sidecar so the held request fails now
	// rather than waiting out its timeout.
	h := newHarness(t)
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	// Answer "this project, from now on" — the route that records a grant.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("p\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	h.env.Stdin = r

	pol, err := policy.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pol.DenyHosts = []string{"evil.example"}

	side := &netpolicy.Sidecar{
		Engine:   container.New(h.env.driver(vmName)),
		Topology: netpolicy.Topology{SidecarName: sidecarName},
	}
	p := newPrompter(h.env, side, store, h.paths.ProjectDir, pol)
	p.Handle(context.Background(), netpolicy.Event{
		Action: "pending", Host: "evil.example", Port: 443,
	})

	if got := store.Resolve("default").AllowHosts; contains(got, "evil.example") {
		t.Fatalf("the prompt recorded a grant the policy denies: %v", got)
	}
	if !strings.Contains(h.stderr.String(), "policy forbids") {
		t.Errorf("the user was not told why:\n%s", h.stderr.String())
	}
	var refused bool
	for _, line := range h.fake.Lines() {
		if strings.Contains(line, "control refuse evil.example") {
			refused = true
		}
		if strings.Contains(line, "control allow evil.example") {
			t.Errorf("the running sidecar was told to allow it: %s", line)
		}
	}
	if !refused {
		t.Errorf("the held request was left to time out, calls:\n%s",
			strings.Join(h.fake.Lines(), "\n"))
	}
}

// An agent asked to work on a project needs that project's package index.
// Found by running an agent on this repository: it could not fetch a Go
// module, because proxy.golang.org serves large zips as a redirect to
// storage.googleapis.com and the agent path inherited none of the project's
// registries. `dev run` had them all along.
func TestAgentRunGetsTheProjectsRegistries(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	// A language plugin declaring the module proxy and the bucket its zips
	// are redirected to, and a go.mod so the project detects as that
	// language. The fixture is deliberately minimal: the point is that
	// whatever a plugin declares reaches an agent run, not what the shipped
	// golang plugin happens to say today.
	h.writeLanguage(t, "golang", `name: golang
versions: ["1.26"]
detection:
  files: [go.mod]
registries:
  - proxy.golang.org
  - storage.googleapis.com
`)
	if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, "go.mod"),
		[]byte("module example.com/x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v", err)
	}

	allow := h.sidecarAllow(t)
	for _, want := range []string{"proxy.golang.org", "storage.googleapis.com"} {
		if !contains(allow, want) {
			t.Fatalf("agent allowlist missing %q: %s", want, argv(allow))
		}
	}
}
