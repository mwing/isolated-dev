package clone

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/runner"
)

// Invariants, tested adversarially rather than as features.
//
// The dangerous defects in this package have been composition failures:
// two individually-safe mechanisms meeting. Reuse met branch changes,
// captures met wildcard semantics, a lossless fetch met non-unique names,
// a sandboxed clone met host-side git. Every one violated a property
// nobody had written down, and none violated a feature test — including
// the ones introduced while fixing another.
//
// So these name the properties instead, and try to break them. A failure
// here is a real defect by construction: there is no feature to argue
// about, only a promise that did or did not hold.

// INVARIANT: an agent never receives git objects unrelated to the history
// it starts from.
//
// A clone is meant to hand over the commit graph the run needs. `git clone
// --no-hardlinks <path>` is git's local mode, which copies the whole
// object database — so every other branch, every tag and every unreachable
// blob comes with it, readable by sha even when no ref names them.
func TestInvariantCloneCarriesNoUnrelatedObjects(t *testing.T) {
	src := gitRepo(t)
	run := runner.New(false)
	ctx := context.Background()

	// Work on another branch that the run has no business seeing.
	if _, err := git(ctx, run, src, "checkout", "-q", "-b", "secret-branch"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "secret.txt"), "SECRET-PAYLOAD\n")
	if _, err := git(ctx, run, src, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, run, src, "commit", "-qm", "secret work"); err != nil {
		t.Fatal(err)
	}
	secret, err := gitOutput(ctx, run, src, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	secret = strings.TrimSpace(secret)
	if _, err := git(ctx, run, src, "checkout", "-q", "main"); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "clone")
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	// Refs are the visible half and are not the question: the object store
	// is what an agent can enumerate with plumbing.
	if _, err := cloneGit(ctx, run, dest, "cat-file", "-e", secret+"^{commit}"); err == nil {
		body, _ := cloneGitOutput(ctx, run, dest, "show", secret+":secret.txt")
		t.Fatalf("the clone carries a commit from another branch: %s\n%s", secret, body)
	}
}

// INVARIANT: no successful operation makes previously captured work
// unreachable.
//
// Captures exist so that work survives a crash, a forgotten clone or a
// person who never got round to applying. A capture that can be
// overwritten is a capture that can lose the thing it was made to keep —
// and the ids are generated per second while the fetch uses --force, so
// two runs a second apart address the same ref.
func TestInvariantACaptureNeverLosesEarlierWork(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	commit := func(name string) string {
		write(t, filepath.Join(dest, name), name+"\n")
		if _, err := git(ctx, run, dest, "add", "-A"); err != nil {
			t.Fatal(err)
		}
		if _, err := git(ctx, run, dest, "commit", "-qm", name); err != nil {
			t.Fatal(err)
		}
		out, err := gitOutput(ctx, run, dest, "rev-parse", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}

	first := commit("run-one-work")
	if _, err := Capture(ctx, run, src, dest, "same-second"); err != nil {
		t.Fatal(err)
	}

	// A second run whose history does not contain the first's — what
	// happens after a clone is discarded and remade, and what part 2's
	// refresh will produce by design.
	if _, err := git(ctx, run, dest, "reset", "-q", "--hard", "HEAD~1"); err != nil {
		t.Fatal(err)
	}
	commit("run-two-work")
	if _, err := Capture(ctx, run, src, dest, "same-second"); err != nil {
		t.Fatal(err)
	}

	// The first run's commit must still be reachable from something.
	out, err := gitOutput(ctx, run, src, "for-each-ref", "--format=%(objectname)", "--contains", first)
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("the first run's work is unreachable after a second capture: %s", first)
	}
}

// INVARIANT: an agent can never modify the source repository.
//
// The one this package exists for. Asserted against the object store
// rather than the working tree, because a shared object file is the way it
// would actually happen.
func TestInvariantTheSourceIsNeverModified(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	before, _ := gitOutput(ctx, run, src, "rev-parse", "HEAD")

	// Everything an agent might do to its own repository.
	write(t, filepath.Join(dest, "f.txt"), "rewritten\n")
	for _, args := range [][]string{
		{"add", "-A"}, {"commit", "-qm", "agent work"},
		{"gc", "--prune=now", "--quiet"},
		{"update-ref", "refs/heads/main", "HEAD"},
	} {
		_, _ = git(ctx, run, dest, args...)
	}

	after, _ := gitOutput(ctx, run, src, "rev-parse", "HEAD")
	if after != before {
		t.Fatalf("the source moved: %s -> %s", before, after)
	}
	if out, err := gitOutput(ctx, run, src, "fsck", "--no-progress"); err != nil {
		t.Fatalf("the source repository is damaged: %v\n%s", err, out)
	}
	if body := read(t, filepath.Join(src, "kept.txt")); body != "committed\n" {
		t.Fatalf("a file in the source changed: %q", body)
	}
}
