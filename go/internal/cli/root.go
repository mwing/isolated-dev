// Package cli builds the dev2 command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/backend/orbstack"
	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/runner"
)

// Env carries process-level dependencies so commands stay testable: no
// command reads os.Args, os.Stdout or the filesystem directly.
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
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

// driver builds the container backend for a VM, carrying the injected PATH
// lookup so every command probes the host the same way.
func (e *Env) driver(vmName string) *orbstack.Driver {
	d := orbstack.New(vmName, e.Runner)
	if e.LookPath != nil {
		d.LookPath = e.LookPath
	}
	return d
}

// NewRootCmd builds the command tree against env.
func NewRootCmd(env *Env) *cobra.Command {
	root := &cobra.Command{
		Use:   "dev2",
		Short: "Isolated development environments (v2)",
		Long: "dev2 is the Go implementation of isolated-dev.\n" +
			"During the transition it ships alongside the bash v1 `dev`.",
		SilenceUsage:  true,
		SilenceErrors: true,
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
	root.AddCommand(newMigrateCmd(env))
	root.AddCommand(newConsoleCmd(env))
	root.AddCommand(newToolsCmd(env))
	root.AddCommand(newPinCmd(env))
	root.AddCommand(newScanCmd(env))
	root.AddCommand(newPolicyCmd(env))
	root.AddCommand(newUpdateCmd(env))
	root.AddCommand(newNewCmd(env))
	root.AddCommand(newDevcontainerCmd(env))

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
		fmt.Fprintln(os.Stderr, "dev2:", err)
		return 1
	}
	env := &Env{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Env:    os.Environ(),
		Paths:  paths,
		Runner: runner.New(false),
	}
	root := NewRootCmd(env)
	root.SetArgs(args)
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "dev2:", err)
		return 1
	}
	return 0
}
