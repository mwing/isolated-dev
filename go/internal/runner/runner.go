// Package runner is the single choke point for every external process the
// tool spawns. Nothing in the codebase may call os/exec directly: routing
// everything through one interface is what makes commands mockable in tests
// and printable under --verbose.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Command is a fully-resolved external command. Args is an argument vector,
// never a string to be re-split by a shell; this is the property that makes
// the v1 quoting bug class unrepresentable.
type Command struct {
	Path string
	Args []string
	Env  []string
	Dir  string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Result carries the outcome. Stdout/Stderr are populated only for streams
// the caller did not redirect.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// String renders the command the way a user could paste it back into a
// shell. Used by --verbose and by test assertions.
func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, shellQuote(c.Path))
	for _, a := range c.Args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`*?[]{}()<>|&;#~=!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Runner executes commands.
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// Exec is the real runner.
type Exec struct {
	// Verbose prints each command to Log before running it.
	Verbose bool
	Log     io.Writer
}

// New returns an Exec runner logging to stderr.
func New(verbose bool) *Exec {
	return &Exec{Verbose: verbose, Log: os.Stderr}
}

// Run executes cmd. A non-zero exit status is reported in Result.ExitCode
// and is NOT an error: only failure to run the process at all is. Callers
// decide whether an exit code matters, which is what lets `doctor` probe
// things that are expected to fail.
func (e *Exec) Run(ctx context.Context, cmd Command) (Result, error) {
	if cmd.Path == "" {
		return Result{}, errors.New("runner: empty command path")
	}
	if e.Verbose {
		log := e.Log
		if log == nil {
			log = os.Stderr
		}
		fmt.Fprintf(log, "+ %s\n", cmd.String())
	}

	c := exec.CommandContext(ctx, cmd.Path, cmd.Args...)
	c.Dir = cmd.Dir
	c.Stdin = cmd.Stdin
	if cmd.Env != nil {
		c.Env = cmd.Env
	}

	var outBuf, errBuf bytes.Buffer
	if cmd.Stdout != nil {
		c.Stdout = cmd.Stdout
	} else {
		c.Stdout = &outBuf
	}
	if cmd.Stderr != nil {
		c.Stderr = cmd.Stderr
	} else {
		c.Stderr = &errBuf
	}

	err := c.Run()
	res := Result{Stdout: outBuf.String(), Stderr: errBuf.String()}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, fmt.Errorf("runner: %s: %w", cmd.Path, err)
	}
	return res, nil
}

// LookPath reports whether a binary is on PATH.
func LookPath(bin string) (string, bool) {
	p, err := exec.LookPath(bin)
	if err != nil {
		return "", false
	}
	return p, true
}
