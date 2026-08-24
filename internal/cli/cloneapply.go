package cli

import (
	"context"
	"fmt"
	"io"
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
		// Sanitized: these lines are commit subjects, and a subject is a
		// field the committer chose. This is the review step, on a terminal
		// that executes some of what it is sent — an escape sequence here
		// could erase the line above it or redraw one commit as another,
		// which is precisely the trust the review depends on.
		fmt.Fprintf(env.Stdout, "Commits the project does not have:\n\n%s\n",
			clone.SanitizeLines(commits))
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
		// A patch body is the one place where sanitizing and fidelity
		// actually conflict, and the resolution is what the output is for.
		//
		// On a terminal it is being read, and every byte of it — the `+++
		// b/<path>` headers, the added lines — was chosen by the agent. That
		// is the attack this sanitizing exists for, and it is the default
		// output of the review command, so leaving it raw would have left
		// the largest surface open while cleaning the smaller ones.
		//
		// Redirected, it may be a patch someone means to apply, and a
		// replacement character in a CRLF file or a binary hunk would
		// corrupt it. So the rule is the one git uses for colour: decide by
		// where the output is going. `--stat` is filenames rather than
		// content and is safe to clean either way.
		if stat || goesToATerminal(env.Stdout) {
			uncommitted = clone.SanitizeLines(uncommitted)
		}
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
		// Stated as the contract, not as a shortfall. `apply` moves
		// commits; committing is the act that says "this is work", and
		// reaching into a working tree would mean deciding about untracked
		// files, ignored files and partial staging — judgements about code
		// this command has not read.
		fmt.Fprintf(env.Stderr, "Note: %d uncommitted change(s) stay in the clone — apply moves "+
			"commits.\n", dirty)
		fmt.Fprintf(env.Stderr, "      Commit them there and apply again if you want them:\n")
		fmt.Fprintf(env.Stderr, "        git -C %s status\n\n", path)
	}

	// The fetch runs in the project and reads the clone, so it starts
	// upload-pack inside a repository an agent controls. Quarantined for
	// the duration: uploadpack.packObjectsHook there names a program that
	// would otherwise run on the host.
	var out string
	if qerr := clone.WhileQuarantined(path, func() error {
		var ferr error
		// --no-tags for the reason the capture path already carries it: an
		// explicit refspec does not stop tag following, so without it a tag
		// the agent made lands in the user's own repository, where
		// `git describe` and release tooling read it.
		out, ferr = projectGit(ctx, env, project, "fetch", "--no-tags", path,
			fmt.Sprintf("HEAD:%s", branch), "--force")
		return ferr
	}); qerr != nil {
		return fmt.Errorf("fetching from the clone: %w", qerr)
	}
	if strings.TrimSpace(out) != "" {
		fmt.Fprintln(env.Stdout, clone.SanitizeLines(strings.TrimSpace(out)))
	}

	// Captures from runs that ended without anyone applying them. They are
	// the same work, already in the project, so `apply` has to sweep them
	// or it reports "nothing new" while they sit in a namespace the user
	// has no reason to know about.
	if refs, rerr := clone.CapturedRefs(ctx, env.Runner, project, clone.CurrentBranch(ctx, env.Runner, project)); rerr == nil && len(refs) > 0 {
		fmt.Fprintf(env.Stdout, "%d capture(s) from earlier runs are already here:\n", len(refs))
		for _, r := range refs {
			fmt.Fprintf(env.Stdout, "  %s\n", r)
		}
		fmt.Fprintf(env.Stdout, "They are included below; `git log --oneline <ref>` reads one.\n\n")
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
		// Lead with the branch the user is standing on. The command is
		// called `apply`, so "fetched onto clone-work" reads as a detail
		// while they look at an unchanged working tree and conclude it did
		// nothing — reported exactly that way. What happened to *their*
		// branch is the answer to the question they asked.
		fmt.Fprintf(env.Stdout, "Your branch is unchanged. The clone's %s commit(s) are here, "+
			"on %s.\n", strings.TrimSpace(ahead), branch)
		fmt.Fprintf(env.Stdout, "\nBoth histories have moved, so combining them is a judgement "+
			"about code\nthis command has not read. Yours to make:\n")
		fmt.Fprintf(env.Stdout, "  git log --oneline HEAD..%s  # what it did\n", branch)
		fmt.Fprintf(env.Stdout, "  git cherry-pick %s          # take it, if it is one commit\n", branch)
		fmt.Fprintf(env.Stdout, "  git merge %s                # keep both histories\n", branch)
		fmt.Fprintf(env.Stdout, "  git rebase %s               # replay yours on top\n", branch)
		return nil
	}
	fmt.Fprintf(env.Stdout, "Fast-forwarded onto %s commit(s) from the clone.\n",
		strings.TrimSpace(ahead))
	// The captures have landed on a branch now, so the refs are only
	// pinning objects against gc. Dropped quietly: their whole purpose was
	// to hold the work until this moment.
	if refs, rerr := clone.CapturedRefs(ctx, env.Runner, project, clone.CurrentBranch(ctx, env.Runner, project)); rerr == nil {
		for _, r := range refs {
			_ = clone.DropCapture(ctx, env.Runner, project, r)
		}
	}
	fmt.Fprintf(env.Stdout, "The clone still has them; `dev clone prune` removes it "+
		"once the project has everything.\n")
	return nil
}

// goesToATerminal reports whether output written here is being read by a
// person rather than captured.
//
// The distinction decides whether clone-derived bytes are sanitized: a
// reader needs the escape sequences neutralized, a file needs the bytes.
// Anything that is not an *os.File is a buffer or a pipe — something
// capturing the output — so it gets the bytes.
func goesToATerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
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

// cloneGit reads a clone through the one hardened, config-quarantined path.
//
// It used to forward straight to git, so `dev clone diff` ran against a
// repository an agent had been writing to with that repository's own
// config in force — the hardening in internal/clone stopped at the package
// boundary, which is exactly where a second way of doing the same thing
// gets written.
func cloneGit(ctx context.Context, env *Env, dir string, args ...string) (string, error) {
	return clone.Read(ctx, env.Runner, dir, args...)
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
