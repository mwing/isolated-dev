package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/clone"
	"github.com/mwing/isolated-dev/internal/runner"
)

// cloneAt prepares the clone where applyClone will look for it.
func cloneAt(t *testing.T, h *harness, project string) string {
	t.Helper()
	dest := clone.Dir(h.paths.Home, projectSlug(project))
	if _, err := clone.Prepare(context.Background(), runner.New(false),
		clone.Options{Project: project, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	return dest
}

// commitIn makes one commit in dir, so a test can move a history.
func commitIn(t *testing.T, dir, file, body, msg string) {
	t.Helper()
	run := runner.New(false)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", msg}} {
		if _, err := run.Run(ctx, runner.Command{Path: "git", Args: args, Dir: dir}); err != nil {
			t.Fatal(err)
		}
	}
}

// Reported from a real session: "I assumed dev clone apply would bring the
// work to the current branch, not clone-work."
//
// The command is called apply, so a report that commits were "fetched onto
// clone-work" reads as an implementation detail while the user looks at an
// unchanged working tree and concludes it did nothing. What happened to
// *their* branch is the question they asked, so it goes first.
func TestApplyOnDivergedHistoriesSaysTheBranchIsUnchanged(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	project := h.paths.ProjectDir

	run := runner.New(false)
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
	} {
		if _, err := run.Run(ctx, runner.Command{Path: "git", Args: args, Dir: project}); err != nil {
			t.Fatal(err)
		}
	}
	commitIn(t, project, "base.txt", "base\n", "base")

	// A clone of it, then both sides move: that is what makes this a
	// decision rather than a fast-forward.
	clonePath := cloneAt(t, h, project)
	commitIn(t, clonePath, "from-clone.txt", "clone\n", "work in the clone")
	commitIn(t, project, "from-host.txt", "host\n", "work on the host")

	if err := applyClone(ctx, h.env, "clone-work"); err != nil {
		t.Fatalf("apply: %v\n%s", err, h.stderr.String())
	}

	out := h.stdout.String()
	if !strings.Contains(out, "Your branch is unchanged") {
		t.Errorf("apply does not say what happened to the branch the user is on:\n%s", out)
	}
	// And it names a way to look before choosing, plus the single-commit
	// case, which is the common one after an agent run.
	for _, want := range []string{"git log --oneline", "cherry-pick", "git merge", "git rebase"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q offered:\n%s", want, out)
		}
	}
}

// The uncommitted note states the contract rather than reading as a
// shortfall: apply moves commits, and committing is the act that says
// "this is work".
func TestApplyStatesThatUncommittedWorkStaysPut(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	project := h.paths.ProjectDir

	run := runner.New(false)
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
	} {
		if _, err := run.Run(ctx, runner.Command{Path: "git", Args: args, Dir: project}); err != nil {
			t.Fatal(err)
		}
	}
	commitIn(t, project, "base.txt", "base\n", "base")

	clonePath := cloneAt(t, h, project)
	if err := os.WriteFile(filepath.Join(clonePath, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_ = applyClone(ctx, h.env, "clone-work")

	got := h.stderr.String()
	if !strings.Contains(got, "stay in the clone") {
		t.Errorf("the note does not say where the work is:\n%s", got)
	}
	if strings.Contains(got, "by hand") {
		t.Errorf("the note still reads as a shortfall:\n%s", got)
	}
}

// The review's first finding: `dev clone diff` and `dev clone apply` ran
// git against the clone without the hardening internal/clone had gained,
// so a clone's own config still named programs that ran on the host —
// `apply` especially, because fetching from a clone starts upload-pack
// inside it.
func TestCloneCommandsRunNoProgramTheCloneNamed(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	project := h.paths.ProjectDir

	run := runner.New(false)
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"}, {"config", "commit.gpgsign", "false"},
	} {
		if _, err := run.Run(ctx, runner.Command{Path: "git", Args: args, Dir: project}); err != nil {
			t.Fatal(err)
		}
	}
	commitIn(t, project, "base.txt", "base\n", "base")
	clonePath := cloneAt(t, h, project)
	commitIn(t, clonePath, "work.txt", "work\n", "work in the clone")

	marker := "CLI-PAYLOAD-RAN"
	script := filepath.Join(clonePath, "pwn.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{
		{"core.fsmonitor", "./pwn.sh"},
		{"core.pager", "./pwn.sh"},
		{"uploadpack.packObjectsHook", "./pwn.sh"},
		{"filter.whatever.clean", "./pwn.sh"},
	} {
		if _, err := run.Run(ctx, runner.Command{Path: "git", Dir: clonePath,
			Args: []string{"config", kv[0], kv[1]}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(clonePath, ".gitattributes"),
		[]byte("* filter=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_ = h.run(t, "clone", "diff")
	_ = applyClone(ctx, h.env, "clone-work")

	all := h.stdout.String() + h.stderr.String()
	if strings.Contains(all, marker) {
		t.Fatalf("a program named by the clone ran on the host:\n%s", all)
	}
	// The commands still have to work, or the hardening has bought safety
	// by breaking what it protects.
	if !strings.Contains(all, "work in the clone") && !strings.Contains(all, "Fast-forwarded") {
		t.Errorf("neither command reported the clone's commit:\n%s", all)
	}
}

// Captures accumulate silently by design — nothing the user owns moves
// when one is made, which is what makes them safe and also what makes them
// easy to forget. A run says what is waiting, at the point it can still be
// dealt with cheaply.
func TestARunSaysWhenEarlierWorkIsUnapplied(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	project := h.paths.ProjectDir

	run := runner.New(false)
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"}, {"config", "commit.gpgsign", "false"},
	} {
		if _, err := run.Run(ctx, runner.Command{Path: "git", Args: args, Dir: project}); err != nil {
			t.Fatal(err)
		}
	}
	commitIn(t, project, "base.txt", "base\n", "base")
	clonePath := cloneAt(t, h, project)
	commitIn(t, clonePath, "work.txt", "work\n", "work from an earlier run")

	if _, err := clone.Capture(ctx, h.env.Runner, project, clonePath, "earlier"); err != nil {
		t.Fatal(err)
	}

	// Starting another run is where it has to be said.
	if _, release, err := prepareCloneDir(ctx, h.env, project, 0, h.stderr); err != nil {
		t.Fatalf("prepare: %v\n%s", err, h.stderr.String())
	} else {
		release()
	}
	got := h.stderr.String()
	if !strings.Contains(got, "not on") || !strings.Contains(got, "dev clone apply") {
		t.Errorf("a run did not mention unapplied work:\n%s", got)
	}
}

// INVARIANT: no successful operation makes previously captured work
// unreachable — including on a detached HEAD.
//
// CurrentBranch returns "" when HEAD is detached, and CapturedRefs treats
// "" as "every branch". One sentinel means both "the branch I am on" and
// "all of them", so an ordinary `apply` from a detached HEAD sweeps and
// drops captures belonging to branches it never looked at.
func TestInvariantApplyOnDetachedHeadKeepsOtherBranchesWork(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	project := h.paths.ProjectDir
	run := runner.New(false)
	ctx := context.Background()

	for _, args := range [][]string{
		{"init", "-q"}, {"symbolic-ref", "HEAD", "refs/heads/main"},
		{"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := run.Run(ctx, runner.Command{Path: "git", Args: args, Dir: project}); err != nil {
			t.Fatal(err)
		}
	}
	commitIn(t, project, "base.txt", "base\n", "base")

	// Work captured from a run on another branch.
	clonePath := cloneAt(t, h, project)
	commitIn(t, clonePath, "work.txt", "work\n", "work from a run")
	got, err := clone.Capture(ctx, h.env.Runner, project, clonePath, "earlier")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref == "" {
		t.Fatal("nothing was captured, so the test proves nothing")
	}

	// The user detaches — a bisect, a tag checkout, an ordinary look at an
	// old commit.
	head, err := run.Run(ctx, runner.Command{Path: "git", Dir: project,
		Args: []string{"rev-parse", "HEAD"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Run(ctx, runner.Command{Path: "git", Dir: project,
		Args: []string{"checkout", "-q", "--detach", strings.TrimSpace(head.Stdout)}}); err != nil {
		t.Fatal(err)
	}

	_ = applyClone(ctx, h.env, "clone-work")

	// The capture must still be there: apply was run from somewhere that
	// has nothing to do with it.
	refs, err := clone.AllCapturedRefs(ctx, h.env.Runner, project)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		if r == got.Ref {
			return
		}
	}
	t.Fatalf("apply from a detached HEAD dropped another branch's capture %s; left: %v",
		got.Ref, refs)
}
