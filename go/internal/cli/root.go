// Package cli builds the dev2 command tree.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

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

	verbose bool
}

// Verbose reports whether --verbose was given.
func (e *Env) Verbose() bool { return e.verbose }

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
	return root
}

// Main wires the real process environment and runs the tree. It returns a
// process exit code.
func Main(args []string) int {
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

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "dev2:", err)
		return 1
	}
	return 0
}
