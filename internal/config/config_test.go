package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	return DefaultPaths(filepath.Join(dir, "home"), filepath.Join(dir, "project"))
}

func TestLoadDefaultsWhenNoFiles(t *testing.T) {
	cfg, err := Load(testPaths(t), nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.VMName != "dev-vm-docker-host" {
		t.Errorf("VMName = %q", cfg.VMName)
	}
	if !cfg.AutoStartVM {
		t.Error("AutoStartVM should default true")
	}
	if cfg.MountSSHKeys || cfg.MountGitConfig || cfg.MountDockerSocket {
		t.Error("mounts must default to false")
	}
	if !cfg.PassEnvVars.Empty() {
		t.Error("pass_env_vars must default empty")
	}
	if o := cfg.Origin("vm_name"); o != OriginDefault {
		t.Errorf("origin = %v, want default", o)
	}
}

func TestProjectOverridesGlobal(t *testing.T) {
	p := testPaths(t)
	write(t, p.Global, "vm_name: global-vm\ncontainer_prefix: gp\n")
	write(t, p.Project, "vm_name: project-vm\n")

	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.VMName != "project-vm" {
		t.Errorf("VMName = %q, want project-vm", cfg.VMName)
	}
	if cfg.ContainerPrefix != "gp" {
		t.Errorf("ContainerPrefix = %q, want gp (from global)", cfg.ContainerPrefix)
	}
	if cfg.Origin("vm_name") != OriginProject {
		t.Errorf("vm_name origin = %v", cfg.Origin("vm_name"))
	}
	if cfg.Origin("container_prefix") != OriginGlobal {
		t.Errorf("container_prefix origin = %v", cfg.Origin("container_prefix"))
	}
}

func TestExplicitFalseOverridesGlobalTrue(t *testing.T) {
	// The reason File uses pointers: `false` is a decision, not an absence.
	p := testPaths(t)
	write(t, p.Global, "mount_ssh_keys: true\n")
	write(t, p.Project, "mount_ssh_keys: false\n")

	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MountSSHKeys {
		t.Fatal("project `false` must override global `true`")
	}
}

func TestEnvOverridesFiles(t *testing.T) {
	p := testPaths(t)
	write(t, p.Global, "vm_name: global-vm\n")
	write(t, p.Project, "vm_name: project-vm\n")

	cfg, err := Load(p, []string{"DEV_VM_NAME=env-vm"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.VMName != "env-vm" {
		t.Errorf("VMName = %q, want env-vm", cfg.VMName)
	}
	if cfg.Origin("vm_name") != OriginEnv {
		t.Errorf("origin = %v, want environment", cfg.Origin("vm_name"))
	}
}

func TestEnvBooleanAndNumberParsing(t *testing.T) {
	p := testPaths(t)
	cfg, err := Load(p, []string{
		"DEV_MOUNT_DOCKER_SOCKET=true",
		"DEV_CACHE_TTL=120",
		"DEV_AUTO_START_VM=not-a-bool",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MountDockerSocket {
		t.Error("DEV_MOUNT_DOCKER_SOCKET=true not honored")
	}
	if cfg.CacheTTL != 120 {
		t.Errorf("CacheTTL = %d", cfg.CacheTTL)
	}
	if !cfg.AutoStartVM {
		t.Error("unparseable boolean must leave the default intact")
	}
	if !hasNote(cfg, "DEV_AUTO_START_VM") {
		t.Error("expected a note about the unparseable boolean")
	}
}

func TestPassEnvVarsNestedBlock(t *testing.T) {
	p := testPaths(t)
	write(t, p.Project, `pass_env_vars:
  patterns:
    - AWS_*
    - GITHUB_*
  explicit:
    - MY_VAR
`)
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.PassEnvVars.Patterns) != 2 || cfg.PassEnvVars.Patterns[0] != "AWS_*" {
		t.Errorf("patterns = %v", cfg.PassEnvVars.Patterns)
	}
	if len(cfg.PassEnvVars.Explicit) != 1 || cfg.PassEnvVars.Explicit[0] != "MY_VAR" {
		t.Errorf("explicit = %v", cfg.PassEnvVars.Explicit)
	}
}

func TestDeadKeysAreReportedNotHonored(t *testing.T) {
	p := testPaths(t)
	write(t, p.Global, "network_mode: host\nport_range: \"1-2\"\nvm_name: keep-me\n")

	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.VMName != "keep-me" {
		t.Errorf("live keys must still load: %q", cfg.VMName)
	}
	if !hasNote(cfg, "network_mode") || !hasNote(cfg, "port_range") {
		t.Fatalf("dead keys not reported: %v", cfg.Notes)
	}
	for _, n := range cfg.Notes {
		if n.Key == "network_mode" && !strings.Contains(n.Text, "never implemented") {
			t.Errorf("unhelpful note: %s", n.Text)
		}
	}
}

func TestDeadEnvVarsAreReported(t *testing.T) {
	cfg, err := Load(testPaths(t), []string{"DEV_NETWORK_MODE=host"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !hasNote(cfg, "DEV_NETWORK_MODE") {
		t.Fatalf("dead env var not reported: %v", cfg.Notes)
	}
}

func TestUnknownKeyReported(t *testing.T) {
	p := testPaths(t)
	write(t, p.Global, "totally_made_up: 1\n")
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !hasNote(cfg, "totally_made_up") {
		t.Fatalf("unknown key not reported: %v", cfg.Notes)
	}
}

func TestMalformedYAMLIsAnError(t *testing.T) {
	p := testPaths(t)
	write(t, p.Project, "vm_name: [unclosed\n")
	if _, err := Load(p, nil); err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

func TestAsksExtractsOnlySecurityRelevantFields(t *testing.T) {
	p := testPaths(t)
	write(t, p.Project, `container_prefix: irrelevant
mount_ssh_keys: true
pass_env_vars:
  patterns:
    - AWS_*
`)
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	asks := cfg.Asks()
	if asks.Empty() {
		t.Fatal("asks should not be empty")
	}
	if !asks.MountSSHKeys || len(asks.PassEnvPatterns) != 1 {
		t.Fatalf("asks = %+v", asks)
	}

	desc := strings.Join(asks.Describe(), "\n")
	if !strings.Contains(desc, "~/.ssh") || !strings.Contains(desc, "AWS_*") {
		t.Errorf("Describe() = %q", desc)
	}
	if strings.Contains(desc, "irrelevant") {
		t.Error("non-security config leaked into the grant description")
	}
}

func TestAsksIgnoreCosmeticChanges(t *testing.T) {
	// ROADMAP 4.2: editing a non-security key must not change the grant
	// set, or every prompt becomes noise the user learns to click through.
	p1 := testPaths(t)
	write(t, p1.Project, "mount_ssh_keys: true\ncontainer_prefix: one\n")
	p2 := testPaths(t)
	write(t, p2.Project, "mount_ssh_keys: true\ncontainer_prefix: two\nmemory_limit: 2g\n")

	c1, err := Load(p1, nil)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Load(p2, nil)
	if err != nil {
		t.Fatal(err)
	}

	a, b := c1.Asks(), c2.Asks()
	if strings.Join(a.Describe(), "|") != strings.Join(b.Describe(), "|") {
		t.Fatalf("cosmetic edit changed the grant set:\n%v\n%v", a, b)
	}
}

func hasNote(c Config, key string) bool {
	for _, n := range c.Notes {
		if n.Key == key {
			return true
		}
	}
	return false
}
