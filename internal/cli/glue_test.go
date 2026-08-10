package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
