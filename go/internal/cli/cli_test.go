package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/runner"
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
