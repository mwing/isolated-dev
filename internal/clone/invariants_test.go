package clone

import (
	"context"
	"os"
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

// INVARIANT: two simultaneous runs can never write the same git working
// tree.
//
// clone.Dir is one mutable directory per project and nothing serializes
// access to it, so two `dev agent run`s against one repository mount the
// same tree, index and refs at once. Neither is doing anything wrong; the
// pair is the defect.
func TestInvariantOnlyOneRunHoldsAClone(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	release, err := Lock(dest)
	if err != nil {
		t.Fatalf("first run could not take the clone: %v", err)
	}
	defer release()

	if _, err := Lock(dest); err == nil {
		t.Fatal("a second run took a clone the first is still holding")
	} else if !strings.Contains(err.Error(), "in use") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}

	// And it has to be reusable afterwards, or the lock is a leak.
	release()
	again, err := Lock(dest)
	if err != nil {
		t.Fatalf("the clone stayed locked after the run finished: %v", err)
	}
	again()
}

// INVARIANT: a clone's provenance describes the run it was made for, and
// nothing that happened to the host afterwards.
//
// Reuse re-recorded the host's current branch, which reintroduced one
// level up exactly the bug the capture path was fixed for: the clone is
// still on the branch it was made from, and saying otherwise files its
// work under a branch it never came from.
func TestInvariantReuseDoesNotRewriteProvenance(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()

	if _, err := git(ctx, run, src, "checkout", "-q", "-b", "feature-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	first := ProvenanceOf(dest)
	if first.Branch != "feature-a" {
		t.Fatalf("provenance at creation = %+v", first)
	}

	// The human moves on, then another run reuses the clone.
	if _, err := git(ctx, run, src, "checkout", "-q", "-b", "feature-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	if got := ProvenanceOf(dest); got.Branch != first.Branch || got.Base != first.Base {
		t.Fatalf("reuse rewrote provenance: %+v -> %+v", first, got)
	}
}

// INVARIANT: host git never executes configuration an agent controls.
//
// Stated over the set of keys rather than over the four or five a previous
// test happened to name. Git executes what a dozen settings name, the list
// grows with git itself, and the ones that were tested were the ones
// someone had already thought of — which is the wrong way round for an
// invariant. A key added to git after this was written will not be in the
// table either, but a table is at least a place to add it.
//
// Asserted on a file the payload writes, not on captured output. A payload
// whose stdout is discarded still ran, and "ran" is the thing that must not
// happen.
func TestInvariantHostGitRunsNothingTheCloneNames(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	// Something for the reads to find, so they do real work.
	write(t, filepath.Join(dest, "kept.txt"), "changed by the agent\n")
	if _, err := git(ctx, run, dest, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, run, dest, "commit", "-qm", "agent work"); err != nil {
		t.Fatal(err)
	}

	// The payload records that it ran, in a place no git command writes.
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(dest, "pwn.sh")
	write(t, script, "#!/bin/sh\necho ran > "+marker+"\nexit 1\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}

	// Every setting whose value git runs as a program. Some need a
	// companion in the tree — a filter is chosen by .gitattributes, an
	// alias by being invoked — and those are set up alongside.
	for _, key := range []string{
		"core.fsmonitor",
		"core.hooksPath",
		"core.pager",
		"core.editor",
		"core.sshCommand",
		"core.askPass",
		"core.gitProxy",
		"uploadpack.packObjectsHook",
		"diff.external",
		"gpg.program",
		"credential.helper",
		"sequence.editor",
		"filter.hostile.clean",
		"filter.hostile.smudge",
		"diff.hostile.textconv",
		"merge.hostile.driver",
	} {
		if _, err := git(ctx, run, dest, "config", key, "./pwn.sh"); err != nil {
			t.Fatalf("planting %s: %v", key, err)
		}
	}
	// The attributes file is what makes the filter and textconv drivers
	// reachable: the driver name is the clone's to choose, so there is no
	// key to blank and only quarantining the config closes it.
	write(t, filepath.Join(dest, ".gitattributes"),
		"* filter=hostile diff=hostile merge=hostile\n")

	// Every way host code reads or fetches from a clone.
	reads := map[string]func() error{
		"Read/status": func() error {
			_, err := Read(ctx, run, dest, "status", "--porcelain")
			return err
		},
		"Read/diff": func() error {
			_, err := Read(ctx, run, dest, "diff", "HEAD")
			return err
		},
		"Read/log": func() error {
			_, err := Read(ctx, run, dest, "log", "--oneline", "-1")
			return err
		},
		"State": func() error {
			_, _, _, _ = State(ctx, run, dest)
			return nil
		},
		"driftNotes": func() error {
			_, err := driftNotes(ctx, run, src, dest)
			return err
		},
		"Anomalies": func() error {
			_ = Anomalies(ctx, run, dest)
			return nil
		},
		"Capture": func() error {
			_, err := Capture(ctx, run, src, dest, "invariant")
			return err
		},
		"Prepare/reuse": func() error {
			_, err := Prepare(ctx, run, Options{Project: src, Dest: dest})
			return err
		},
	}
	for name, read := range reads {
		_ = read()
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%s ran a program the clone named", name)
		}
	}
}
