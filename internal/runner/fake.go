package runner

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// Fake is a Runner for tests. It records every command and replies from a
// table of responses keyed by a prefix of the rendered command line.
type Fake struct {
	mu       sync.Mutex
	Calls    []Command
	Response map[string]Result
	// Default is returned when no Response key matches.
	Default Result
	// Err, when set for a matching key, is returned as the run error.
	Err map[string]error
}

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{Response: map[string]Result{}, Err: map[string]error{}}
}

// Run records the command and returns the configured response.
func (f *Fake) Run(_ context.Context, cmd Command) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, cmd)

	line := cmd.String()
	if k, ok := bestKey(line, f.Err); ok {
		return Result{}, f.Err[k]
	}
	if k, ok := bestKey(line, f.Response); ok {
		return deliver(cmd, f.Response[k]), nil
	}
	return deliver(cmd, f.Default), nil
}

// bestKey returns the longest key that prefixes line.
//
// Longest rather than any: one name is often a prefix of another —
// `dev-agent-claude` of `dev-agent-claude-config` — so a map iterated in
// random order answered a question about the second with the response
// registered for the first, roughly half the time. A test that passes on
// alternate runs is worse than one that fails.
func bestKey[T any](line string, table map[string]T) (string, bool) {
	best, found := "", false
	for k := range table {
		if matches(line, k) && (!found || len(k) > len(best)) {
			best, found = k, true
		}
	}
	return best, found
}

// deliver routes a canned response the way Exec routes a real one: output
// goes to the writer the caller supplied, and Result carries it only when
// there was nowhere else to put it.
//
// Without this the fake answered a caller that reads Result.Stdout and
// silently gave nothing to one that passed a writer — so any code path
// capturing a container's output looked like it produced none, which is
// indistinguishable from a command that genuinely printed nothing.
func deliver(cmd Command, res Result) Result {
	if cmd.Stdout != nil && res.Stdout != "" {
		_, _ = io.WriteString(cmd.Stdout, res.Stdout)
		res.Stdout = ""
	}
	if cmd.Stderr != nil && res.Stderr != "" {
		_, _ = io.WriteString(cmd.Stderr, res.Stderr)
		res.Stderr = ""
	}
	return res
}

func matches(line, key string) bool {
	return len(key) <= len(line) && line[:len(key)] == key
}

// Snapshot returns a copy of the recorded commands.
//
// Read this rather than Calls directly: a command can leave a goroutine
// behind that is still following a container's logs, so the slice may still
// be growing while a test inspects it.
func (f *Fake) Snapshot() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Command(nil), f.Calls...)
}

// Lines returns every recorded command as a rendered string.
func (f *Fake) Lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.Calls))
	for _, c := range f.Calls {
		out = append(out, c.String())
	}
	return out
}

// Last returns the most recent command, or an error if none were run.
func (f *Fake) Last() (Command, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Calls) == 0 {
		return Command{}, fmt.Errorf("fake runner: no commands recorded")
	}
	return f.Calls[len(f.Calls)-1], nil
}
