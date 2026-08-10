package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/mwing/isolated-dev/internal/clone"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/project"
)

// useClone points the workspace mount at a private copy of the repository.
//
// Only the mount changes. The image, the allowlist and the recorded
// history stay the project's own, because the run is still that project's
// run — it is the working tree that is being protected, not the identity.
func useClone(ctx context.Context, env *Env, p *project.Project, spec *container.RunSpec,
	depth int) error {
	dest := clone.Dir(env.Paths.Home, projectSlug(p.Dir))
	res, err := clone.Prepare(ctx, env.Runner, clone.Options{
		Project: p.Dir, Dest: dest, Depth: depth,
	})
	if err != nil {
		return err
	}

	for i := range spec.Mounts {
		if spec.Mounts[i].Target == "/workspace" {
			spec.Mounts[i].Source = res.Path
		}
	}

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
