package clone

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mwing/isolated-dev/internal/runner"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := runner.New(false)
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		if _, err := git(ctx, run, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dir, "kept.txt"), "committed\n")
	if _, err := git(ctx, run, dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, run, dir, "commit", "-qm", "init"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A clone that only carries the last commit silently runs against
// different code than the user is looking at.
func TestPrepareCarriesUncommittedWork(t *testing.T) {
	src := gitRepo(t)
	write(t, filepath.Join(src, "kept.txt"), "committed\nedited\n")
	write(t, filepath.Join(src, "new.txt"), "untracked\n")

	dest := filepath.Join(t.TempDir(), "clone")
	res, err := Prepare(context.Background(), runner.New(false),
		Options{Project: src, Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Fresh {
		t.Fatal("expected a fresh clone")
	}
	if got := read(t, filepath.Join(dest, "kept.txt")); got != "committed\nedited\n" {
		t.Fatalf("uncommitted edit not carried: %q", got)
	}
	if got := read(t, filepath.Join(dest, "new.txt")); got != "untracked\n" {
		t.Fatalf("untracked file not carried: %q", got)
	}
}

// The whole point: what happens in the clone stays there.
func TestWritesInTheCloneDoNotReachTheProject(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	if _, err := Prepare(context.Background(), runner.New(false),
		Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dest, "kept.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, "damage.txt"), "x")

	if _, err := os.Stat(filepath.Join(src, "kept.txt")); err != nil {
		t.Fatal("a deletion in the clone reached the project")
	}
	if _, err := os.Stat(filepath.Join(src, "damage.txt")); err == nil {
		t.Fatal("a file created in the clone appeared in the project")
	}
}

// HardLinked objects are still the project's own data, which is exactly
// what an untrusted container must not be handed.
func TestObjectsAreCopiedNotHardLinked(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	if _, err := Prepare(context.Background(), runner.New(false),
		Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	assertNoSharedObjects(t, dest)
}

func assertNoSharedObjects(t *testing.T, dest string) {
	t.Helper()
	var checked int
	err := filepath.Walk(filepath.Join(dest, ".git", "objects"),
		func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !info.Mode().IsRegular() {
				return err
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return nil
			}
			if st.Nlink > 1 {
				t.Fatalf("%s is hard-linked (%d links) — shared with the project",
					path, st.Nlink)
			}
			checked++
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no object files were checked; the test proves nothing")
	}
}

func TestPrepareRefusesANonRepository(t *testing.T) {
	_, err := Prepare(context.Background(), runner.New(false),
		Options{Project: t.TempDir(), Dest: filepath.Join(t.TempDir(), "clone")})
	if err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("err = %v", err)
	}
}

// Cloning the repository root while the user is in a subdirectory would
// mount a different tree than a normal run does.
func TestPrepareRefusesASubdirectory(t *testing.T) {
	src := gitRepo(t)
	sub := filepath.Join(src, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Prepare(context.Background(), runner.New(false),
		Options{Project: sub, Dest: filepath.Join(t.TempDir(), "clone")})
	if err == nil || !strings.Contains(err.Error(), "repository root") {
		t.Fatalf("err = %v", err)
	}
}

// A second run must continue the first one's work, not discard it.
func TestPrepareReusesAndReportsDrift(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	if _, err := Prepare(ctx, runner.New(false),
		Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, "work.txt"), "agent output\n")

	res, err := Prepare(ctx, runner.New(false), Options{Project: src, Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	if res.Fresh {
		t.Fatal("re-cloned over existing work")
	}
	if read(t, filepath.Join(dest, "work.txt")) != "agent output\n" {
		t.Fatal("existing work was lost")
	}
	joined := strings.Join(res.Notes, "; ")
	if !strings.Contains(joined, "reusing") || !strings.Contains(joined, "uncommitted") {
		t.Fatalf("drift not reported: %v", res.Notes)
	}
}

func TestRemoveRefusesWhatIsNotAClone(t *testing.T) {
	dir := t.TempDir()
	if err := Remove(dir); err == nil {
		t.Fatal("removed a directory that is not a clone")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("it was removed anyway")
	}
}

// commits adds n commits so history has something to truncate.
func commits(t *testing.T, dir string, n int) {
	t.Helper()
	run := runner.New(false)
	ctx := context.Background()
	for i := 0; i < n; i++ {
		write(t, filepath.Join(dir, "kept.txt"), strings.Repeat("x", i+1)+"\n")
		if _, err := git(ctx, run, dir, "commit", "-qam", "change"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestShallowCloneTruncatesHistory(t *testing.T) {
	src := gitRepo(t)
	commits(t, src, 4)
	dest := filepath.Join(t.TempDir(), "clone")

	ctx := context.Background()
	res, err := Prepare(ctx, runner.New(false), Options{Project: src, Dest: dest, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	count, err := gitOutput(ctx, runner.New(false), dest, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if count != "1" {
		t.Fatalf("history depth = %s, want 1", count)
	}
	if !strings.Contains(strings.Join(res.Notes, "; "), "shallow") {
		t.Fatalf("shallowness not reported: %v", res.Notes)
	}
}

// file:// transport copies objects, so the property that made
// --no-hardlinks necessary holds here by a different route.
func TestShallowCloneAlsoSharesNothing(t *testing.T) {
	src := gitRepo(t)
	commits(t, src, 3)
	dest := filepath.Join(t.TempDir(), "clone")
	if _, err := Prepare(context.Background(), runner.New(false),
		Options{Project: src, Dest: dest, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	assertNoSharedObjects(t, dest)
}

// The point of a clone is that the work comes back. A shallow clone's
// commits sit on top of a commit the project already has, so nothing that
// was left behind is needed to fetch them.
func TestWorkComesBackOutOfAShallowClone(t *testing.T) {
	src := gitRepo(t)
	commits(t, src, 3)
	dest := filepath.Join(t.TempDir(), "clone")

	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest, Depth: 1}); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(dest, "patch.txt"), "the fix\n")
	for _, args := range [][]string{
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-qm", "the fix"},
	} {
		if _, err := git(ctx, run, dest, args...); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := git(ctx, run, src, "fetch", "-q", dest, "HEAD"); err != nil {
		t.Fatalf("fetching work back from a shallow clone: %v", err)
	}
	if _, err := git(ctx, run, src, "cherry-pick", "FETCH_HEAD"); err != nil {
		t.Fatalf("applying the fetched commit: %v", err)
	}
	if got := read(t, filepath.Join(src, "patch.txt")); got != "the fix\n" {
		t.Fatalf("patch content = %q", got)
	}
}

// commitIn makes a commit inside a clone, as an agent would.
func commitIn(t *testing.T, dir, file, body string) {
	t.Helper()
	run := runner.New(false)
	ctx := context.Background()
	write(t, filepath.Join(dir, file), body)
	for _, args := range [][]string{
		{"config", "user.email", "a@example.com"},
		{"config", "user.name", "agent"},
		{"add", "-A"},
		{"commit", "-qm", "work"},
	} {
		if _, err := git(ctx, run, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStateCountsWorkTheProjectDoesNotHave(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	run := runner.New(false)
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	if dirty, unmerged, _, _ := State(ctx, run, dest); dirty != 0 || unmerged != 0 {
		t.Fatalf("fresh clone: dirty=%d unmerged=%d, want 0/0", dirty, unmerged)
	}

	commitIn(t, dest, "work.txt", "output\n")
	write(t, filepath.Join(dest, "scratch.txt"), "wip\n")

	dirty, unmerged, _, _ := State(ctx, run, dest)
	if unmerged != 1 {
		t.Fatalf("unmerged = %d, want 1 — a deletion would lose that commit", unmerged)
	}
	if dirty != 1 {
		t.Fatalf("dirty = %d, want 1", dirty)
	}
}

// Once the work is merged back, the clone is safe to delete. Reporting it
// as still at risk would train people to reach for --force, which is how
// the guard stops working.
func TestStateStopsCountingWorkOnceItIsMergedBack(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	run := runner.New(false)
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	commitIn(t, dest, "work.txt", "output\n")

	if _, unmerged, _, _ := State(ctx, run, dest); unmerged != 1 {
		t.Fatalf("unmerged = %d before the fetch, want 1", unmerged)
	}

	for _, args := range [][]string{
		{"fetch", "-q", dest, "HEAD"},
		{"merge", "-q", "FETCH_HEAD", "-m", "merge"},
	} {
		if _, err := git(ctx, run, src, args...); err != nil {
			t.Fatal(err)
		}
	}

	if _, unmerged, _, _ := State(ctx, run, dest); unmerged != 0 {
		t.Fatalf("unmerged = %d after merging it back, want 0", unmerged)
	}
}

// A container has no ~/.gitconfig, and git will not guess an address from a
// container hostname: it fails with "unable to auto-detect email address".
// So without an identity in the clone's own config, the first commit an
// agent tries fails — and until something is committed, `dev clone diff`
// and `dev clone apply` have nothing to show or bring back. The clone is
// the way the work gets out, so it has to be committable.
func TestCloneCanBeCommittedIn(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()

	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"user.name", "user.email"} {
		got, err := gitOutput(ctx, run, dest, "config", "--local", "--get", key)
		if err != nil || strings.TrimSpace(got) == "" {
			t.Fatalf("the clone has no %s of its own: %q %v", key, got, err)
		}
	}
	// Copied from the project rather than invented, so the commits that come
	// back are attributed the way that repository's commits already are.
	if got, _ := gitOutput(ctx, run, dest, "config", "--local", "--get", "user.email"); strings.TrimSpace(got) != "t@example.com" {
		t.Errorf("identity = %q, want the project's", strings.TrimSpace(got))
	}
}

// An existing clone predates the fix, and is exactly the one that cannot
// commit. Reusing it has to repair it rather than leave it broken.
func TestReusingAnOldCloneGivesItAnIdentity(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()

	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"user.name", "user.email"} {
		_, _ = git(ctx, run, dest, "config", "--local", "--unset", key)
	}

	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	got, err := gitOutput(ctx, run, dest, "config", "--local", "--get", "user.email")
	if err != nil || strings.TrimSpace(got) == "" {
		t.Fatalf("reusing the clone did not repair its identity: %q %v", got, err)
	}
}

// A clone is keyed by project path, not by branch, so starting work on a
// new branch reuses the clone made from the old one. That cost a real
// afternoon: an agent worked in a clone 64 commits behind, on a branch
// nobody had asked for, and the merge back conflicted on a 22,000-line
// lockfile whose change was already applied. The note for it was "the
// clone is on a different commit than the project", which is true of
// almost every useful clone and so says nothing.
func TestReusingACloneFromAnotherBranchSaysSo(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()

	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	// The project moves to another branch and gains commits, which is the
	// ordinary shape of coming back to a project a week later.
	for _, args := range [][]string{{"checkout", "-q", "-b", "other-branch"}} {
		if _, err := git(ctx, run, src, args...); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(src, "moved.txt"), "moved\n")
	if _, err := git(ctx, run, src, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, run, src, "commit", "-qm", "work on the other branch"); err != nil {
		t.Fatal(err)
	}

	res, err := Prepare(ctx, run, Options{Project: src, Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(res.Notes, "\n")

	if !strings.Contains(all, "other-branch") {
		t.Errorf("the notes do not name the branch the project is on:\n%s", all)
	}
	if !strings.Contains(all, "wrong branch") {
		t.Errorf("the notes do not say the clone starts from the wrong place:\n%s", all)
	}
	if !strings.Contains(all, "dev clone rm --force") {
		t.Errorf("the notes do not say how to start afresh:\n%s", all)
	}
	if !strings.Contains(all, "moved on since the clone was made") {
		t.Errorf("the notes do not say the project has moved:\n%s", all)
	}
}

// A clone's .git is attacker-controlled: an agent writes to it freely, and
// these commands run on the host, as the user, with their SSH keys in
// reach — which is what --clone exists to protect. Measured on git 2.47.3:
// core.fsmonitor pointing at a script executes it during the
// `status --porcelain` that State and driftNotes run on every reuse.
func TestHostSideGitDoesNotRunProgramsFromTheClone(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()

	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	// The payload announces itself on stdout, so anything that executed it
	// shows up in what the tool read back.
	marker := "PAYLOAD-RAN-" + filepath.Base(dest)
	script := filepath.Join(dest, "pwn.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{
		{"core.fsmonitor", "./pwn.sh"},
		{"core.hooksPath", dest},
		{"core.pager", "./pwn.sh"},
		{"core.sshCommand", "./pwn.sh"},
		{"uploadpack.packObjectsHook", "./pwn.sh"},
	} {
		if _, err := git(ctx, run, dest, "config", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}

	// Everything this package does to a clone, through its own helpers.
	dirty, _, branch, _ := State(ctx, run, dest)
	notes, _ := driftNotes(ctx, run, src, dest)
	got := strings.Join(append(notes, branch), "\n")

	if strings.Contains(got, marker) {
		t.Fatalf("a program from the clone ran on the host:\n%s", got)
	}
	// And the reads still work, or the hardening has bought safety by
	// breaking the feature.
	// The payload script itself is the untracked file, so a working read
	// sees exactly one.
	if dirty != 1 || branch == "" {
		t.Errorf("hardening broke the reads it was protecting: dirty=%d branch=%q", dirty, branch)
	}
	if len(notes) == 0 {
		t.Error("driftNotes reported nothing about a reused clone")
	}
}
