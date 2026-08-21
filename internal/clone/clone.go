// Package clone gives a run a private copy of the project to work in.
//
// The default bind mount is the user's actual working tree, which is right
// for a person typing commands and wrong for an agent left running
// unattended: a mistake lands on the files being edited, and "undo" means
// whatever git can recover. A clone moves the blast radius off the working
// tree without taking the work away — the changes are still on disk,
// reviewable and mergeable, just not in the directory the user is in.
//
// It is a copy, not a snapshot: git objects are copied rather than
// hard-linked. Hard links are the fast default and are safe when both sides
// are trusted, but this exists precisely for the case where the container
// is not, and a process that rewrites a shared object file would corrupt
// the repository it was cloned from.
package clone

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mwing/isolated-dev/internal/runner"
)

// Result describes the clone a run will use.
type Result struct {
	// Path is the directory to mount at /workspace.
	Path string
	// Fresh reports whether this run created it.
	Fresh bool
	// Notes are differences between the clone and the working tree that
	// the user should know about before trusting what they see.
	Notes []string
}

// Options describe the clone to prepare.
type Options struct {
	// Project is the repository to copy, which must be its root.
	Project string
	// Dest is where the copy lives.
	Dest string
	// Depth limits how much history is copied. Zero copies all of it.
	//
	// A full copy of a large repository is mostly history the run will
	// never read, and the first clone of a multi-gigabyte tree is slow
	// enough to put people off using the mode at all. Work still comes
	// back out: the commits made in a shallow clone sit on top of a commit
	// the project already has, so fetching them needs nothing that was
	// left behind.
	Depth int
}

// Prepare returns a private copy of the project, creating it when it does
// not exist and reusing it when it does.
//
// Reuse is deliberate. An agent session that spans several runs would
// otherwise lose its work every time, which is the one outcome worse than
// working in the wrong directory. The cost is that the clone drifts from
// the project, so drift is reported rather than silently tolerated.
func Prepare(ctx context.Context, run runner.Runner, o Options) (Result, error) {
	projectDir, dest := o.Project, o.Dest
	res := Result{Path: dest}

	top, err := gitOutput(ctx, run, projectDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return res, fmt.Errorf("--clone needs a git repository: %s is not one "+
			"(a clone is how the work gets back out, and without git there is "+
			"nothing to diff against)", projectDir)
	}
	// git reports a resolved path, so both sides are resolved before they
	// are compared. Without this, a project reached through a symlink —
	// /tmp on macOS, or anyone whose code directory is a link — is
	// rejected as a subdirectory of itself.
	if !sameDir(top, projectDir) {
		// Cloning the whole repository when the user is in a subdirectory
		// would mount a different tree than a normal run does, silently
		// changing what /workspace means.
		return res, fmt.Errorf("--clone works from the repository root; "+
			"this is a subdirectory of %s", top)
	}

	if info, statErr := os.Stat(filepath.Join(dest, ".git")); statErr == nil && info.IsDir() {
		notes, err := driftNotes(ctx, run, projectDir, dest)
		res.Notes = notes
		// Also on the reuse path, so clones made before this existed are
		// repaired rather than left as the one that still cannot commit.
		res.Notes = append(res.Notes, ensureIdentity(ctx, run, projectDir, dest)...)
		// Recorded at creation and never rewritten. Re-recording here
		// would say the clone came from wherever the host has moved to
		// since, which is the bug the capture path was fixed for, one level
		// up. Filled in only when missing, for clones made before any of
		// this existed.
		if have := ProvenanceOf(dest); have.Branch == "" {
			_ = recordProvenance(dest, Provenance{
				Project: firstNonEmptyString(have.Project, projectDir),
				Branch:  branchOf(ctx, run, dest),
				Base:    have.Base,
			})
		}
		return res, err
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return res, err
	}
	if _, err := git(ctx, run, "", cloneArgs(o)...); err != nil {
		return res, fmt.Errorf("cloning %s: %w", projectDir, err)
	}
	res.Fresh = true
	if o.Depth > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"shallow: %d commit(s) of history, so `git log` and anything deriving "+
				"a version from tags sees less than the project does", o.Depth))
	}

	res.Notes = append(res.Notes, ensureIdentity(ctx, run, projectDir, dest)...)
	_ = recordProvenance(dest, Provenance{
		Project: projectDir,
		Branch:  CurrentBranch(ctx, run, projectDir),
		Base:    headOf(ctx, run, projectDir),
	})

	notes, err := carryUncommitted(ctx, run, projectDir, dest)
	res.Notes = append(res.Notes, notes...)
	return res, err
}

// ProjectFile is where a clone's project path is recorded: a sibling of the
// clone directory, not a file inside it.
//
// Inside would be inside the bind mount, which is to say inside the
// agent's reach — and the value's whole job is to be the one thing about
// the clone that the clone did not choose. The container mounts the clone
// directory alone, so a sibling is unreachable from it.
func ProjectFile(clonePath string) string { return clonePath + ".project" }

// Provenance is what a clone was made from, recorded when it was made.
//
// The branch matters as much as the path, and it has to be read here
// rather than from the host at capture time. A run lasts minutes and a
// person switches branches during them: asking "what branch is the project
// on?" when the agent finishes answers a question about the human, not
// about the run, and files the work under a branch it never came from.
type Provenance struct {
	Project string
	Branch  string
	Base    string
}

// recordProvenance notes what a clone was made from, beside the clone.
//
// Beside, never inside: inside is inside the bind mount, and the whole
// value of this record is that it is the one thing about a clone the clone
// did not choose.
func recordProvenance(clonePath string, p Provenance) error {
	body := fmt.Sprintf("project=%s\nbranch=%s\nbase=%s\n", p.Project, p.Branch, p.Base)
	return os.WriteFile(ProjectFile(clonePath), []byte(body), 0o600)
}

// ProvenanceOf reads what was recorded, tolerating the single-line form
// earlier versions wrote.
func ProvenanceOf(clonePath string) Provenance {
	body, err := os.ReadFile(ProjectFile(clonePath))
	if err != nil {
		return Provenance{}
	}
	text := strings.TrimSpace(string(body))
	if !strings.Contains(text, "=") {
		return Provenance{Project: text}
	}
	var out Provenance
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "project":
			out.Project = value
		case "branch":
			out.Branch = value
		case "base":
			out.Base = value
		}
	}
	return out
}

// projectOf returns the recorded project directory, or "" when there is
// none to trust.
//
// Absent is not an error to route around: it means containment cannot be
// proved, and every caller of this treats unprovable as "holds work".
// Clones made before this was recorded land there, which costs a refused
// deletion and never a lost commit.
func projectOf(clonePath string) string {
	dir := ProvenanceOf(clonePath).Project
	if dir == "" {
		return ""
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return ""
	}
	return dir
}

// ensureIdentity writes a committer identity into the clone's own config.
//
// Nothing else supplies one. A container gets none of the host's home
// directory, so there is no ~/.gitconfig in it, and `git clone` copies
// history rather than configuration — so the first commit inside the clone
// fails with "please tell me who you are". That is a wall every agent hits
// the moment it tries to save work, and the work is the whole reason the
// clone exists: `dev clone diff` and `dev clone apply` have nothing to show
// or fast-forward until something is committed.
//
// The identity is copied from the project when it has one. It is not a
// secret being handed over: the name and address are already written into
// every commit in the history the clone just copied. Writing it to the
// clone's local config rather than a global one keeps it scoped to this
// clone, and keeps everything else in the host's git configuration —
// signing keys, credential helpers, insteadOf rewrites — out.
func ensureIdentity(ctx context.Context, run runner.Runner, src, dest string) []string {
	// --local, not --get: a plain --get reads the host's global config too,
	// and the container never sees that. Checking it would mean any machine
	// whose user has a global git identity — which is to say every machine
	// that commits — decided the clone already had one and left it without.
	if got, err := gitOutput(ctx, run, dest, "config", "--local", "--get", "user.email"); err == nil &&
		strings.TrimSpace(got) != "" {
		return nil
	}

	name := strings.TrimSpace(configValue(ctx, run, src, "user.name"))
	email := strings.TrimSpace(configValue(ctx, run, src, "user.email"))
	var notes []string
	if name == "" || email == "" {
		// A placeholder rather than a failure. The alternative is an agent
		// that cannot commit at all, and a commit under an obviously
		// synthetic name is easy to correct later with `git commit --amend`.
		name, email = "dev sandbox", "dev-sandbox@localhost"
		notes = append(notes, "no git identity configured here, so the clone commits as "+
			name+" <"+email+">")
	}
	if _, err := git(ctx, run, dest, "config", "user.name", name); err != nil {
		return notes
	}
	if _, err := git(ctx, run, dest, "config", "user.email", email); err != nil {
		return notes
	}
	return notes
}

// configValue reads one git setting, treating "not set" as empty rather
// than as an error: `git config --get` exits 1 for a missing key.
func configValue(ctx context.Context, run runner.Runner, dir, key string) string {
	out, err := gitOutput(ctx, run, dir, "config", "--get", key)
	if err != nil {
		return ""
	}
	return out
}

// cloneArgs builds the git invocation.
//
// The two forms are not interchangeable. A plain local path lets git share
// the object store, so --no-hardlinks is required: a hard-linked object
// store is still the project's own data, and this mode exists because the
// container is not trusted with it. Depth is silently ignored on that
// path, which is why a shallow clone goes through file:// instead — and
// that transport copies objects anyway, so the same property holds by a
// different route.
func cloneArgs(o Options) []string {
	// Always the smart transport, never git's local mode. A local clone
	// copies the whole object database, so every other branch, tag and
	// unreachable blob comes with it — hidden from `branch -a` and readable
	// by sha. --no-hardlinks protected the source from modification and
	// said nothing about what the agent could read. Measured: with the
	// local clone a commit on another branch is fetchable by sha and `git
	// show` prints its contents; over file:// it is absent.
	//
	// --single-branch and --no-tags are what make that true: the clone
	// carries the history leading to the branch it starts from and nothing
	// else. An agent therefore cannot see other branches to rebase onto,
	// which is a deliberate narrowing rather than an oversight.
	args := []string{"clone", "--single-branch", "--no-tags"}
	if o.Depth > 0 {
		args = append(args, "--depth", strconv.Itoa(o.Depth))
	}
	return append(args, "file://"+o.Project, o.Dest)
}

// carryUncommitted reproduces the working tree's uncommitted state in the
// clone, so a run starts from what the user sees rather than from the last
// commit.
//
// Without this, a clone silently discards work in progress — the run would
// look fine and be operating on different code, which is worse than
// refusing outright.
func carryUncommitted(ctx context.Context, run runner.Runner, src, dest string) ([]string, error) {
	var notes []string

	// --binary so the patch survives files git considers binary; without
	// it, apply fails on exactly the files nobody remembers to check.
	//
	// The output is used verbatim: a patch is newline-terminated, and
	// trimming the last one makes `git apply` reject it as corrupt.
	diffRes, err := git(ctx, run, src, "diff", "--no-ext-diff", "HEAD", "--binary")
	if err != nil {
		return notes, err
	}
	diff := diffRes.Stdout
	if strings.TrimSpace(diff) != "" {
		res, err := run.Run(ctx, runner.Command{
			Path:  "git",
			Args:  []string{"-C", dest, "apply", "--whitespace=nowarn"},
			Stdin: strings.NewReader(diff),
		})
		if err != nil {
			return notes, err
		}
		if res.ExitCode != 0 {
			return notes, fmt.Errorf("carrying uncommitted changes into the clone: %s",
				strings.TrimSpace(res.Stderr))
		}
		notes = append(notes, "uncommitted changes carried in")
	}

	untracked, err := gitOutput(ctx, run, src, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return notes, err
	}
	var copied int
	for _, rel := range strings.Split(untracked, "\n") {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		if err := copyFile(filepath.Join(src, rel), filepath.Join(dest, rel)); err != nil {
			return notes, err
		}
		copied++
	}
	if copied > 0 {
		notes = append(notes, fmt.Sprintf("%d untracked file(s) copied in", copied))
	}
	// Ignored files are not carried: they are build output and installed
	// dependencies, they are the bulk of a large tree, and a clone that
	// copied them would take minutes to make. Say so, because a missing
	// node_modules looks like a bug until you know.
	//
	// It is also worth saying what else that covers. A gitignored .env does
	// not reach the clone either, which is the common shape for local
	// credentials — not because anything here looks for secrets, but
	// because "ignored" is where they usually live. A committed one is
	// carried like any other tracked file.
	notes = append(notes, "ignored files not copied — dependencies and build output, "+
		"and any gitignored .env with them")
	return notes, nil
}

// driftNotes describes how a reused clone differs from the project.
func driftNotes(ctx context.Context, run runner.Runner, src, dest string) ([]string, error) {
	notes := []string{"reusing the existing clone"}
	if shallow, err := cloneGitOutput(ctx, run, dest, "rev-parse", "--is-shallow-repository"); err == nil &&
		strings.TrimSpace(shallow) == "true" {
		notes = append(notes, "the clone is shallow")
	}

	// A clone is keyed by project path, not by branch, so starting work on
	// a new branch reuses the clone made from the old one. That produced a
	// real afternoon of confusion: an agent worked in a clone 64 commits
	// behind, on a branch nobody had asked for, and the merge back
	// conflicted on a 22,000-line lockfile that was already applied. The
	// old note for this was "the clone is on a different commit than the
	// project", which is true of almost every useful clone and so says
	// nothing.
	srcBranch := strings.TrimSpace(branchOf(ctx, run, src))
	destBranch := strings.TrimSpace(branchOf(ctx, run, dest))
	if srcBranch != "" && destBranch != "" && srcBranch != destBranch {
		notes = append(notes, fmt.Sprintf(
			"⚠  it is on %q and this project is on %q: work done here starts from "+
				"the wrong branch", destBranch, srcBranch))
		notes = append(notes, "   `dev clone rm --force` discards it so the next run clones afresh")
	}

	srcHead, err := gitOutput(ctx, run, src, "rev-parse", "HEAD")
	if err != nil {
		return notes, nil
	}
	destHead, err := cloneGitOutput(ctx, run, dest, "rev-parse", "HEAD")
	if err != nil {
		return notes, nil
	}

	// Whether the clone has ever seen where the project is now. Asked by
	// object presence because the project's newer commits are not in the
	// clone to compute a merge-base against.
	if strings.TrimSpace(srcHead) != strings.TrimSpace(destHead) {
		if _, err := cloneGit(ctx, run, dest, "cat-file", "-e",
			strings.TrimSpace(srcHead)+"^{commit}"); err != nil {
			behind := ""
			if base, berr := cloneGitOutput(ctx, run, dest, "rev-parse",
				"origin/"+destBranch); berr == nil {
				if n, nerr := gitOutput(ctx, run, src, "rev-list", "--count",
					strings.TrimSpace(base)+"..HEAD"); nerr == nil {
					behind = " (" + strings.TrimSpace(n) + " commit(s))"
				}
			}
			notes = append(notes, fmt.Sprintf(
				"⚠  the project has moved on since the clone was made%s, so what "+
					"comes back will not fast-forward", behind))
		} else {
			notes = append(notes, "the clone is on a different commit than the project")
		}
	}

	if status, err := cloneGitOutput(ctx, run, dest, "status", "--porcelain"); err == nil {
		if n := len(nonEmptyLines(status)); n > 0 {
			notes = append(notes, fmt.Sprintf("%d uncommitted change(s) already in the clone", n))
		}
	}
	return notes, nil
}

// branchOf is the checked-out branch name, empty on a detached HEAD.
func branchOf(ctx context.Context, run runner.Runner, dir string) string {
	out, err := gitOutput(ctx, run, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || strings.TrimSpace(out) == "HEAD" {
		return ""
	}
	return out
}

// sameDir compares two paths after resolving symlinks.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// hardenedGitArgs are prepended to every git invocation this package makes.
//
// A clone's `.git` is attacker-controlled: an agent has unsupervised write
// access to it, and these commands run on the host, as the user, with
// their SSH keys in reach — which is what --clone exists to protect.
// Measured on git 2.47.3, in a clone's own config:
//
//	core.fsmonitor = ./pwn.sh  +  git status --porcelain  ->  PAYLOAD-RAN
//	same, with -c core.fsmonitor=                         ->  (did not run)
//
// `status --porcelain` is what State and driftNotes run on every reuse, so
// this was reachable by any agent that had run once. These are the config
// keys whose values git executes as programs.
//
// Applied to the project's own repository as well as to clones. It is the
// user's own config there and not a threat, but one path that is always
// hardened cannot drift into two paths where one is not — the argument the
// runner package already makes about itself.
//
// Two more that look like they belong here and do not. `diff.external=`
// makes git try to execute the empty string ("cannot run : No such file
// or directory"); that class is disabled per-command with --no-ext-diff,
// which is why carryUncommitted's diff carries it. And
// protocol.file.allow=never. It is the
// right guard against a hostile repository fetching a submodule over
// file://, and it also forbids the local clone this package exists to
// make — the tests caught that immediately. It belongs on the specific
// fetch, not on every invocation.
//
// This is mitigation, not the whole fix (BACKLOG B24). A `filter.<driver>`
// named by an in-tree .gitattributes cannot be blanked by flag, because the
// driver name is the attacker's to choose; only quarantining the repo
// config closes that, and doing it safely means surviving a crash without
// leaving a clone stripped of its identity and its remote.
var hardenedGitArgs = []string{
	"-c", "core.fsmonitor=",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.pager=cat",
	"-c", "core.editor=true",
	"-c", "core.sshCommand=true",
	"-c", "uploadpack.packObjectsHook=",
}

// hardenedGitEnv is the environment those commands run with.
//
// append(os.Environ(), …), never a bare slice: runner.Command.Env replaces
// the environment rather than adding to it, so a literal would drop PATH
// and HOME and break git in a way that reads as a git bug.
func hardenedGitEnv() []string {
	return append(os.Environ(),
		// A `git replace` in the clone makes a host-side read show benign
		// content while a fetch delivers the real commit.
		"GIT_NO_REPLACE_OBJECTS=1",
		// Nothing here should ever be able to ask the user for anything.
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"GIT_ATTR_NOSYSTEM=1",
	)
}

// cloneGit runs git against a clone: hardened flags, and the clone's own
// repository config set aside for the duration.
//
// Separate from git() because git() is also used against the user's own
// project, where quarantining their config would be both wrong and rude.
// The distinction is which repository the command is pointed at, and it is
// the caller's to make.
func cloneGit(ctx context.Context, run runner.Runner, dir string,
	args ...string) (res runner.Result, err error) {
	qerr := withQuarantinedConfig(dir, func() error {
		res, err = git(ctx, run, dir, args...)
		return nil
	})
	if qerr != nil {
		return res, qerr
	}
	return res, err
}

func cloneGitOutput(ctx context.Context, run runner.Runner, dir string,
	args ...string) (string, error) {
	res, err := cloneGit(ctx, run, dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(res.Stdout, "\n"), nil
}

func git(ctx context.Context, run runner.Runner, dir string, args ...string) (runner.Result, error) {
	full := append(append([]string(nil), hardenedGitArgs...), args...)
	cmd := runner.Command{Path: "git", Args: full, Dir: dir, Env: hardenedGitEnv()}
	res, err := run.Run(ctx, cmd)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("git %s: %s", strings.Join(args, " "),
			strings.TrimSpace(res.Stderr))
	}
	return res, nil
}

func gitOutput(ctx context.Context, run runner.Runner, dir string, args ...string) (string, error) {
	res, err := git(ctx, run, dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(res.Stdout, "\n"), nil
}

func copyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	// Symlinks are recreated rather than followed: following one would
	// copy a file from outside the project into the clone, which is the
	// opposite of what this mode is for.
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		_ = os.Remove(dst)
		return os.Symlink(target, dst)
	}
	if !info.Mode().IsRegular() {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Dir is where a project's clone lives.
func Dir(home, slug string) string {
	return filepath.Join(home, "clones", slug)
}

// Remove deletes a clone. It refuses anything that is not one, since the
// argument is a path this tool composed and a bug here deletes a user's
// work.
func Remove(path string) error {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return fmt.Errorf("clone: %s does not look like a clone; not removing it", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	// The recorded project goes with it. Left behind, it would be adopted
	// by whatever clone is made at this path next — which is fine while the
	// path means the same project and wrong the moment it does not.
	if err := os.Remove(ProjectFile(path)); err != nil && !os.IsNotExist(err) {
		return err
	}
	// And the lock, which is only meaningful while there is something to
	// lock. Leaving it would accumulate a file per clone ever made.
	if err := os.Remove(path + ".lock"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// State reports what a clone is holding: uncommitted changes, commits the
// project does not have, the current branch, and whether history is
// truncated.
//
// The unmerged count is the one that matters, since it is what a deletion
// would destroy. It is answered by asking the project whether it contains
// each commit, not by asking the clone whether it has pushed them: work
// fetched and merged back is safe, and the clone has no way to know that
// happened. Reporting it as still at risk would train people to use
// --force, which is how the guard stops working.
func State(ctx context.Context, run runner.Runner, path string) (dirty, unmerged int,
	branch string, shallow bool) {
	if status, err := cloneGitOutput(ctx, run, path, "status", "--porcelain"); err == nil {
		dirty = len(nonEmptyLines(status))
	}
	branch, _ = cloneGitOutput(ctx, run, path, "rev-parse", "--abbrev-ref", "HEAD")
	if out, err := cloneGitOutput(ctx, run, path, "rev-parse", "--is-shallow-repository"); err == nil {
		shallow = strings.TrimSpace(out) == "true"
	}

	// Commits on local branches that no remote has: the candidates.
	out, err := cloneGitOutput(ctx, run, path, "log", "--branches", "--not", "--remotes",
		"--format=%H")
	if err != nil {
		return dirty, unmerged, branch, shallow
	}
	candidates := nonEmptyLines(out)
	if len(candidates) == 0 {
		return dirty, unmerged, branch, shallow
	}

	// Where the project is comes from a file beside the clone, never from
	// the clone's own config.
	//
	// Asking the clone was a data-loss path, not merely untidy: `git config
	// remote.origin.url .` makes the containment check below compare the
	// clone's commits against the clone itself, so every one of them reads
	// as already safe and `dev clone rm` and `dev clone prune` delete them
	// without --force. That is the shape B2 named — reading an identity out
	// of the repository whose identity is in question — arriving as
	// deletion rather than as confusion.
	origin := projectOf(path)
	if origin == "" {
		// Nothing trustworthy to check against, so every candidate counts.
		// Fail-closed: the cost is a refused deletion, and the alternative
		// cost is a deleted commit.
		return dirty, len(candidates), branch, shallow
	}

	for _, sha := range candidates {
		sha = strings.TrimSpace(sha)
		// A commit no branch in the project contains would be lost with
		// the directory. One that is merged, or fetched onto a branch, is
		// already safe.
		res, err := run.Run(ctx, runner.Command{
			Path: "git",
			Args: []string{"-C", origin, "branch", "--all", "--contains", sha, "--format=%(refname)"},
		})
		if err != nil || res.ExitCode != 0 || strings.TrimSpace(res.Stdout) == "" {
			unmerged++
		}
	}
	return dirty, unmerged, branch, shallow
}

// headOf is the project's commit when a clone was made, recorded so a
// later capture can say what it was based on without asking the clone.
func headOf(ctx context.Context, run runner.Runner, dir string) string {
	out, err := gitOutput(ctx, run, dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func firstNonEmptyString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
