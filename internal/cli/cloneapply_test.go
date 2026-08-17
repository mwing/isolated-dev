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
