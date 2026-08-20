package clone

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mwing/isolated-dev/internal/runner"
)

// RefNamespace is where captured work lands in the project.
//
// Under refs/, so the commits are reachable and safe from gc — but not
// under refs/heads/, so they are not branches: invisible to `git branch`,
// impossible to check out by accident, not pushed (push refspecs name
// refs/heads/*), not fetched by teammates.
//
// The tool owns this namespace, which is the whole point. `clone-work` is
// a branch the tool force-moved, so a user who had commits there lost
// them; and two runs before one apply overwrote each other. A ref the tool
// created is one it can safely manage.
const RefNamespace = "refs/dev/clone"

// Captured is what one capture produced.
type Captured struct {
	// Ref is the full ref name, empty when there was nothing to keep.
	Ref string
	// Commits is how many the project did not already have.
	Commits int
}

// Capture fetches the clone's current HEAD into the project under a ref the
// tool owns, and reports what the project gained.
//
// Called at both ends of a run, not only at the end. It is idempotent and
// lossless, so running it at the start means a session that was killed —
// out of memory, a bug, a closed laptop — has its commits picked up by the
// next run rather than sitting in the clone until someone notices. A
// mechanism that only works when the process exits cleanly is the wrong
// shape for something that runs unattended.
//
// Nothing is checked out and no branch moves: the commits are simply in the
// project rather than only in the clone.
func Capture(ctx context.Context, run runner.Runner, projectDir, clonePath, id string) (Captured, error) {
	var out Captured
	if id == "" {
		return out, fmt.Errorf("clone: a capture needs an id")
	}
	ref := RefNamespace + "/" + id

	// The fetch runs in the project but reads the clone, starting
	// upload-pack inside it — so the clone's config is set aside for the
	// duration. --no-tags because an explicit refspec does not stop tag
	// following, and a tag from a clone feeds `git describe` and release
	// tooling in the user's own repository.
	err := WhileQuarantined(clonePath, func() error {
		_, ferr := git(ctx, run, projectDir, "fetch", "--no-tags", "--force",
			clonePath, "HEAD:"+ref)
		return ferr
	})
	if err != nil {
		return out, fmt.Errorf("capturing the clone's work: %w", err)
	}

	// What the project did not already have. Counted against every local
	// branch rather than HEAD, so work already merged elsewhere is not
	// reported as new.
	count, err := gitOutput(ctx, run, projectDir, "rev-list", "--count", ref, "--not", "--branches")
	if err != nil {
		return Captured{Ref: ref}, nil
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(count))
	if convErr != nil {
		return Captured{Ref: ref}, nil
	}
	if n == 0 {
		// Nothing worth keeping a ref for. Refs pin their objects against
		// gc forever, so an empty one is a leak with a name.
		_, _ = git(ctx, run, projectDir, "update-ref", "-d", ref)
		return out, nil
	}
	return Captured{Ref: ref, Commits: n}, nil
}

// CapturedRefs lists the captures the project is holding, oldest first.
func CapturedRefs(ctx context.Context, run runner.Runner, projectDir string) ([]string, error) {
	out, err := gitOutput(ctx, run, projectDir, "for-each-ref", "--format=%(refname)", RefNamespace)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

// DropCapture removes a ref once its work has landed, releasing the objects
// it was pinning.
func DropCapture(ctx context.Context, run runner.Runner, projectDir, ref string) error {
	if !strings.HasPrefix(ref, RefNamespace+"/") {
		return fmt.Errorf("clone: %s is not a capture this tool owns", ref)
	}
	_, err := git(ctx, run, projectDir, "update-ref", "-d", ref)
	return err
}
