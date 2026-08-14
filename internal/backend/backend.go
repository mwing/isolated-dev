// Package backend abstracts the container engine behind one interface so
// that OrbStack, plain docker and (later) Apple's container tooling are
// swappable without touching command code.
package backend

import (
	"context"
	"fmt"
	"io"

	"github.com/mwing/isolated-dev/internal/runner"
)

// Call is a docker CLI invocation, expressed as an argument vector. The
// backend decides how to transport it (through an OrbStack VM, to a local
// daemon, ...).
type Call struct {
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// PTY attaches the call to a pseudo-terminal, for workloads that need
	// to believe they are talking to a terminal.
	PTY *runner.PTY
}

// Status is what `doctor` reports about a backend.
type Status struct {
	Backend  string
	CLIFound bool
	// CLIName is the command this backend drives, e.g. "orb" or "docker".
	// Reported rather than assumed: doctor used to label whatever it found
	// "orb CLI", so the docker backend claimed an orb it was not using.
	CLIName       string
	CLIPath       string
	VMName        string
	VMExists      bool
	VMRunning     bool
	DaemonUp      bool
	DaemonVersion string
	// Rootless reports a daemon running as an ordinary user. It changes
	// what a uid means: container uid 0 maps to the host user and every
	// other uid maps into a subuid range, so the uid a workload runs as is
	// not the uid its files land under. Reported rather than accommodated —
	// see doctor.
	Rootless bool
	// Detail carries a backend-specific explanation when something is off.
	Detail string
}

// Ready reports whether the backend can run containers right now.
func (s Status) Ready() bool { return s.CLIFound && s.DaemonUp }

// Backend is the container engine abstraction.
//
// M0 defines the probe and the raw docker passthrough, which is everything
// `doctor` needs and the substrate the rest is built on. Build/Run/Exec/
// ImageExists/Save/NetworkCreate/VolumeCreate land in M1-M2 on top of
// Docker(), taking a typed RunSpec rather than strings.
type Backend interface {
	// Name identifies the driver, e.g. "orbstack".
	Name() string
	// Probe reports whether the backend is usable, without changing state.
	Probe(ctx context.Context) (Status, error)
	// Docker runs a docker CLI invocation through this backend.
	Docker(ctx context.Context, call Call) (runner.Result, error)
}

// ErrUnavailable indicates a backend cannot be used on this machine.
type ErrUnavailable struct {
	Backend string
	Reason  string
}

func (e *ErrUnavailable) Error() string {
	return fmt.Sprintf("backend %s unavailable: %s", e.Backend, e.Reason)
}
