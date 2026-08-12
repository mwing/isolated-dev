// Package cli builds the dev command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/backend"
	"github.com/mwing/isolated-dev/internal/backend/docker"
	"github.com/mwing/isolated-dev/internal/backend/orbstack"
	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/runner"
)

// Env carries process-level dependencies so commands stay testable: no
// command reads os.Args, os.Stdout or the filesystem directly.
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	// Stdin is the process's standard input, nil when there is none to give.
	// Whether it is a terminal decides whether a blocking prompt can be
	// asked and whether a container is given a keyboard, so it is injected
	// rather than read from os: `go test` hands the process /dev/null, which
	// is a character device and so reads as a terminal to the naive check.
	Stdin  *os.File
	Env    []string
	Paths  config.Paths
	Runner runner.Runner
	// LookPath resolves a binary on PATH. Injected so tests do not depend
	// on whether the host running them happens to have OrbStack.
	LookPath func(string) (string, bool)

	verbose bool
}

// Verbose reports whether --verbose was given.
func (e *Env) Verbose() bool { return e.verbose }

// exitStatus carries a workload's exit code out through the error path.
//
// Without it every failure was exit 1, so `dev run -- make test` could not
// be a step in a script or a CI job: the thing being sandboxed had a status
// and the sandbox threw it away. The message is still printed — the code is
// for the machine, the sentence is for the person.
type exitStatus struct {
	// What names the thing that exited, empty for the workload itself.
	What string
	Code int
}

func (e *exitStatus) Error() string {
	if e.What == "" {
		return fmt.Sprintf("exited with status %d", e.Code)
	}
	return fmt.Sprintf("%s exited with status %d", e.What, e.Code)
}

// exitCode maps an error to a process status. A status outside 1..255 is
// not one the OS can carry, and a workload that exited 0 does not reach
// here, so anything unusable becomes a plain failure rather than an
// accidental success.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var status *exitStatus
	if errors.As(err, &status) && status.Code > 0 && status.Code < 256 {
		return status.Code
	}
	return 1
}

// stdin returns the reader to attach to a container, or nil when there is
// nothing to attach. A typed nil *os.File would be a non-nil io.Reader that
// fails on the first read, which is worse than no stdin at all.
func (e *Env) stdin() io.Reader {
	if e.Stdin == nil {
		return nil
	}
	return e.Stdin
}

// stdinIsTerminal reports whether standard input is an interactive terminal,
// which is what makes asking the user a question possible.
func (e *Env) stdinIsTerminal() bool { return isTerminal(e.Stdin) }

// driver returns the container backend, carrying the injected PATH lookup
// so every command probes the host the same way.
//
// OrbStack unless told otherwise. DEV_BACKEND=docker selects a local
// daemon, which is what a Linux machine has and what the integration tests
// need — they cannot reach a VM that is not there, and a test tier that
// cannot reach a daemon is the gap that let every bug in the review through.
//
// An environment variable rather than a config key, for now: this selects
// where the tests run, and a setting in ~/.dev-envs that silently changes
// which daemon a run uses is a bigger promise than the docker backend has
// earned.
func (e *Env) driver(vmName string) backend.Backend {
	if lookupEnv(e.Env, "DEV_BACKEND") == "docker" {
		d := docker.New(e.Runner)
		if e.LookPath != nil {
			d.LookPath = e.LookPath
		}
		return d
	}
	d := orbstack.New(vmName, e.Runner)
	if e.LookPath != nil {
		d.LookPath = e.LookPath
	}
	return d
}

// NewRootCmd builds the command tree against env.
func NewRootCmd(env *Env) *cobra.Command {
	root := &cobra.Command{
		Use:   "dev",
		Short: "Isolated development environments",
		Long: "Run code in a container without learning Docker, in a sandbox that\n" +
			"is closed by default and opened one deliberate step at a time.\n\n" +
			"Run `dev` on its own inside a project for the guided view: what is\n" +
			"set up, what is not, and what each next step would do.\n\n" +
			"The bash tool this replaces installs itself as `dev1` and stays\n" +
			"available; `dev migrate` says what changes.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Without this cobra hands an unrecognized command to the root's own
		// RunE as an argument, so `dev buld` would open the menu instead of
		// saying that `buld` is not a command.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFrontDoor(cmd, env)
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			v, err := cmd.Flags().GetBool("verbose")
			if err != nil {
				return err
			}
			env.verbose = v
			if r, ok := env.Runner.(*runner.Exec); ok {
				r.Verbose = v
				r.Log = env.Stderr
			}
			return nil
		},
	}
	root.PersistentFlags().BoolP("verbose", "v", false,
		"print every external command before running it")

	root.AddCommand(newVersionCmd(env))
	root.AddCommand(newDoctorCmd(env))
	root.AddCommand(newAgentCmd(env))
	root.AddCommand(newBuildCmd(env))
	root.AddCommand(newRunCmd(env))
	root.AddCommand(newShellCmd(env))
	root.AddCommand(newStatusCmd(env))
	root.AddCommand(newAcceptCmd(env))
	// Egress grants and the file that records them. They belong to the
	// project, not to agents: a plain `dev run` consumes the same grants, so
	// `dev agent allow` named the wrong owner. The old spellings survive as
	// hidden aliases under `dev agent`.
	root.AddCommand(newAllowCmd(env))
	root.AddCommand(newRevokeCmd(env))
	root.AddCommand(newGrantsCmd(env))
	root.AddCommand(newConfigCmd(env))
	root.AddCommand(newMigrateCmd(env))
	root.AddCommand(newConsoleCmd(env))
	root.AddCommand(newToolsCmd(env))
	root.AddCommand(newPinCmd(env))
	root.AddCommand(newScanCmd(env))
	root.AddCommand(newPolicyCmd(env))
	root.AddCommand(newUpdateCmd(env))
	root.AddCommand(newNewCmd(env))
	root.AddCommand(newDevcontainerCmd(env))
	root.AddCommand(newHistoryCmd(env))
	root.AddCommand(newCloneCmd(env))
	root.AddCommand(newInteractiveCmd(env))
	root.AddCommand(newLanguagesCmd(env))

	// Completion covers every command in the tree, so this has to run
	// after the tree is built.
	registerCompletions(root)
	// cobra creates the completion command during Execute, which is too
	// late to hang a subcommand off it.
	root.InitDefaultCompletionCmd()
	if c, _, err := root.Find([]string{"completion"}); err == nil && c != nil {
		c.AddCommand(newCompletionInstallCmd(env))
	}
	root.AddCommand(newCleanCmd(env))
	root.AddCommand(newEnvCmd(env))
	return root
}

// Main wires the real process environment and runs the tree. It returns a
// process exit code.
//
// Interrupts cancel the context rather than killing the process outright,
// so cleanup runs: a console leaves a container behind otherwise, since
// `docker run` attaches to a container rather than owning it.
func Main(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, args)
}

func run(ctx context.Context, args []string) int {
	paths, err := config.Discover()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dev:", err)
		return 1
	}
	env := &Env{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Env:    os.Environ(),
		Paths:  paths,
		Runner: runner.New(false),
	}
	root := NewRootCmd(env)
	root.SetArgs(args)
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "dev:", err)
		return exitCode(err)
	}
	return 0
}
