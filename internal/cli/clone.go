package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mwing/isolated-dev/internal/clone"
	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/trust"
)

// warnUnappliedCaptures says what earlier runs left that is not on a branch.
func warnUnappliedCaptures(ctx context.Context, env *Env, projectDir string) {
	// What is unapplied for the branch this project is on now, which is
	// the question someone standing on it is asking.
	branch := clone.CurrentBranch(ctx, env.Runner, projectDir)
	refs, err := clone.CapturedRefs(ctx, env.Runner, projectDir, branch)
	if err != nil || len(refs) == 0 {
		return
	}
	newest := refs[len(refs)-1]
	fmt.Fprintf(env.Stderr, "\n⚠  %d capture(s) from earlier runs on this branch are in the project\n",
		len(refs))
	fmt.Fprintf(env.Stderr, "   but not on it. They are safe — nothing removes them — but they are\n")
	fmt.Fprintf(env.Stderr, "   also not in your working tree.\n")
	fmt.Fprintf(env.Stderr, "     git log --oneline %s\n", newest)
	fmt.Fprintf(env.Stderr, "     dev clone apply    bring them onto your branch\n\n")
}

// captureCloneWork brings whatever the clone holds into the project, under
// a ref the tool owns, and says what arrived.
//
// Called at both ends of a run. At the end it closes the loop: the commits
// are in the project without anything the user owns having moved. At the
// start it is the recovery path — a session killed by a crash, an OOM or a
// closed laptop never reached its own ending, and its work would otherwise
// sit in the clone until somebody noticed. The operation is idempotent and
// lossless, so doing it twice costs nothing and doing it early costs
// nothing either.
//
// Never fatal. This is bookkeeping around someone else's run, and a run
// that worked must not be reported as failed because the capture did not.
func captureCloneWork(ctx context.Context, env *Env, projectDir, clonePath, id string) {
	if clonePath == "" {
		return
	}
	// The branch comes from what was recorded when the clone was made, not
	// from wherever the host has moved to since — Capture reads it.
	got, err := clone.Capture(ctx, env.Runner, projectDir, clonePath, id)
	if err != nil {
		fmt.Fprintf(env.Stderr, "⚠  could not capture the clone's work: %v\n", err)
		fmt.Fprintf(env.Stderr, "   It is still in %s.\n", clonePath)
		return
	}
	if got.Commits == 0 {
		return
	}
	fmt.Fprintf(env.Stderr, "\nThe clone has %d commit(s) the project did not, now fetched into it.\n",
		got.Commits)
	fmt.Fprintf(env.Stderr, "  git log --oneline %s\n", got.Ref)
	fmt.Fprintf(env.Stderr, "  dev clone apply    bring them onto your branch\n")
}

// captureID names a capture. Time-based so the refs sort in the order the
// runs happened, and unique per run so two runs before one apply cannot
// overwrite each other — which is the bug that made a pair of lossless
// operations compose into a lossy loop.
func captureID(t time.Time) string {
	return t.UTC().Format("20060102-150405")
}

// prepareCloneDir makes the private clone for a run and returns the
// directory to mount, or "" to stay in the working tree.
//
// Shared by `dev agent run` and `dev console --agent` because the decision
// they encode is the same one: an agent acts on a model's instructions, so
// it does not get the tree the person is editing. Two copies of that would
// be two chances for one of them to drift into the weaker default, and the
// weaker default is the one nobody notices.
//
// out is where the clone's own account of itself goes. The console has to
// send it somewhere that is not the screen the full-screen program is about
// to take over.
func prepareCloneDir(ctx context.Context, env *Env, projectDir string, depth int,
	out io.Writer) (string, error) {
	dest := clone.Dir(env.Paths.Home, projectSlug(projectDir))
	res, err := clone.Prepare(ctx, env.Runner, clone.Options{
		Project: projectDir, Dest: dest, Depth: depth,
	})
	// A clone needs a repository. Without one there is no mechanism — and
	// no error the user can act on either, since they never asked for a
	// clone: it is the default. So the run continues in place and says so
	// loudly. A directory with no version control has nothing to recover
	// from anyway, which is the same reason it is usually scratch work.
	if err != nil && !isGitRepo(ctx, env, projectDir) {
		fmt.Fprintf(env.Stderr,
			"⚠  %s is not a git repository, so there is nothing to clone.\n"+
				"   The agent edits these files directly, and nothing here\n"+
				"   can undo that. `git init` first, or --in-place to silence this.\n\n",
			projectDir)
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if res.Path != "" {
		fmt.Fprintf(out, "Clone:     %s\n", res.Path)
		for _, note := range res.Notes {
			fmt.Fprintf(out, "           %s\n", note)
		}
		// Named as commands rather than as a git recipe: the safer path has
		// to cost less than the unsafe one, and composing a fetch refspec is
		// more than it can cost.
		fmt.Fprintf(out, "Review:    dev clone diff\n")
		fmt.Fprintf(out, "Bring back: dev clone apply\n")
	}
	// Whatever an earlier run left behind, before this one starts writing
	// over the top of it.
	if res.Path != "" {
		captureCloneWork(ctx, env, projectDir, res.Path, captureID(time.Now())+"-recovered")
	}

	// Work from earlier runs that has not reached a branch. Said at the
	// start of a run, because that is when it can still be dealt with
	// cheaply — and because captures accumulate silently by design: nothing
	// the user owns moves when one is made, which is the property that
	// makes them safe and also the property that makes them easy to forget.
	//
	// A warning rather than a question. A prompt here would hang a scripted
	// run with nobody present, which is the lesson B9 paid for, and B12's
	// rule is that a stop names the flag that resolves it. There is nothing
	// dangerous to stop for yet: consecutive runs in one clone build on
	// each other, so the newest capture contains the older ones. That stops
	// being true when a clone is replaced under unapplied work — which is
	// part 2's problem, and part 2's condition list already has it.
	warnUnappliedCaptures(ctx, env, projectDir)

	// Now that every agent run makes one of these, the disk they take is
	// the tool's doing and its business to report — before it is a surprise
	// rather than after.
	warnCloneSpace(ctx, env)
	return res.Path, nil
}

// cloneOpts is what a command was told about the workspace, before the
// defaults for that kind of run are applied.
type cloneOpts struct {
	use     bool
	inPlace bool
	depth   int
}

// mountWorkspace points the /workspace mount at dir. Only the mount
// changes: the image, the allowlist and the recorded history stay the
// project's own, because the run is still that project's run.
func mountWorkspace(spec *container.RunSpec, dir string) {
	if dir == "" {
		return
	}
	for i := range spec.Mounts {
		if spec.Mounts[i].Target == "/workspace" {
			spec.Mounts[i].Source = dir
		}
	}
}

// agentBaseImage is the image an agent's overlay is built on when nothing
// else was chosen: the project's own environment, built if it does not
// exist yet, with the project's declared tools on it.
//
// This is what the agent definition already describes. `Agent.Base` is
// documented as "the image to build the overlay on **when no project image
// exists**", and `Agent.Runtime` exists so the agent's own runtime is
// installed into the overlay instead of being assumed present — which, as
// that field's comment puts it, is what "rules out running the agent on the
// project's own image — and an agent that cannot run the project's tests
// cannot check its own work". The wiring was simply never done, so
// `dev new python` followed by an agent produced a sandbox with no python
// in it, and the agent's first move was to fetch its own.
//
// Returns "" when the project has nothing to build, which leaves the
// agent's own base image as the fallback the definition intends.
func agentBaseImage(ctx context.Context, env *Env, eng *container.Engine,
	cfg config.Config, p *project.Project, store *trust.Store) (string, error) {
	if !projectIsBuildable(p) {
		return "", nil
	}
	exists, err := eng.ImageExists(ctx, p.Image)
	if err != nil {
		return "", err
	}
	if !exists {
		if err := buildImage(ctx, env, cfg, p, ""); err != nil {
			return "", err
		}
	}
	return ensureTools(ctx, env, eng, p, store, cfg)
}

// projectIsBuildable reports whether there is anything to build an image
// from: a recognized language, a Dockerfile, or a devcontainer naming an
// image.
func projectIsBuildable(p *project.Project) bool {
	return p != nil &&
		(p.Detected.Found() || p.Dockerfile != "" || p.DevcontainerImage != "")
}

// consoleWantsClone decides whether a console run works in a private clone.
//
// With an agent, the same default as `dev agent run`: what makes the clone
// right is who is driving — a model rather than the person in the room —
// not which view they are watching through. Without one, a person is
// running their own command, which is the case the plain mount is right
// for, so --clone is opt-in exactly as it is for `dev run` and `dev shell`.
func consoleWantsClone(cfg config.Config, agentName string, cl cloneOpts) bool {
	if agentName != "" {
		return wantCloneFor(cfg.AgentClone, cl.inPlace, cl.use)
	}
	return cl.use || cl.depth > 0
}

// wantCloneFor resolves the three layers, narrowest last: the built-in
// default, what config says, and what this invocation says. Someone who
// runs agents interactively sets agent_clone once instead of typing a flag
// forever; a flag still wins for the run in front of them, in both
// directions.
func wantCloneFor(configured, inPlace, useClone bool) bool {
	want := configured
	if inPlace {
		want = false
	}
	if useClone {
		want = true
	}
	return want
}

// useClone points the workspace mount at a private copy of the repository.
//
// Only the mount changes. The image, the allowlist and the recorded
// history stay the project's own, because the run is still that project's
// run — it is the working tree that is being protected, not the identity.
//
// This is the opt-in path for `dev run` and `dev shell`, where the person
// asked for it explicitly, so a missing repository is an error rather than
// the loud fallback prepareCloneDir makes for a default nobody chose.
func useClone(ctx context.Context, env *Env, p *project.Project, spec *container.RunSpec,
	depth int) error {
	dest := clone.Dir(env.Paths.Home, projectSlug(p.Dir))
	res, err := clone.Prepare(ctx, env.Runner, clone.Options{
		Project: p.Dir, Dest: dest, Depth: depth,
	})
	if err != nil {
		return err
	}

	mountWorkspace(spec, res.Path)

	fmt.Fprintf(env.Stderr, "Clone:    %s\n", res.Path)
	for _, note := range res.Notes {
		fmt.Fprintf(env.Stderr, "          %s\n", note)
	}
	// Work that cannot be found is work that was lost, so the way back is
	// printed at the start rather than left to be discovered.
	fmt.Fprintf(env.Stderr, "Review:   git -C %s status\n", res.Path)
	fmt.Fprintf(env.Stderr, "Bring back: git -C %s fetch %s && git -C %s log FETCH_HEAD\n\n",
		p.Dir, res.Path, p.Dir)
	return nil
}

// explainPortConflict turns the daemon's bind failure into something
// actionable.
//
// "Bind for 127.0.0.1:5000 failed: port is already allocated" names the
// port and nothing else. The usual cause is a sidecar from another project
// that outlived its run — killed before teardown — and the user has no way
// to know which project that was, or that `dev clean` is the fix.
func explainPortConflict(ctx context.Context, eng *container.Engine, err error, ports []int) error {
	if err == nil || !strings.Contains(err.Error(), "already allocated") {
		return err
	}
	for _, port := range ports {
		holders, lerr := eng.PublishedBy(ctx, port)
		if lerr != nil || len(holders) == 0 {
			continue
		}
		h := holders[0]
		project := h.Label("dev.project")
		if project == "" {
			project = "another project"
		}
		return fmt.Errorf("port %d is already published by %s (%s).\n"+
			"  Free it:  dev clean --all\n"+
			"  %w", port, h.Names, project, err)
	}
	return err
}
