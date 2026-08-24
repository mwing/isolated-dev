package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/backend/orbstack"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/runner"
)

func TestBuiltinsAreValid(t *testing.T) {
	for _, a := range NewRegistry().List() {
		if err := a.Validate(); err != nil {
			t.Errorf("built-in %s: %v", a.Name, err)
		}
	}
}

func TestBuiltinsAllowTheirOwnAPI(t *testing.T) {
	// An agent that cannot reach its own API fails in a way that looks
	// like a bug rather than a policy decision.
	want := map[string]string{
		"claude": "api.anthropic.com",
		"codex":  "api.openai.com",
	}
	r := NewRegistry()
	for name, host := range want {
		a, err := r.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		allow, err := netpolicy.Parse(a.AllowHosts)
		if err != nil {
			t.Fatalf("%s allowlist: %v", name, err)
		}
		if !allow.Allows(host, 443) {
			t.Errorf("%s cannot reach %s", name, host)
		}
	}
}

func TestBuiltinAllowlistsAreTight(t *testing.T) {
	// Every entry must parse and none may be a bare wildcard or a
	// well-known catch-all.
	for _, a := range NewRegistry().List() {
		allow, err := netpolicy.Parse(a.AllowHosts)
		if err != nil {
			t.Fatalf("%s: %v", a.Name, err)
		}
		for _, h := range []string{"evil.example.com", "pastebin.com", "1.2.3.4"} {
			if allow.Allows(h, 443) {
				t.Errorf("%s allows %s", a.Name, h)
			}
		}
	}
}

func TestValidateCatchesBadDefinitions(t *testing.T) {
	cases := map[string]Agent{
		"no name":       {Binary: "x", ConfigDir: "/c", AllowHosts: []string{"a.com"}},
		"no binary":     {Name: "a", ConfigDir: "/c", AllowHosts: []string{"a.com"}},
		"no config dir": {Name: "a", Binary: "x", AllowHosts: []string{"a.com"}},
		"relative dir":  {Name: "a", Binary: "x", ConfigDir: "rel", AllowHosts: []string{"a.com"}},
		"no allowlist":  {Name: "a", Binary: "x", ConfigDir: "/c"},
		"spacey name":   {Name: "a b", Binary: "x", ConfigDir: "/c", AllowHosts: []string{"a.com"}},
	}
	for name, a := range cases {
		if err := a.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestPinnedReportsFloatingVersions(t *testing.T) {
	if (&Agent{Version: "latest"}).Pinned() {
		t.Error("latest must not count as pinned")
	}
	if (&Agent{Version: ""}).Pinned() {
		t.Error("empty version must not count as pinned")
	}
	if !(&Agent{Version: "1.2.3"}).Pinned() {
		t.Error("explicit version should be pinned")
	}
}

func TestLoadDirOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `name: claude
description: pinned locally
version: 1.0.99
binary: claude
config_dir: /home/dev/.claude
base: node:22-bookworm-slim
allow_hosts:
  - api.anthropic.com
`
	if err := os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	if err := r.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	a, err := r.Get("claude")
	if err != nil {
		t.Fatal(err)
	}
	if a.Version != "1.0.99" {
		t.Errorf("version = %q, want the local override", a.Version)
	}
	if !a.Pinned() {
		t.Error("override should be pinned")
	}
	if a.Source() == "built-in" {
		t.Error("source should point at the file")
	}
}

func TestLoadDirMissingIsNotAnError(t *testing.T) {
	r := NewRegistry()
	if err := r.LoadDir(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatalf("LoadDir on a missing dir: %v", err)
	}
}

func TestLoadDirRejectsInvalidDefinition(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	// No allow_hosts: would silently fail to reach anything.
	body := "name: broken\nbinary: x\nconfig_dir: /c\n"
	if err := os.WriteFile(filepath.Join(bad, "agent.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewRegistry().LoadDir(dir); err == nil {
		t.Fatal("expected an error for an invalid definition")
	}
}

func TestVolumeIsPerAgentNotPerProject(t *testing.T) {
	// One login should serve every project; scoping per project would ask
	// the user to authenticate again in each repo.
	a := &Agent{Name: "claude"}
	if got := a.VolumeName(); got != "dev-agent-claude-config" {
		t.Errorf("VolumeName = %q", got)
	}
	// And it is not the old whole-home volume under a new name: that one is
	// read once to carry a login forward and then left alone.
	if a.VolumeName() == homeVolumeName(a) {
		t.Error("the config volume reuses the home volume's name")
	}
}

// The review's finding: the volume was the whole home directory and scoped
// per agent, so an agent working in project A could leave a shell profile,
// a git config or an MCP setting behind for an agent in project B to read —
// two projects with no route to each other on the network and none intended
// here either. Only the config directory persists now, because only the
// credential has to.
func TestOnlyTheConfigDirectoryPersistsBetweenRuns(t *testing.T) {
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude",
		Base: "node:22", AllowHosts: []string{"api.anthropic.com"},
	}
	spec := Spec(Options{Agent: a, Project: "/host/proj"}, netpolicy.Topology{SidecarIP: "10.0.0.2"})

	for _, m := range spec.Mounts {
		if m.Volume && m.Target == HomePath {
			t.Errorf("the whole home directory still persists: %+v", m)
		}
	}
	var found bool
	for _, m := range spec.Mounts {
		if m.Volume && m.Target == a.ConfigDir {
			found = true
		}
	}
	if !found {
		t.Errorf("the config directory does not persist, so a login will not: %+v", spec.Mounts)
	}
}

// An agent that writes state beside its config directory rather than inside
// it would ask to log in again every run, now that the home is not kept. So
// it is told where the directory it keeps is.
func TestTheAgentIsToldWhereItsConfigDirectoryIs(t *testing.T) {
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude",
		ConfigEnv: "CLAUDE_CONFIG_DIR", Base: "node:22",
		AllowHosts: []string{"api.anthropic.com"},
	}
	spec := Spec(Options{Agent: a, Project: "/host/proj"}, netpolicy.Topology{SidecarIP: "10.0.0.2"})
	var found bool
	for _, e := range spec.Env {
		if e == "CLAUDE_CONFIG_DIR=/home/dev/.claude" {
			found = true
		}
	}
	if !found {
		t.Errorf("config_env was not passed: %v", spec.Env)
	}
}

// The mount point has to exist in the image, owned by the account the run
// uses. A path docker creates for a mount is created owned by root, and an
// agent that cannot write its config directory completes the OAuth exchange,
// says it logged in, and has nowhere to put the credential.
func TestTheOverlayCreatesTheConfigDirectory(t *testing.T) {
	a := &Agent{Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude"}
	df := Dockerfile(a, "debian:bookworm-slim")
	if !strings.Contains(df, "mkdir -p /home/dev/.claude") {
		t.Errorf("the overlay does not create the config directory:\n%s", df)
	}
	if !strings.Contains(df, `chown -R "$DEV_UID":"$DEV_GID" /home/dev/.claude`) {
		t.Errorf("the config directory is not owned by the run's account:\n%s", df)
	}
}

func TestImageTagVariesWithBaseAndVersion(t *testing.T) {
	a := &Agent{Name: "claude", Version: "1.2.3"}
	one := a.ImageTag("node:22-bookworm-slim")
	two := a.ImageTag("python:3.13")
	if one == two {
		t.Fatal("different bases must not share an overlay tag")
	}
	if !strings.Contains(one, "1.2.3") {
		t.Errorf("tag should carry the version: %q", one)
	}
	if strings.ContainsAny(one, "/") {
		t.Errorf("tag contains a slash: %q", one)
	}
}

func TestDockerfileDoesNotTouchProjectImageAndEndsUnprivileged(t *testing.T) {
	a := &Agent{Name: "claude", Install: "npm i -g x", ConfigDir: "/home/dev/.claude"}
	df := Dockerfile(a, "node:22-bookworm-slim")

	if !strings.HasPrefix(df, "FROM node:22-bookworm-slim\n") {
		t.Errorf("overlay must build FROM the base:\n%s", df)
	}
	lines := strings.Split(strings.TrimSpace(df), "\n")
	var lastUser string
	for _, l := range lines {
		if strings.HasPrefix(l, "USER ") {
			lastUser = l
		}
	}
	if lastUser != "USER $DEV_UID:$DEV_GID" {
		t.Errorf("overlay must end unprivileged, got %q", lastUser)
	}
	if !strings.Contains(df, "npm i -g x") {
		t.Error("install step missing")
	}
}

func TestSpecPinsAgentToUntrustedPosture(t *testing.T) {
	a := &Agent{
		Name: "claude", Binary: "claude", Args: []string{"--yolo"},
		ConfigDir: "/home/dev/.claude", Base: "node:22",
		AllowHosts: []string{"api.anthropic.com"},
	}
	topo := netpolicy.Topology{
		InternalNetwork: "proj-int", SidecarIP: "10.1.2.3", ProxyPort: 3128,
	}
	spec := Spec(Options{Agent: a, Project: "/host/proj"}, topo)

	args := strings.Join(spec.Args(), " ")
	for _, want := range []string{
		"--user " + container.HostUser(),
		"--cap-drop ALL",
		"--security-opt no-new-privileges:true",
		"--network proj-int",
		"--dns 10.1.2.3",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in:\n%s", want, args)
		}
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSpecPassesNoHostCredentialsByDefault(t *testing.T) {
	// The M1 exit criterion: credentials absent unless explicitly granted.
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude",
		Base: "node:22", AllowHosts: []string{"api.anthropic.com"},
		AuthEnv: []string{"ANTHROPIC_API_KEY"},
	}
	spec := Spec(Options{Agent: a, Project: "/p"}, netpolicy.Topology{SidecarIP: "10.0.0.2"})

	for _, e := range spec.Env {
		name := strings.SplitN(e, "=", 2)[0]
		switch name {
		case "ANTHROPIC_API_KEY", "AWS_ACCESS_KEY_ID", "GITHUB_TOKEN", "OPENAI_API_KEY":
			t.Errorf("credential %q passed without a grant", name)
		}
	}
}

func TestSpecEnvAuthModeRequiresExplicitValues(t *testing.T) {
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude",
		Base: "node:22", AllowHosts: []string{"api.anthropic.com"},
	}
	spec := Spec(Options{
		Agent: a, Project: "/p",
		AuthMode: "env", AuthEnv: []string{"ANTHROPIC_API_KEY=sk-test"},
	}, netpolicy.Topology{SidecarIP: "10.0.0.2"})

	var found bool
	for _, e := range spec.Env {
		if e == "ANTHROPIC_API_KEY=sk-test" {
			found = true
		}
	}
	if !found {
		t.Error("explicitly granted key not passed")
	}
	// Even in env mode the value must be NAME=VALUE, never a bare name.
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSpecMountsWorkspaceAndAgentVolumeOnly(t *testing.T) {
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude",
		Base: "node:22", AllowHosts: []string{"api.anthropic.com"},
	}
	spec := Spec(Options{Agent: a, Project: "/host/proj"}, netpolicy.Topology{SidecarIP: "10.0.0.2"})

	if len(spec.Mounts) != 2 {
		t.Fatalf("mounts = %+v", spec.Mounts)
	}
	if spec.Mounts[0].Source != "/host/proj" || spec.Mounts[0].Target != WorkspacePath {
		t.Errorf("workspace mount = %+v", spec.Mounts[0])
	}
	if !spec.Mounts[1].Volume || spec.Mounts[1].Source != "dev-agent-claude-config" {
		t.Errorf("config volume = %+v", spec.Mounts[1])
	}
	// No ~/.ssh, no ~/.gitconfig, no docker socket.
	for _, m := range spec.Mounts {
		if strings.Contains(m.Source, ".ssh") || strings.Contains(m.Source, "docker.sock") {
			t.Errorf("unexpected mount: %+v", m)
		}
	}
}

func TestAllowlistCombinesAgentDefaultsAndExtraHosts(t *testing.T) {
	a := &Agent{AllowHosts: []string{"api.anthropic.com"}}
	o := Options{Agent: a, ExtraHosts: []string{"internal.example.com"}}
	got := strings.Join(o.Allowlist(), ",")
	if got != "api.anthropic.com,internal.example.com" {
		t.Fatalf("Allowlist = %q", got)
	}
}

func TestTelemetryIsDisabledAtSourceNotAllowlisted(t *testing.T) {
	// The Datadog intake hosts carry optional operational telemetry.
	// Allowlisting them would permit the traffic; leaving them blocked
	// without disabling it produces a stream of denial notices for
	// something the user never wanted. Turning it off at the source does
	// neither.
	a, err := NewRegistry().Get("claude")
	if err != nil {
		t.Fatal(err)
	}

	allow, err := netpolicy.Parse(a.AllowHosts)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{
		"http-intake.logs.us5.datadoghq.com",
		"browser-intake-us5-datadoghq.com",
	} {
		if allow.Allows(h, 443) {
			t.Errorf("telemetry host %s is allowlisted", h)
		}
	}

	var found bool
	for _, e := range a.Env {
		if e == "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1" {
			found = true
		}
	}
	if !found {
		t.Error("telemetry not disabled at the source; notices will be noisy")
	}
}

func TestLoginHostsAreAllowed(t *testing.T) {
	// Sign-in touches all three: claude.com opens the page,
	// platform.claude.com does the OAuth token exchange for both account
	// types, claude.ai authenticates.
	a, err := NewRegistry().Get("claude")
	if err != nil {
		t.Fatal(err)
	}
	allow, err := netpolicy.Parse(a.AllowHosts)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"claude.com", "claude.ai", "platform.claude.com"} {
		if !allow.Allows(h, 443) {
			t.Errorf("login host %s blocked; first-run auth would fail", h)
		}
	}
}

func TestAgentEnvCannotOverrideSandboxProxySettings(t *testing.T) {
	// A definition that set HTTP_PROXY would route the agent around the
	// policy. Docker takes the last --env for a name, so the topology's
	// values must come after the agent's.
	a := &Agent{
		Name: "x", Binary: "x", ConfigDir: "/c", Base: "b",
		AllowHosts: []string{"a.com"},
		Env:        []string{"HTTP_PROXY=http://attacker.example.com:8080"},
	}
	topo := netpolicy.Topology{SidecarIP: "10.0.0.2", ProxyPort: 3128}
	spec := Spec(Options{Agent: a, Project: "/p"}, topo)

	var last string
	for _, e := range spec.Env {
		if strings.HasPrefix(e, "HTTP_PROXY=") {
			last = e
		}
	}
	if last != "HTTP_PROXY=http://10.0.0.2:3128" {
		t.Fatalf("effective HTTP_PROXY = %q, want the sandbox's", last)
	}
}

func TestSafeModeDropsAutoApproveArgs(t *testing.T) {
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/c", Base: "b",
		Args: []string{"--dangerously-skip-permissions"}, AllowHosts: []string{"a.com"},
	}
	topo := netpolicy.Topology{SidecarIP: "10.0.0.2"}

	def := Spec(Options{Agent: a, Project: "/p"}, topo)
	if len(def.Command) != 2 || def.Command[1] != "--dangerously-skip-permissions" {
		t.Fatalf("default command = %v, want auto-approve inside the sandbox", def.Command)
	}

	safe := Spec(Options{Agent: a, Project: "/p", Safe: true}, topo)
	if len(safe.Command) != 1 || safe.Command[0] != "claude" {
		t.Fatalf("--safe command = %v, want the bare binary", safe.Command)
	}
}

func TestOverlayInstallsItsOwnRuntime(t *testing.T) {
	// The agent must not require the base image to carry node. Assuming it
	// rules out running the agent on the project's own image, and an agent
	// that cannot run the project's tests cannot check its own work.
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude",
		Runtime: "node", RuntimeImage: "node:22-bookworm-slim",
		Install: "npm install -g x", AllowHosts: []string{"a.com"},
	}
	df := Dockerfile(a, "golang:1.26")

	if !strings.Contains(df, "FROM node:22-bookworm-slim AS runtime") {
		t.Errorf("no runtime stage:\n%s", df)
	}
	if !strings.Contains(df, "COPY --from=runtime /usr/local/bin/node") {
		t.Errorf("node not copied in:\n%s", df)
	}
	if !strings.Contains(df, "FROM golang:1.26") {
		t.Errorf("base image lost:\n%s", df)
	}
	// The runtime stage must come first, or the final image is node rather
	// than the project's.
	if strings.Index(df, "FROM node:22-bookworm-slim AS runtime") > strings.Index(df, "FROM golang:1.26") {
		t.Errorf("runtime stage must precede the base:\n%s", df)
	}
}

func TestRuntimeDoesNotClobberTheBaseToolchain(t *testing.T) {
	// golang keeps its toolchain in /usr/local/go. Copying node into
	// /usr/local would take the base's toolchain with it.
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/c",
		Runtime: "node", AllowHosts: []string{"a.com"},
	}
	df := Dockerfile(a, "golang:1.26")

	for _, line := range strings.Split(df, "\n") {
		if !strings.HasPrefix(line, "COPY --from=runtime") {
			continue
		}
		dest := line[strings.LastIndex(line, " ")+1:]
		if !strings.HasPrefix(dest, RuntimePath+"/") {
			t.Errorf("runtime copied outside %s: %q", RuntimePath, line)
		}
	}
	if !strings.Contains(df, "ENV PATH="+RuntimePath+"/bin:$PATH") {
		t.Errorf("runtime not on PATH:\n%s", df)
	}
}

func TestNoRuntimeStageWhenNoneRequested(t *testing.T) {
	a := &Agent{
		Name: "x", Binary: "x", ConfigDir: "/c", AllowHosts: []string{"a.com"},
	}
	df := Dockerfile(a, "alpine:3")
	if strings.Contains(df, "AS runtime") {
		t.Errorf("unrequested runtime stage:\n%s", df)
	}
	if !strings.HasPrefix(df, "FROM alpine:3\n") {
		t.Errorf("base should be the only stage:\n%s", df)
	}
}

func TestRuntimeImageIsPinnedNotFloating(t *testing.T) {
	// A floating runtime tag changes what runs in the sandbox between two
	// builds of the same agent version.
	for _, a := range NewRegistry().List() {
		if a.Runtime == "" {
			continue
		}
		img := a.runtimeImage()
		if !strings.Contains(img, ":") || strings.HasSuffix(img, ":latest") {
			t.Errorf("%s: runtime image %q is not pinned", a.Name, img)
		}
	}
}

func TestGitIdentityWithoutPushGrant(t *testing.T) {
	// Commit inside, review and push from the host: that is the default
	// review boundary, not a gap. The identity must be present so commits
	// are attributable, with no path to a remote.
	a := &Agent{Name: "x", Binary: "x", ConfigDir: "/c", AllowHosts: []string{"a.com"}}
	spec := Spec(Options{
		Agent: a, Project: "/p",
		GitIdentity: [2]string{"Ada", "ada@example.com"},
	}, netpolicy.Topology{SidecarIP: "10.0.0.2"})

	env := strings.Join(spec.Env, "\n")
	if !strings.Contains(env, "GIT_AUTHOR_NAME=Ada") ||
		!strings.Contains(env, "GIT_COMMITTER_EMAIL=ada@example.com") {
		t.Errorf("git identity missing:\n%s", env)
	}
	if strings.Contains(env, "SSH_AUTH_SOCK") {
		t.Error("ssh agent forwarded without --allow-push")
	}
	for _, m := range spec.Mounts {
		if strings.Contains(m.Target, "ssh") {
			t.Errorf("unexpected ssh mount: %+v", m)
		}
	}
}

func TestAllowPushForwardsTheSocketNotAKey(t *testing.T) {
	// The socket lets the agent sign; it never exposes the key itself, so
	// there is nothing in the container to exfiltrate, and revoking is
	// killing the agent rather than rotating a credential.
	a := &Agent{Name: "x", Binary: "x", ConfigDir: "/c", AllowHosts: []string{"a.com"}}
	spec := Spec(Options{
		Agent: a, Project: "/p", SSHAuthSock: "/tmp/ssh-agent.sock",
	}, netpolicy.Topology{SidecarIP: "10.0.0.2"})

	var forwarded bool
	for _, m := range spec.Mounts {
		if m.Source == "/tmp/ssh-agent.sock" {
			forwarded = true
			if m.Volume {
				t.Error("socket mounted as a volume")
			}
		}
		// A key file must never be mounted, however convenient.
		if strings.Contains(m.Source, "id_rsa") || strings.Contains(m.Source, "id_ed25519") ||
			strings.HasSuffix(m.Source, "/.ssh") {
			t.Errorf("key material mounted: %+v", m)
		}
	}
	if !forwarded {
		t.Fatalf("socket not forwarded: %+v", spec.Mounts)
	}
	if !strings.Contains(strings.Join(spec.Env, "\n"), "SSH_AUTH_SOCK=/run/ssh-agent.sock") {
		t.Errorf("SSH_AUTH_SOCK not pointed at the forwarded socket:\n%v", spec.Env)
	}
}

func TestPushRoutesSSHThroughTheProxy(t *testing.T) {
	// ssh does not speak HTTP proxying and the container has no other
	// route out, so forwarding the agent socket alone yields "network
	// unreachable". Routing ssh through the same CONNECT proxy keeps git
	// subject to the allowlist rather than needing a hole punched for it.
	a := &Agent{Name: "x", Binary: "x", ConfigDir: "/c", AllowHosts: []string{"a.com"}}
	spec := Spec(Options{
		Agent: a, Project: "/p", SSHAuthSock: "/tmp/agent.sock",
	}, netpolicy.Topology{SidecarIP: "10.9.9.9", ProxyPort: 3128})

	var cmd string
	for _, e := range spec.Env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			cmd = e
		}
	}
	if cmd == "" {
		t.Fatalf("no GIT_SSH_COMMAND:\n%v", spec.Env)
	}
	if !strings.Contains(cmd, "nc -X connect -x 10.9.9.9:3128") {
		t.Errorf("ssh not routed through the sidecar: %q", cmd)
	}
	// StrictHostKeyChecking=no would accept a substituted key silently.
	if !strings.Contains(cmd, "StrictHostKeyChecking=accept-new") {
		t.Errorf("host key policy too weak: %q", cmd)
	}
	if strings.Contains(cmd, "StrictHostKeyChecking=no") {
		t.Errorf("host key checking disabled: %q", cmd)
	}
}

func TestNoSSHProxyRoutingWithoutPushGrant(t *testing.T) {
	a := &Agent{Name: "x", Binary: "x", ConfigDir: "/c", AllowHosts: []string{"a.com"}}
	spec := Spec(Options{Agent: a, Project: "/p"},
		netpolicy.Topology{SidecarIP: "10.9.9.9", ProxyPort: 3128})

	for _, e := range spec.Env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			t.Errorf("ssh routing configured without a push grant: %q", e)
		}
	}
}

func TestForwardedSocketGroupIsAdded(t *testing.T) {
	a := &Agent{Name: "x", Binary: "x", ConfigDir: "/c", AllowHosts: []string{"a.com"}}
	spec := Spec(Options{
		Agent: a, Project: "/p", SSHAuthSock: "/tmp/agent.sock", SSHSockGID: "67278",
	}, netpolicy.Topology{SidecarIP: "10.0.0.2"})

	args := strings.Join(spec.Args(), " ")
	if !strings.Contains(args, "--group-add 67278") {
		t.Errorf("socket group not added: %s", args)
	}
	// The uid must not change: running as the socket's owner would hand
	// the container the host user's identity.
	if !strings.Contains(args, "--user "+container.HostUser()) {
		t.Errorf("uid changed to reach the socket: %s", args)
	}
}

// `dev agent run claude -- "fix the retry logic"` reads as a prompt in
// every other tool that takes one. Treating trailing arguments as a
// replacement command made the container try to exec the prompt, which
// fails with "executable file not found" and status 127 — for the most
// obvious way to use the command.
func TestTrailingArgumentsGoToTheAgentNotInsteadOfIt(t *testing.T) {
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/c", Base: "b",
		Args: []string{"--dangerously-skip-permissions"},
	}
	spec := Spec(Options{
		Agent: a,
		Args:  []string{"finish the work in todo.txt"},
	}, netpolicy.Topology{})

	if len(spec.Command) < 2 {
		t.Fatalf("command = %v", spec.Command)
	}
	if spec.Command[0] != a.Binary {
		t.Fatalf("command starts with %q, want the agent binary", spec.Command[0])
	}
	if last := spec.Command[len(spec.Command)-1]; last != "finish the work in todo.txt" {
		t.Fatalf("prompt did not reach the agent: %v", spec.Command)
	}
	// The auto-approve default still applies; a prompt is not a reason to
	// silently change the agent's posture.
	if !contains(spec.Command, "--dangerously-skip-permissions") {
		t.Fatalf("default args dropped when a prompt was given: %v", spec.Command)
	}
}

func TestSafeStillDropsAutoApproveWithAPrompt(t *testing.T) {
	a := &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/c", Base: "b",
		Args: []string{"--dangerously-skip-permissions"},
	}
	spec := Spec(Options{Agent: a, Safe: true, Args: []string{"do the thing"}}, netpolicy.Topology{})
	if contains(spec.Command, "--dangerously-skip-permissions") {
		t.Fatalf("--safe did not drop auto-approve: %v", spec.Command)
	}
	if spec.Command[len(spec.Command)-1] != "do the thing" {
		t.Fatalf("prompt lost: %v", spec.Command)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The rename moved this volume, and the volume holds an OAuth login. A
// fresh empty one would look like a bug: the agent just asks you to log in
// again, with nothing to explain why.
func TestEnsureVolumeAdoptsThePreRenameVolume(t *testing.T) {
	fake := runner.NewFake()
	// Keys are a prefix of the whole rendered command, which the backend
	// wraps in its own invocation.
	const orb = "orb -m vm sudo docker "
	// Nothing exists but the one from before the rename.
	fake.Response[orb+"volume inspect dev-agent-claude-config"] = runner.Result{ExitCode: 1}
	fake.Response[orb+"volume inspect dev-agent-claude"] = runner.Result{ExitCode: 1}
	fake.Response[orb+"volume inspect dev2-agent-claude"] = runner.Result{ExitCode: 0}

	r := &Runner{
		Engine: container.New(orbstack.New("vm", fake)),
		Out:    io.Discard,
	}
	a := &Agent{Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude", Base: "b"}
	if err := r.EnsureVolume(context.Background(), a); err != nil {
		t.Fatal(err)
	}

	var created, copied bool
	for _, c := range fake.Calls {
		line := c.String()
		if strings.Contains(line, "volume create dev-agent-claude-config") {
			created = true
		}
		// The copy runs in a container because the volumes live in the VM.
		if strings.Contains(line, "source=dev2-agent-claude") &&
			strings.Contains(line, "source=dev-agent-claude-config") {
			copied = true
		}
	}
	if !created {
		t.Fatal("did not create the new volume")
	}
	if !copied {
		t.Fatalf("did not copy the old volume in; calls:\n%s", callLines(fake))
	}
}

// Narrowing the volume from the home directory to the config directory
// renamed it, and the old one holds a login. A fresh empty volume would
// read as the tool having logged the user out for no reason, so the config
// directory inside the old home is carried across — and only that, or the
// narrowing would undo itself on every machine that had run an agent
// before.
func TestEnsureVolumeCarriesTheConfigDirOutOfTheOldHomeVolume(t *testing.T) {
	fake := runner.NewFake()
	const orb = "orb -m vm sudo docker "
	fake.Response[orb+"volume inspect dev-agent-claude-config"] = runner.Result{ExitCode: 1}
	fake.Response[orb+"volume inspect dev-agent-claude"] = runner.Result{ExitCode: 0}

	r := &Runner{Engine: container.New(orbstack.New("vm", fake)), Out: io.Discard}
	a := &Agent{Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude", Base: "b"}
	if err := r.EnsureVolume(context.Background(), a); err != nil {
		t.Fatal(err)
	}

	var copied bool
	for _, c := range fake.Calls {
		line := c.String()
		if !strings.Contains(line, "source=dev-agent-claude,") &&
			!strings.Contains(line, "source=dev-agent-claude ") {
			continue
		}
		if !strings.Contains(line, "/from/.claude/.") {
			t.Errorf("the whole old home was copied, not just the config dir:\n%s", line)
		}
		copied = true
	}
	if !copied {
		t.Fatalf("the old home volume was not read at all; calls:\n%s", callLines(fake))
	}
}

// With nothing to adopt, it must not run a pointless container.
func TestEnsureVolumeSkipsAdoptionWhenThereIsNoOldVolume(t *testing.T) {
	fake := runner.NewFake()
	fake.Response["orb -m vm sudo docker volume inspect"] = runner.Result{ExitCode: 1}

	r := &Runner{Engine: container.New(orbstack.New("vm", fake)), Out: io.Discard}
	a := &Agent{Name: "claude", Binary: "claude", ConfigDir: "/c", Base: "b"}
	if err := r.EnsureVolume(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	for _, c := range fake.Calls {
		if strings.Contains(c.String(), "docker run") {
			t.Fatalf("ran a container with nothing to adopt:\n%s", callLines(fake))
		}
	}
}

func callLines(f *runner.Fake) string {
	var b strings.Builder
	for _, c := range f.Calls {
		b.WriteString("  " + c.String() + "\n")
	}
	return b.String()
}

// An external review's first finding, and the sharpest kind: a command
// that does the opposite of what it says. Migration keeps the old home
// volume deliberately, and EnsureVolume imports from it whenever the
// config volume is missing — which is precisely the state logout leaves.
// So logout, run, and the credential came back.
func TestLogoutDoesNotLeaveALoginToBeRestored(t *testing.T) {
	fake := runner.NewFake()
	const orb = "orb -m vm sudo docker "
	// Both volumes exist, which is what a machine looks like after the
	// migration ran.
	fake.Response[orb+"volume inspect dev-agent-claude-config"] = runner.Result{ExitCode: 0}
	fake.Response[orb+"volume inspect dev-agent-claude"] = runner.Result{ExitCode: 0}

	r := &Runner{Engine: container.New(orbstack.New("vm", fake)), Out: io.Discard}
	a := &Agent{Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude", Base: "b"}
	if err := r.Logout(context.Background(), a); err != nil {
		t.Fatal(err)
	}

	removed := map[string]bool{}
	for _, c := range fake.Calls {
		if line := c.String(); strings.Contains(line, "volume rm ") {
			removed[strings.TrimSpace(line[strings.LastIndex(line, " ")+1:])] = true
		}
	}
	if !removed["dev-agent-claude-config"] {
		t.Errorf("logout left the config volume; removed: %v", removed)
	}
	if !removed["dev-agent-claude"] {
		t.Errorf("logout left the volume it was migrated from, so the next run "+
			"copies the login back in; removed: %v", removed)
	}
}

// The overlay's instructions name its base by tag, so rebuilding a project
// image under the same tag leaves the text identical while the thing
// underneath has changed. Without the base's identity in the marker, the
// overlay reads as current forever and the agent runs on an image the
// project replaced.
func TestTheOverlayMarkerFollowsTheBaseImage(t *testing.T) {
	a := &Agent{Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude",
		Base: "debian:bookworm-slim", AllowHosts: []string{"api.anthropic.com"}}
	df := overlayDockerfile(Options{Agent: a})

	if sourceMarker(df, "sha256:aaa") == sourceMarker(df, "sha256:bbb") {
		t.Error("the same instructions on two different base images produce one marker")
	}
	// Stable for the same inputs, or every run would rebuild. Written
	// through variables because a linter reads the direct form as a
	// tautology — which it is, textually, and is not once the arguments
	// come from two calls that could disagree.
	first, second := sourceMarker(df, "sha256:aaa"), sourceMarker(overlayDockerfile(
		Options{Agent: a}), "sha256:aaa")
	if first != second {
		t.Error("the marker is not stable for identical inputs")
	}
}
