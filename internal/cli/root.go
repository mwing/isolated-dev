// Package cli builds the dev command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
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
// DEV_BACKEND names one outright. Otherwise: OrbStack on macOS, and a
// local daemon anywhere else, because anywhere else is where one is.
//
// Linux used to get the OrbStack driver and fail with "orb CLI not on
// PATH; install OrbStack from https://orbstack.dev" — advice for a
// different operating system, on a machine that already had a working
// daemon. Choosing by platform is not a heuristic here; OrbStack does not
// run on Linux at all.
//
// The environment variable stays, and stays first: it is how the
// integration tests reach a daemon on a mac, and how someone on macOS who
// uses Docker Desktop selects it.
func (e *Env) driver(vmName string) backend.Backend {
	switch lookupEnv(e.Env, "DEV_BACKEND") {
	case "docker":
		return e.dockerDriver()
	case "orbstack":
		return e.orbstackDriver(vmName)
	}
	if runtime.GOOS != "darwin" {
		return e.dockerDriver()
	}
	return e.orbstackDriver(vmName)
}

func (e *Env) dockerDriver() backend.Backend {
	d := docker.New(e.Runner)
	if e.LookPath != nil {
		d.LookPath = e.LookPath
	}
	return d
}

func (e *Env) orbstackDriver(vmName string) backend.Backend {
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

	// Grouped rather than listed. Twenty-nine commands in one alphabetical
	// column read as a development platform, which is the wrong first
	// impression for a tool whose job is "run this project safely" — and it
	// buries the three or four commands most people ever need under the
	// twenty-five they do not.
	//
	// The titles are the ones docs/COMMANDS.md uses, deliberately: someone
	// moving between the reference and `--help` should not have to learn the
	// vocabulary twice.
	root.AddGroup(
		&cobra.Group{ID: groupStart, Title: "Starting out"},
		&cobra.Group{ID: groupRun, Title: "Running things"},
		&cobra.Group{ID: groupAgents, Title: "Agents"},
		&cobra.Group{ID: groupTrust, Title: "Egress and trust"},
		&cobra.Group{ID: groupEnv, Title: "The environment"},
		&cobra.Group{ID: groupClones, Title: "Private clones"},
		&cobra.Group{ID: groupMachine, Title: "This machine"},
	)

	root.AddCommand(
		inGroup(groupStart, newInteractiveCmd(env)),
		inGroup(groupStart, newNewCmd(env)),
		inGroup(groupStart, newDoctorCmd(env)),
		inGroup(groupStart, newMigrateCmd(env)),

		inGroup(groupRun, newRunCmd(env)),
		inGroup(groupRun, newShellCmd(env)),
		inGroup(groupRun, newConsoleCmd(env)),
		inGroup(groupRun, newBuildCmd(env)),
		inGroup(groupRun, newCleanCmd(env)),

		inGroup(groupAgents, newAgentCmd(env)),

		// Egress grants and the file that records them. They belong to the
		// project, not to agents: a plain `dev run` consumes the same grants,
		// so `dev agent allow` named the wrong owner. The old spellings
		// survive as hidden aliases under `dev agent`.
		inGroup(groupTrust, newAcceptCmd(env)),
		inGroup(groupTrust, newAllowCmd(env)),
		inGroup(groupTrust, newRevokeCmd(env)),
		inGroup(groupTrust, newGrantsCmd(env)),
		inGroup(groupTrust, newHistoryCmd(env)),
		inGroup(groupTrust, newPolicyCmd(env)),
		inGroup(groupTrust, newStatusCmd(env)),
		inGroup(groupTrust, newConfigCmd(env)),

		inGroup(groupEnv, newToolsCmd(env)),
		inGroup(groupEnv, newPinCmd(env)),
		inGroup(groupEnv, newScanCmd(env)),
		inGroup(groupEnv, newUpdateCmd(env)),
		inGroup(groupEnv, newDevcontainerCmd(env)),
		inGroup(groupEnv, newLanguagesCmd(env)),

		inGroup(groupClones, newCloneCmd(env)),

		inGroup(groupMachine, newVersionCmd(env)),
		inGroup(groupMachine, newVMCmd(env)),
	)

	// Completion covers every command in the tree, so this has to run
	// after the tree is built.
	registerCompletions(root)
	// cobra creates the completion command during Execute, which is too
	// late to hang a subcommand off it.
	root.InitDefaultCompletionCmd()
	if c, _, err := root.Find([]string{"completion"}); err == nil && c != nil {
		c.AddCommand(newCompletionInstallCmd(env))
		c.GroupID = groupStart
	}
	// help is about the tool rather than about a project, and cobra would
	// otherwise file it under "Additional Commands" on its own.
	root.SetHelpCommandGroupID(groupMachine)
	return root
}

// Command groups for the root help.
const (
	groupStart   = "start"
	groupRun     = "run"
	groupAgents  = "agents"
	groupTrust   = "trust"
	groupEnv     = "env"
	groupClones  = "clones"
	groupMachine = "machine"
)

// inGroup files a command under a heading in the root help.
func inGroup(id string, cmd *cobra.Command) *cobra.Command {
	cmd.GroupID = id
	return cmd
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
