package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mwing/isolated-dev/internal/clone"
	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/project"
)

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
