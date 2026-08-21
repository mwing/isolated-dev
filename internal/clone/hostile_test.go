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
	got := Sanitize("fix\x1b[31m the\ttest\x07‮")
	if strings.ContainsAny(got, "\x1b\x07") || strings.Contains(got, "‮") {
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
