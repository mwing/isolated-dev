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

	"github.com/creack/pty"
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

	// PTY attaches the command to a pseudo-terminal instead of pipes.
	// Interactive programs behave differently without one: a shell will
	// not draw a prompt, and anything that checks isatty takes its
	// non-interactive path.
	PTY *PTY
}

// PTY configures a pseudo-terminal for a command.
type PTY struct {
	Rows, Cols uint16
	// Ready receives the master side once the command has started, so the
	// caller can write keystrokes to it and resize it. It is called from
	// the goroutine that started the process.
	Ready func(*os.File)
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

	if cmd.PTY != nil {
		return e.runPTY(ctx, c, cmd)
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

// runPTY runs the command attached to a pseudo-terminal. Output is copied
// to Stdout; a pty has no separate error stream, which is exactly what an
// interactive program expects.
func (e *Exec) runPTY(ctx context.Context, c *exec.Cmd, cmd Command) (Result, error) {
	size := &pty.Winsize{Rows: cmd.PTY.Rows, Cols: cmd.PTY.Cols}
	if size.Rows == 0 {
		size.Rows = 24
	}
	if size.Cols == 0 {
		size.Cols = 80
	}

	ptmx, err := pty.StartWithSize(c, size)
	if err != nil {
		return Result{}, fmt.Errorf("runner: %s: pty: %w", cmd.Path, err)
	}
	defer func() { _ = ptmx.Close() }()

	if cmd.PTY.Ready != nil {
		cmd.PTY.Ready(ptmx)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if cmd.Stdout != nil {
			_, _ = io.Copy(cmd.Stdout, ptmx)
		}
	}()

	// Killing the process on cancellation is what makes a console able to
	// stop a shell that is waiting for input.
	go func() {
		<-ctx.Done()
		_ = ptmx.Close()
	}()

	err = c.Wait()
	<-done

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return Result{}, nil
	case errors.As(err, &exitErr):
		return Result{ExitCode: exitErr.ExitCode()}, nil
	default:
		return Result{}, nil
	}
}

// LookPath reports whether a binary is on PATH.
func LookPath(bin string) (string, bool) {
	p, err := exec.LookPath(bin)
	if err != nil {
		return "", false
	}
	return p, true
}
