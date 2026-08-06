package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/go/internal/netpolicy"
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
	if got := a.VolumeName(); got != "dev2-agent-claude" {
		t.Errorf("VolumeName = %q", got)
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
	if lastUser != "USER 1000:1000" {
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
		"--user 1000:1000",
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
	if !spec.Mounts[1].Volume || spec.Mounts[1].Source != "dev2-agent-claude" {
		t.Errorf("home volume = %+v", spec.Mounts[1])
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
