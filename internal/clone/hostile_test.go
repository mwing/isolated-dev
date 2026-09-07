package clone

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/runner"
)

// unbornRepo is a repository as it exists between `git init` and the first
// commit: files on disk, some staged, no HEAD.
func unbornRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := runner.New(false)
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"},
		{"symbolic-ref", "HEAD", "refs/heads/main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := git(ctx, run, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dir, "staged.txt"), "staged\n")
	write(t, filepath.Join(dir, "loose.txt"), "untracked\n")
	if _, err := git(ctx, run, dir, "add", "staged.txt"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A repository between `git init` and its first commit is an ordinary
// state, and the one where an agent is most useful — there is nothing to
// lose review of yet. It was also the one state `--clone` refused: `git
// diff HEAD` has no HEAD to diff against and fails, so preparing the clone
// returned an error instead of a clone.
func TestPrepareWorksWithNoCommitsYet(t *testing.T) {
	src := unbornRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")

	res, err := Prepare(context.Background(), runner.New(false),
		Options{Project: src, Dest: dest})
	if err != nil {
		t.Fatalf("preparing a clone of a repository with no commits: %v", err)
	}
	// Both kinds of file have to arrive. With no commits, a staged file is
	// in the index rather than untracked, so asking only for untracked
	// files would carry half the work.
	for name, want := range map[string]string{
		"staged.txt": "staged\n",
		"loose.txt":  "untracked\n",
	} {
		if got := read(t, filepath.Join(dest, name)); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "no commits yet") {
		t.Errorf("the notes do not say the project had no commits: %v", res.Notes)
	}
}

// The file list was newline-split and each entry trimmed, so a legal
// filename with leading or trailing whitespace was looked for under a name
// it does not have — and the copy failed, taking the whole run with it.
func TestUntrackedNamesWithWhitespaceSurvive(t *testing.T) {
	src := gitRepo(t)
	names := []string{" leading.txt", "trailing .txt", "two  spaces.txt"}
	for _, n := range names {
		write(t, filepath.Join(src, n), "body\n")
	}

	dest := filepath.Join(t.TempDir(), "clone")
	if _, err := Prepare(context.Background(), runner.New(false),
		Options{Project: src, Dest: dest}); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for _, n := range names {
		if got := read(t, filepath.Join(dest, n)); got != "body\n" {
			t.Errorf("%q was not carried: %q", n, got)
		}
	}
}

// A replace ref makes a read show one commit's content while a fetch
// delivers another's. Reads here already ignore them
// (GIT_NO_REPLACE_OBJECTS=1), which is exactly why it has to be said: a
// defence that works invisibly leaves the user believing they are reading
// a repository nobody arranged for them.
func TestAnomaliesReportsReplaceRefs(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	run := runner.New(false)
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	head, err := gitOutput(ctx, run, dest, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, run, dest, "update-ref",
		"refs/replace/"+strings.TrimSpace(head), strings.TrimSpace(head)); err != nil {
		t.Fatal(err)
	}

	notes := strings.Join(Anomalies(ctx, run, dest), "\n")
	if !strings.Contains(notes, "replace ref") {
		t.Errorf("replace refs are not reported: %q", notes)
	}
}

// A grafts file rewrites what the history looks like. Same argument.
func TestAnomaliesReportsAGraftsFile(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	run := runner.New(false)
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dest, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, ".git", "info", "grafts"), "\n")

	notes := strings.Join(Anomalies(ctx, run, dest), "\n")
	if !strings.Contains(notes, "grafts") {
		t.Errorf("a grafts file is not reported: %q", notes)
	}
}

// Where the escape channel actually is, and is not. git's own
// check-ref-format refuses a control character in a branch name, so the
// name that reaches a note cannot carry one — the sanitizing there is
// belt-and-braces. A commit subject is different: git stores whatever it
// is given, and `dev clone diff` prints subjects. That case is tested
// where the printing happens, in internal/cli.
func TestGitItselfRefusesAControlCharacterInABranchName(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	run := runner.New(false)
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, run, dest, "checkout", "-q", "-b", "evil\x1b[2K"); err == nil {
		t.Error("git accepted a branch name with an escape sequence; the note " +
			"sanitizing is now the only thing standing between it and a terminal")
	}
}

func TestSanitizeKeepsTextAndDropsControl(t *testing.T) {
	got := Sanitize("fix\x1b[31m the\ttest\x07\u202e")
	if strings.ContainsAny(got, "\x1b\x07") || strings.Contains(got, "\u202e") {
		t.Errorf("control characters survived: %q", got)
	}
	if !strings.Contains(got, "fix") || !strings.Contains(got, "the\ttest") {
		t.Errorf("the text did not survive: %q", got)
	}
}

// A ref may begin with `-`, and git reads that as an option. Refused rather
// than escaped: there is no quoting that makes `-P` not an option.
func TestSafeArgRefusesWhatGitWouldReadAsAnOption(t *testing.T) {
	for _, bad := range []string{"--upload-pack=./pwn.sh", "-P", "", "  ", "one\x1btwo"} {
		if _, err := safeArg("branch name", bad); err == nil {
			t.Errorf("safeArg accepted %q", bad)
		}
	}
	if got, err := safeArg("branch name", "feature/ok"); err != nil || got != "feature/ok" {
		t.Errorf("safeArg rejected a legitimate name: %q %v", got, err)
	}
}

func TestSafeSHARefusesAnythingButAnObjectName(t *testing.T) {
	for _, bad := range []string{"--all", "HEAD", "deadbee!", "", "abc"} {
		if _, err := safeSHA(bad); err == nil {
			t.Errorf("safeSHA accepted %q", bad)
		}
	}
	if _, err := safeSHA("deadbeef"); err != nil {
		t.Errorf("safeSHA rejected an object name: %v", err)
	}
}

// The review's first finding, and it was right: GIT_NO_REPLACE_OBJECTS
// covers refs/replace and nothing else, so a grafts file still truncated
// history. Measured on git 2.52.0 — three commits, .git/info/grafts naming
// the tip: plain and GIT_NO_REPLACE_OBJECTS both showed 1 commit,
// GIT_GRAFT_FILE=/dev/null showed 3, and the `-c core.graftsFile` form did
// not work at all.
//
// State is where it mattered: it counts the commits a deletion would
// destroy by walking history, so a shortened walk means `dev clone rm` and
// `prune` delete work without asking for --force.
func TestAGraftCannotHideCommitsFromState(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	run := runner.New(false)
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	// Two commits the project does not have.
	for _, n := range []string{"one", "two"} {
		write(t, filepath.Join(dest, n+".txt"), n+"\n")
		if _, err := git(ctx, run, dest, "add", "-A"); err != nil {
			t.Fatal(err)
		}
		if _, err := git(ctx, run, dest, "commit", "-qm", n); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, unmerged, _, _ := stateOf(ctx, run, dest); unmerged != 2 {
		t.Fatalf("the clone does not hold the work this test is about: %d", unmerged)
	}

	// A graft that makes the tip look like a root commit.
	head, err := gitOutput(ctx, run, dest, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dest, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, ".git", "info", "grafts"), strings.TrimSpace(head)+"\n")

	if _, _, unmerged, _, _ := stateOf(ctx, run, dest); unmerged != 2 {
		t.Errorf("a graft changed what State counts as unmerged: %d, want 2 — "+
			"`dev clone rm` would delete it without --force", unmerged)
	}
}

// stateOf adapts State's positional returns for readability.
func stateOf(ctx context.Context, run runner.Runner, path string) (string, int, int, string, bool) {
	dirty, unmerged, branch, shallow := State(ctx, run, path)
	return path, dirty, unmerged, branch, shallow
}

// `git add f && rm f` is an ordinary state, and with --cached in the list
// for unborn repositories it became a refused run: ls-files reports index
// entries, which need not exist on disk, and copyFile's Lstat aborted the
// whole clone with a raw ENOENT.
func TestAStagedThenDeletedFileDoesNotRefuseTheClone(t *testing.T) {
	src := unbornRepo(t)
	if err := os.Remove(filepath.Join(src, "staged.txt")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "clone")

	if _, err := Prepare(context.Background(), runner.New(false),
		Options{Project: src, Dest: dest}); err != nil {
		t.Fatalf("a staged-then-deleted file refused the clone: %v", err)
	}
	// The file that does exist still arrives.
	if got := read(t, filepath.Join(dest, "loose.txt")); got != "untracked\n" {
		t.Errorf("loose.txt = %q", got)
	}
}

// An unborn repository has a branch — `git init` points HEAD at one — but
// `rev-parse --abbrev-ref HEAD` needs a commit to answer, so this returned
// "" and captures were filed under refs/dev/clone/detached/…. Once the
// agent made the first commits and the user was on main, `apply` looked
// under main and never saw them: work in a place nobody looks, pinning its
// objects forever.
func TestTheBranchOfAnUnbornRepositoryIsItsBranch(t *testing.T) {
	src := unbornRepo(t)
	ctx := context.Background()
	run := runner.New(false)

	if got := CurrentBranch(ctx, run, src); got != "main" {
		t.Errorf("CurrentBranch on an unborn repository = %q, want main", got)
	}
	// And a real detached HEAD still reports nothing, or the distinction
	// this rests on is gone.
	if _, err := git(ctx, run, src, "commit", "-qm", "first"); err != nil {
		t.Fatal(err)
	}
	head, err := gitOutput(ctx, run, src, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, run, src, "checkout", "-q", "--detach",
		strings.TrimSpace(head)); err != nil {
		t.Fatal(err)
	}
	if got := CurrentBranch(ctx, run, src); got != "" {
		t.Errorf("a detached HEAD reported branch %q", got)
	}
}

// An external review's critical: the config quarantine set aside
// `.git/config`, but `.git/commondir` names a different directory whose
// config git reads instead — so a filter.<x>.clean there, chosen by an
// in-tree .gitattributes, ran on the host during an ordinary `git status`,
// escaping the quarantine entirely. Confirmed against host git 2.52.0
// before the fix. The layout a workload rewrote this way is refused, not
// read.
func TestACommondirRedirectionIsRefused(t *testing.T) {
	dir := t.TempDir()
	run := runner.New(false)
	ctx := context.Background()
	for _, a := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := git(ctx, run, dir, a...); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dir, "kept.txt"), "original\n")
	if _, err := git(ctx, run, dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, run, dir, "commit", "-qm", "init"); err != nil {
		t.Fatal(err)
	}

	// Move the real repository into a common dir the agent points at, and
	// plant the filter there — outside the .git/config the quarantine sees.
	if err := os.MkdirAll(filepath.Join(dir, ".git", "common"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"config", "refs", "objects"} {
		if err := os.Rename(filepath.Join(dir, ".git", n),
			filepath.Join(dir, ".git", "common", n)); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dir, ".git", "commondir"), "common\n")
	marker := filepath.Join(t.TempDir(), "HOST-MARKER")
	f, err := os.OpenFile(filepath.Join(dir, ".git", "common", "config"),
		os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("[filter \"review\"]\n\tclean = \"touch " + marker + "; cat\"\n")
	_ = f.Close()
	write(t, filepath.Join(dir, ".gitattributes"), "kept.txt filter=review\n")
	write(t, filepath.Join(dir, ".git", "config"), "# harmless decoy\n")
	write(t, filepath.Join(dir, "kept.txt"), "ORIGINAL\n") // same length, changed

	_, err = Read(ctx, run, dir, "status", "--porcelain")
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a filter named in a redirected common config ran on the host")
	}
	if err == nil {
		t.Errorf("the redirected clone was read rather than refused")
	}
}

// The review's structural point on the quarantine: a two-file blocklist
// keeps missing the next redirection mechanism. A symlinked objects
// directory redirects the store the same way the alternates file does, and
// the first fix caught the file but not the directory. Refused now.
func TestASymlinkedObjectsDirIsRefused(t *testing.T) {
	dir := t.TempDir()
	run := runner.New(false)
	ctx := context.Background()
	for _, a := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := git(ctx, run, dir, a...); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dir, "f.txt"), "x\n")
	if _, err := git(ctx, run, dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, run, dir, "commit", "-qm", "i"); err != nil {
		t.Fatal(err)
	}
	// Redirect the object store to an agent-chosen path via a symlink.
	elsewhere := t.TempDir()
	real := filepath.Join(dir, ".git", "objects")
	if err := os.Rename(real, filepath.Join(elsewhere, "objects")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "objects"), real); err != nil {
		t.Fatal(err)
	}

	if _, err := Read(ctx, run, dir, "status", "--porcelain"); err == nil {
		t.Error("a clone with a symlinked objects directory was read rather than refused")
	}
}
