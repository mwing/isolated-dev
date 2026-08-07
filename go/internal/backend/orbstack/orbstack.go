// Package orbstack drives docker inside an OrbStack Linux VM, reproducing
// v1 behavior: every docker call is `orb -m <vm> sudo docker ...`.
package orbstack

import (
	"bufio"
	"context"
	"strings"

	"github.com/mwing/isolated-dev/go/internal/backend"
	"github.com/mwing/isolated-dev/go/internal/runner"
)

// Driver implements backend.Backend on top of the orb CLI.
type Driver struct {
	VM     string
	Runner runner.Runner
	// LookPath resolves a binary on PATH. Injected so tests do not depend
	// on whether the host running them happens to have OrbStack installed.
	LookPath func(string) (string, bool)
}

// New returns a driver for a named VM.
func New(vm string, r runner.Runner) *Driver {
	return &Driver{VM: vm, Runner: r, LookPath: runner.LookPath}
}

func (d *Driver) lookPath(bin string) (string, bool) {
	if d.LookPath == nil {
		return runner.LookPath(bin)
	}
	return d.LookPath(bin)
}

// Name identifies the driver.
func (d *Driver) Name() string { return "orbstack" }

// Docker runs a docker invocation inside the VM.
func (d *Driver) Docker(ctx context.Context, call backend.Call) (runner.Result, error) {
	args := append([]string{"-m", d.VM, "sudo", "docker"}, call.Args...)
	return d.Runner.Run(ctx, runner.Command{
		Path:   "orb",
		Args:   args,
		Stdin:  call.Stdin,
		Stdout: call.Stdout,
		Stderr: call.Stderr,
		PTY:    call.PTY,
	})
}

// Probe reports OrbStack availability without changing any state: it never
// starts the VM. `doctor` diagnoses, it does not repair.
func (d *Driver) Probe(ctx context.Context) (backend.Status, error) {
	st := backend.Status{Backend: d.Name(), VMName: d.VM}

	path, found := d.lookPath("orb")
	st.CLIFound, st.CLIPath = found, path
	if !found {
		st.Detail = "orb CLI not on PATH; install OrbStack from https://orbstack.dev"
		return st, nil
	}

	list, err := d.Runner.Run(ctx, runner.Command{Path: "orb", Args: []string{"list"}})
	if err != nil {
		return st, err
	}
	if list.ExitCode != 0 {
		st.Detail = "`orb list` failed; is the OrbStack app running?"
		return st, nil
	}

	st.VMExists, st.VMRunning = parseVMState(list.Stdout, d.VM)
	if !st.VMExists {
		st.Detail = "VM not found; create it with `dev env up docker-host`"
		return st, nil
	}
	if !st.VMRunning {
		st.Detail = "VM exists but is not running; start it with `orb start " + d.VM + "`"
		return st, nil
	}

	ver, err := d.Docker(ctx, backend.Call{
		Args: []string{"version", "--format", "{{.Server.Version}}"},
	})
	if err != nil {
		return st, err
	}
	if ver.ExitCode != 0 {
		st.Detail = "docker daemon not reachable inside the VM; it may still be starting"
		return st, nil
	}
	st.DaemonUp = true
	st.DaemonVersion = strings.TrimSpace(ver.Stdout)
	return st, nil
}

// parseVMState reads `orb list` output. v1 grepped for "<name>.*running",
// which matches any VM whose name merely contains the target and would
// report a running VM when a differently-named one is running. Here the
// name must match the first field exactly.
func parseVMState(out, vm string) (exists, running bool) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || fields[0] != vm {
			continue
		}
		exists = true
		for _, f := range fields[1:] {
			if strings.EqualFold(f, "running") {
				running = true
			}
		}
		return exists, running
	}
	return false, false
}
