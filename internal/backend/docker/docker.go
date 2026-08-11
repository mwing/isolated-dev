// Package docker talks to a docker daemon directly, with no VM in front of
// it.
//
// It exists for two reasons, and the second is the important one. Linux
// users have a local daemon and no OrbStack. And the integration tests need
// a backend they can actually reach: every bug this project's review found
// survived a green suite because the tests stopped above the daemon, and a
// tier that runs against a real one needs somewhere to run.
package docker

import (
	"context"
	"fmt"

	"github.com/mwing/isolated-dev/internal/backend"
	"github.com/mwing/isolated-dev/internal/runner"
)

// Driver runs docker as a local command.
type Driver struct {
	Runner runner.Runner
	// LookPath resolves the binary. Injected so a test does not depend on
	// what the machine running it happens to have installed.
	LookPath func(string) (string, bool)
}

// New returns a driver using r.
func New(r runner.Runner) *Driver {
	return &Driver{Runner: r, LookPath: runner.LookPath}
}

// Name identifies the backend in diagnostics.
func (d *Driver) Name() string { return "docker" }

// Docker runs a docker command.
func (d *Driver) Docker(ctx context.Context, call backend.Call) (runner.Result, error) {
	return d.Runner.Run(ctx, runner.Command{
		Path:   "docker",
		Args:   call.Args,
		Stdin:  call.Stdin,
		Stdout: call.Stdout,
		Stderr: call.Stderr,
		PTY:    call.PTY,
	})
}

// Probe reports what can be seen of the local daemon.
//
// There is no VM, so the VM fields describe its absence rather than
// pretending: a report claiming a running VM on a machine that has none
// would send someone looking for a problem that is not there.
func (d *Driver) Probe(ctx context.Context) (backend.Status, error) {
	st := backend.Status{Backend: d.Name(), VMName: "(none: local daemon)"}

	lookPath := d.LookPath
	if lookPath == nil {
		lookPath = runner.LookPath
	}
	path, found := lookPath("docker")
	st.CLIFound, st.CLIPath = found, path
	if !found {
		st.Detail = "docker is not on PATH"
		return st, nil
	}
	// A local daemon needs nothing started, so "exists" and "running"
	// describe the daemon itself rather than a machine around it.
	st.VMExists, st.VMRunning = true, true

	res, err := d.Docker(ctx, backend.Call{Args: []string{"version", "--format", "{{.Server.Version}}"}})
	if err != nil {
		return st, err
	}
	if res.ExitCode != 0 {
		st.Detail = "docker is installed but the daemon did not answer: " +
			firstLine(res.Stderr)
		return st, nil
	}
	st.DaemonUp = true
	st.DaemonVersion = trimLine(res.Stdout)
	return st, nil
}

// Start has nothing to start. Reporting success is honest: the caller asked
// for the daemon to be usable, and it either already is or Probe said why.
func (d *Driver) Start(ctx context.Context) error {
	st, err := d.Probe(ctx)
	if err != nil {
		return err
	}
	if !st.DaemonUp {
		return fmt.Errorf("docker: %s", firstOr(st.Detail, "the daemon is not answering"))
	}
	return nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

func trimLine(s string) string {
	return firstLine(s)
}

func firstOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
