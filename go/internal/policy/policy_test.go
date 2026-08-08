package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(DefaultPath(root), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func load(t *testing.T, body string) *Policy {
	t.Helper()
	p, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoPolicyConstrainsNothing(t *testing.T) {
	p, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.Active() {
		t.Error("an absent policy reported itself active")
	}
	for _, check := range []error{
		p.CheckNetwork("open"), p.CheckSetting("mount_docker_socket"),
		p.CheckHost("anywhere.example.com"), p.CheckRegistry("golang:1.26"),
	} {
		if check != nil {
			t.Errorf("absent policy refused something: %v", check)
		}
	}
}

func TestMalformedPolicyIsAnError(t *testing.T) {
	// A rule that fails to parse is a rule that silently stops applying.
	if _, err := Load(write(t, "forbid: [unclosed\n")); err == nil {
		t.Fatal("malformed policy loaded as if empty")
	}
}

func TestNetworkModesRestricted(t *testing.T) {
	p := load(t, "network_modes: [allowlist, none]\n")
	if err := p.CheckNetwork("allowlist"); err != nil {
		t.Errorf("permitted mode refused: %v", err)
	}
	err := p.CheckNetwork("open")
	if err == nil {
		t.Fatal("`open` allowed despite the policy")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("error does not say what is permitted: %v", err)
	}
}

func TestForbiddenSettingsCannotBeAccepted(t *testing.T) {
	p := load(t, "forbid: [mount_docker_socket, pass_env_vars]\n")
	if err := p.CheckSetting("mount_docker_socket"); err == nil {
		t.Fatal("forbidden setting permitted")
	}
	if err := p.CheckSetting("network"); err != nil {
		t.Errorf("unrelated setting refused: %v", err)
	}
}

func TestDeniedHostsMatchSubdomains(t *testing.T) {
	// A deny rule that misses a subdomain is a deny rule that does not
	// work; an allow rule that matches too much grants more than intended.
	p := load(t, "deny_hosts:\n  - pastebin.com\n  - \"*.evil.example.com\"\n")

	for _, host := range []string{
		"pastebin.com", "raw.pastebin.com", "PASTEBIN.COM",
		"pastebin.com:443", "a.b.evil.example.com", "evil.example.com",
	} {
		if err := p.CheckHost(host); err == nil {
			t.Errorf("denied host %q was permitted", host)
		}
	}
	for _, host := range []string{"example.com", "notpastebin.com", "pastebin.com.example.org"} {
		if err := p.CheckHost(host); err != nil {
			t.Errorf("unrelated host %q was denied: %v", host, err)
		}
	}
}

func TestRegistryRestriction(t *testing.T) {
	p := load(t, "allowed_registries: [ghcr.io, docker.io]\n")
	for _, image := range []string{
		"golang:1.26", "docker.io/library/golang:1.26",
		"ghcr.io/org/app:1", "library/golang",
	} {
		if err := p.CheckRegistry(image); err != nil {
			t.Errorf("permitted image %q refused: %v", image, err)
		}
	}
	err := p.CheckRegistry("quay.io/org/app:1")
	if err == nil {
		t.Fatal("image from a forbidden registry permitted")
	}
	if !strings.Contains(err.Error(), "quay.io") {
		t.Errorf("error does not name the registry: %v", err)
	}
}

func TestRegistryOf(t *testing.T) {
	cases := map[string]string{
		"golang:1.26":              "docker.io",
		"library/golang":           "docker.io",
		"docker.io/library/golang": "docker.io",
		"ghcr.io/org/app:1":        "ghcr.io",
		"localhost:5000/app":       "localhost:5000",
		"registry.example.com/a/b": "registry.example.com",
		"golang@sha256:abc":        "docker.io",
	}
	for in, want := range cases {
		if got := RegistryOf(in); got != want {
			t.Errorf("RegistryOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScanSeverityIsAFloorNotADefault(t *testing.T) {
	// "Minimum severity" means the loosest threshold permitted: asking to
	// fail only on critical when policy says high would let high findings
	// through.
	p := load(t, "min_scan_severity: high\n")

	got, raised := p.FloorSeverity("critical")
	if !raised || got != "high" {
		t.Fatalf("critical was not lowered to the floor: %q %v", got, raised)
	}
	if got, raised := p.FloorSeverity("medium"); raised || got != "medium" {
		t.Errorf("a stricter request was altered: %q %v", got, raised)
	}
}

func TestDescribeNamesEveryRule(t *testing.T) {
	p := load(t, `network_modes: [allowlist]
forbid: [mount_docker_socket]
deny_hosts: [pastebin.com]
require:
  memory: 4g
  cpus: "2"
min_scan_severity: high
allowed_registries: [ghcr.io]
`)
	got := strings.Join(p.Describe(), "\n")
	for _, want := range []string{
		"allowlist", "mount_docker_socket", "pastebin.com", "4g", "2", "high", "ghcr.io",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe() omits %q:\n%s", want, got)
		}
	}
	if p.Path() != filepath.Join(filepath.Dir(p.Path()), "policy.yaml") {
		t.Errorf("Path() = %q", p.Path())
	}
}
