package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/clone"
	"github.com/mwing/isolated-dev/internal/runner"
)

// Getting work out of a clone used to be `dev clone path` and a git
// incantation the user had to compose. That is too much ceremony for the
// safer path: if reviewing an agent's work costs more than letting it edit
// your tree, people will let it edit your tree.
//
// So the round trip is two commands. `diff` shows what is in there, read
// with the project's own tools. `apply` brings it back, and refuses to do
// anything clever with it.

func newCloneDiffCmd(env *Env) *cobra.Command {
	var stat bool
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "What is in this project's clone that is not in the project",
		Long: "Shows the commits the clone has and the project does not, then the\n" +
			"changes not yet committed there.\n\n" +
			"This is the review step. Nothing here has run on your machine: the\n" +
			"clone was mounted into a container, and what you are reading is the\n" +
			"result, on disk, with your own tools.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return diffClone(cmd.Context(), env, stat)
		},
	}
	cmd.Flags().BoolVar(&stat, "stat", false, "summarize by file instead of showing the patch")
	return cmd
}

func diffClone(ctx context.Context, env *Env, stat bool) error {
	path, err := requireClone(env)
	if err != nil {
		return err
	}

	commits, err := cloneGit(ctx, env, path, "log", "--branches", "--not", "--remotes",
		"--oneline")
	if err != nil {
		return err
	}
	if strings.TrimSpace(commits) != "" {
		fmt.Fprintf(env.Stdout, "Commits the project does not have:\n\n%s\n", commits)
	}

	args := []string{"diff", "HEAD"}
	if stat {
		args = append(args, "--stat")
	}
	uncommitted, err := cloneGit(ctx, env, path, args...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(uncommitted) != "" {
		fmt.Fprintf(env.Stdout, "Uncommitted in the clone:\n\n%s\n", uncommitted)
	}

	if strings.TrimSpace(commits) == "" && strings.TrimSpace(uncommitted) == "" {
		fmt.Fprintf(env.Stdout, "Nothing in %s that the project does not have.\n", path)
		return nil
	}
	fmt.Fprintf(env.Stdout, "\nBring it back:  dev clone apply\n")
	return nil
}

func newCloneApplyCmd(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Bring the clone's commits back into this project",
		Long: "Fetches the clone's commits onto a local branch, and fast-forwards\n" +
			"the current branch onto them when that is possible without a\n" +
			"decision.\n\n" +
			"It will not merge, rebase or resolve anything. Where a\n" +
			"fast-forward is not available the branch is left for you and the\n" +
			"command says so: an automatic merge is a decision about someone\n" +
			"else's code, made by a tool that cannot read it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return applyClone(cmd.Context(), env, branch)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "clone-work",
		"local branch to fetch the clone's commits onto")
	return cmd
}

func applyClone(ctx context.Context, env *Env, branch string) error {
	path, err := requireClone(env)
	if err != nil {
		return err
	}
	project := env.Paths.ProjectDir

	// Uncommitted work in the clone cannot be fetched — git moves commits.
	// Saying so beats a silent partial result: the user asked for what is
	// in there, and this is the part that is not coming.
	if dirty, _, _, _ := clone.State(ctx, env.Runner, path); dirty > 0 {
		fmt.Fprintf(env.Stderr, "⚠  %d uncommitted change(s) in the clone are not fetched.\n",
			dirty)
		fmt.Fprintf(env.Stderr, "   Commit them there first, or copy them by hand:\n")
		fmt.Fprintf(env.Stderr, "     git -C %s status\n\n", path)
	}

	out, err := projectGit(ctx, env, project, "fetch", path,
		fmt.Sprintf("HEAD:%s", branch), "--force")
	if err != nil {
		return fmt.Errorf("fetching from the clone: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		fmt.Fprintln(env.Stdout, strings.TrimSpace(out))
	}

	ahead, err := projectGit(ctx, env, project, "rev-list", "--count", "HEAD.."+branch)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ahead) == "0" {
		fmt.Fprintf(env.Stdout, "Nothing new: the project already has everything on %s.\n", branch)
		return nil
	}

	// A fast-forward is the only move made without asking. It cannot lose
	// anything and cannot produce a conflict; everything else is a
	// judgement about code this command has not read.
	if _, err := projectGit(ctx, env, project, "merge", "--ff-only", branch); err != nil {
		fmt.Fprintf(env.Stdout, "%s commit(s) fetched onto %s.\n", strings.TrimSpace(ahead), branch)
		fmt.Fprintf(env.Stdout, "\nThe current branch has moved too, so this is a decision:\n")
		fmt.Fprintf(env.Stdout, "  git merge %s      # keep both histories\n", branch)
		fmt.Fprintf(env.Stdout, "  git rebase %s     # replay yours on top\n", branch)
		fmt.Fprintf(env.Stdout, "  git diff HEAD..%s # look first\n", branch)
		return nil
	}
	fmt.Fprintf(env.Stdout, "Fast-forwarded onto %s commit(s) from the clone.\n",
		strings.TrimSpace(ahead))
	fmt.Fprintf(env.Stdout, "The clone still has them; `dev clone prune` removes it "+
		"once the project has everything.\n")
	return nil
}

// requireClone resolves this project's clone, or explains its absence.
func requireClone(env *Env) (string, error) {
	path := clone.Dir(env.Paths.Home, projectSlug(env.Paths.ProjectDir))
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return "", fmt.Errorf("this project has no clone; `dev agent run` makes one, " +
			"and `dev run --clone` does too")
	}
	return path, nil
}

func cloneGit(ctx context.Context, env *Env, dir string, args ...string) (string, error) {
	return gitIn(ctx, env, dir, args...)
}

func projectGit(ctx context.Context, env *Env, dir string, args ...string) (string, error) {
	return gitIn(ctx, env, dir, args...)
}

func gitIn(ctx context.Context, env *Env, dir string, args ...string) (string, error) {
	res, err := env.Runner.Run(ctx, runner.Command{Path: "git", Args: args, Dir: dir})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("git %s: %s", strings.Join(args, " "),
			strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

// isGitRepo reports whether a directory is inside a git work tree.
//
// Used to tell "the clone failed" apart from "there was never a repository
// to clone", which are different messages: one is a fault, the other is a
// fact about the project the user did not have to state.
func isGitRepo(ctx context.Context, env *Env, dir string) bool {
	out, err := gitIn(ctx, env, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}
