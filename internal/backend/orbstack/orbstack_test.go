package orbstack

import (
	"context"
	"testing"

	"github.com/mwing/isolated-dev/internal/backend"
	"github.com/mwing/isolated-dev/internal/runner"
)

const orbList = `NAME                    STATE     DISTRO   VERSION  ARCH
dev-vm-docker-host      running   ubuntu   noble    arm64
dev-vm-other            stopped   ubuntu   noble    arm64
`

// newTestDriver builds a driver whose PATH lookup is deterministic, so the
// suite behaves identically on a macOS host with OrbStack and in Linux CI
// without it.
func newTestDriver(vm string, f *runner.Fake, orbInstalled bool) *Driver {
	d := New(vm, f)
	d.LookPath = func(bin string) (string, bool) {
		if bin == "orb" && orbInstalled {
			return "/usr/local/bin/orb", true
		}
		return "", false
	}
	return d
}

func TestParseVMStateExactNameMatch(t *testing.T) {
	// v1 grepped "<name>.*running", so a stopped VM whose name is a prefix
	// of a running one reported as running. The first field must match.
	cases := []struct {
		vm              string
		exists, running bool
	}{
		{"dev-vm-docker-host", true, true},
		{"dev-vm-other", true, false},
		{"dev-vm", false, false},
		{"docker-host", false, false},
		{"missing", false, false},
	}
	for _, c := range cases {
		exists, running := parseVMState(orbList, c.vm)
		if exists != c.exists || running != c.running {
			t.Errorf("parseVMState(%q) = (%t,%t), want (%t,%t)",
				c.vm, exists, running, c.exists, c.running)
		}
	}
}

func TestDockerCallGoesThroughVMWithSudo(t *testing.T) {
	f := runner.NewFake()
	d := New("my-vm", f)

	if _, err := d.Docker(context.Background(), backend.Call{
		Args: []string{"run", "-e", "MSG=hello world", "img"},
	}); err != nil {
		t.Fatalf("Docker: %v", err)
	}

	last, err := f.Last()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-m", "my-vm", "sudo", "docker", "run", "-e", "MSG=hello world", "img"}
	if last.Path != "orb" {
		t.Errorf("path = %q, want orb", last.Path)
	}
	if len(last.Args) != len(want) {
		t.Fatalf("args = %v, want %v", last.Args, want)
	}
	for i := range want {
		if last.Args[i] != want[i] {
			t.Fatalf("args = %v, want %v", last.Args, want)
		}
	}
}

func TestProbeReportsMissingVM(t *testing.T) {
	f := runner.NewFake()
	f.Response["orb list"] = runner.Result{Stdout: orbList}
	d := newTestDriver("nonexistent-vm", f, true)

	st, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if st.VMExists {
		t.Error("VMExists should be false")
	}
	if st.Ready() {
		t.Error("Ready() should be false without a VM")
	}
	if st.Detail == "" {
		t.Error("Detail should explain what to do")
	}
}

func TestProbeReportsMissingCLIWithoutRunningAnything(t *testing.T) {
	// The Linux CI case: no OrbStack at all. Probe must say so and stop,
	// not shell out to a binary that isn't there.
	f := runner.NewFake()
	d := newTestDriver("dev-vm-docker-host", f, false)

	st, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if st.CLIFound || st.Ready() {
		t.Errorf("status = %+v", st)
	}
	if len(f.Calls) != 0 {
		t.Errorf("Probe ran commands despite a missing CLI: %v", f.Lines())
	}
	if st.Detail == "" {
		t.Error("Detail should tell the user how to install OrbStack")
	}
}

func TestProbeReportsStoppedVMWithoutStartingIt(t *testing.T) {
	f := runner.NewFake()
	f.Response["orb list"] = runner.Result{Stdout: orbList}
	d := newTestDriver("dev-vm-other", f, true)

	st, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !st.VMExists || st.VMRunning {
		t.Errorf("status = %+v", st)
	}
	// doctor diagnoses; it must never mutate.
	for _, line := range f.Lines() {
		if line == "orb start dev-vm-other" {
			t.Fatal("Probe started the VM")
		}
	}
}

func TestProbeReportsDaemonVersionWhenHealthy(t *testing.T) {
	f := runner.NewFake()
	f.Response["orb list"] = runner.Result{Stdout: orbList}
	f.Response["orb -m dev-vm-docker-host sudo docker version"] = runner.Result{Stdout: "27.1.1\n"}
	d := newTestDriver("dev-vm-docker-host", f, true)

	st, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !st.Ready() {
		t.Fatalf("expected ready, got %+v", st)
	}
	if st.DaemonVersion != "27.1.1" {
		t.Errorf("DaemonVersion = %q", st.DaemonVersion)
	}
}

func TestProbeHandlesUnreachableDaemon(t *testing.T) {
	f := runner.NewFake()
	f.Response["orb list"] = runner.Result{Stdout: orbList}
	f.Response["orb -m dev-vm-docker-host sudo docker version"] = runner.Result{
		ExitCode: 1, Stderr: "Cannot connect to the Docker daemon",
	}
	d := newTestDriver("dev-vm-docker-host", f, true)

	st, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe should not error on a non-zero exit: %v", err)
	}
	if st.DaemonUp || st.Ready() {
		t.Errorf("status = %+v", st)
	}
}
