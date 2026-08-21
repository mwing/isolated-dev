package clone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	// The branch the run started from, not whatever the host is on now.
	// A run lasts minutes and people switch branches during them.
	ref := CaptureRef(ProvenanceOf(clonePath).Branch, id)

	// The fetch runs in the project but reads the clone, starting
	// upload-pack inside it — so the clone's config is set aside for the
	// duration. --no-tags because an explicit refspec does not stop tag
	// following, and a tag from a clone feeds `git describe` and release
	// tooling in the user's own repository.
	err := WhileQuarantined(clonePath, func() error {
		// No --force. A capture ref is append-only: it may fast-forward,
		// and anything else would move it off commits it is holding. The
		// ids are meant to be unique, but "meant to" is what fails when two
		// runs land in the same second — and the cost of that is the work
		// this exists to keep.
		_, ferr := git(ctx, run, projectDir, "fetch", "--no-tags",
			clonePath, "HEAD:"+ref)
		if ferr == nil {
			return nil
		}
		// The ref exists and this is not a fast-forward of it, which means
		// two runs shared an id and hold different histories. Neither is
		// disposable, so the second gets a ref of its own rather than an
		// error: the tip's own name cannot collide with anything.
		if !strings.Contains(ferr.Error(), "non-fast-forward") {
			return ferr
		}
		tip, terr := gitOutput(ctx, run, clonePath, "rev-parse", "--short=9", "HEAD")
		if terr != nil {
			return ferr
		}
		ref += "-" + strings.TrimSpace(tip)
		_, ferr = git(ctx, run, projectDir, "fetch", "--no-tags", clonePath, "HEAD:"+ref)
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

// CaptureRef is where a run's work lands: under the branch it was made
// from, then the run.
//
// Keyed by branch because the question people actually ask is "what have I
// not finished on this branch?", and one flat namespace answers it only by
// reading timestamps and guessing. Long-lived feature work interleaved with
// other features is the case that makes a flat namespace useless — the
// captures pile up together and nothing distinguishes them.
//
// The branch is flattened into one path segment. Git refuses a ref that is
// both a file and a directory, so `refs/dev/clone/feat/<id>` and
// `refs/dev/clone/feat/foo/<id>` could not coexist — which is a collision
// between two of the user's own branches, and the least acceptable kind.
func CaptureRef(branch, id string) string {
	return RefNamespace + "/" + flattenBranch(branch) + "/" + id
}

// flattenBranch reduces a branch name to one ref-safe path segment.
func flattenBranch(branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
		return "detached"
	}
	var b strings.Builder
	for _, r := range branch {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	readable := strings.Trim(b.String(), "-.")
	if readable == "" {
		readable = "branch"
	}
	// A short digest of the original, because the readable part is lossy:
	// feature/foo, feature-foo and feature@foo all flatten to the same
	// thing, and two of the user's branches sharing a namespace would file
	// one branch's unapplied work under another's name. The digest makes
	// the segment unique while the prefix keeps it greppable.
	sum := sha256.Sum256([]byte(branch))
	return readable + "-" + hex.EncodeToString(sum[:3])
}

// CapturedRefs lists the captures the project holds for one branch, oldest
// first. An empty branch matches nothing.
//
// Empty deliberately means nothing rather than everything. CurrentBranch
// returns "" on a detached HEAD, and this used to read "" as "every
// branch" — so one sentinel meant both "the branch I am on" and "all of
// them", and an ordinary `apply` from a detached HEAD swept and dropped
// captures belonging to branches it had never looked at. A wildcard has
// to be asked for by name: see AllCapturedRefs.
func CapturedRefs(ctx context.Context, run runner.Runner, projectDir, branch string) ([]string, error) {
	if strings.TrimSpace(branch) == "" {
		return nil, nil
	}
	return refsUnder(ctx, run, projectDir, RefNamespace+"/"+flattenBranch(branch))
}

// AllCapturedRefs lists every branch's captures. Separate from
// CapturedRefs so that "I do not know which branch" can never be spelled
// the same way as "all of them".
func AllCapturedRefs(ctx context.Context, run runner.Runner, projectDir string) ([]string, error) {
	return refsUnder(ctx, run, projectDir, RefNamespace)
}

func refsUnder(ctx context.Context, run runner.Runner, projectDir, scope string) ([]string, error) {
	out, err := gitOutput(ctx, run, projectDir, "for-each-ref", "--format=%(refname)", scope)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

// CurrentBranch is the branch a project is on, empty when detached.
func CurrentBranch(ctx context.Context, run runner.Runner, projectDir string) string {
	out, err := gitOutput(ctx, run, projectDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || strings.TrimSpace(out) == "HEAD" {
		return ""
	}
	return strings.TrimSpace(out)
}

// DropCapture removes a ref once its work has landed, releasing the objects
// it was pinning.
func DropCapture(ctx context.Context, run runner.Runner, projectDir, ref string) error {
	if !strings.HasPrefix(ref, RefNamespace+"/") {
		return fmt.Errorf("clone: %s is not a capture this tool owns", ref)
	}
	// One invariant governs deletion: a capture is only ever dropped once
	// its tip is reachable from a branch. The ref exists to hold work until
	// it is somewhere a person would look, and "apply succeeded" is not the
	// same statement as "this particular capture landed".
	held, err := gitOutput(ctx, run, projectDir, "for-each-ref",
		"--format=%(refname)", "--contains", ref, "refs/heads")
	if err != nil {
		return fmt.Errorf("clone: could not confirm %s has landed: %w", ref, err)
	}
	if len(nonEmptyLines(held)) == 0 {
		return fmt.Errorf("clone: %s is on no branch, so it is still the only copy", ref)
	}
	_, err = git(ctx, run, projectDir, "update-ref", "-d", ref)
	return err
}
