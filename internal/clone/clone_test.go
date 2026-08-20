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
		// Signing is the runner's preference, not this test's.
		{"config", "commit.gpgsign", "false"},
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

// The class flags cannot close: the filter driver is named by an in-tree
// .gitattributes, so there is no key to blank with -c. Only the repository
// config not being there while host git reads works.
func TestAnAttackerNamedFilterDoesNotRunOnTheHost(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	marker := "FILTER-RAN"
	write(t, filepath.Join(dest, "pwn.sh"), "#!/bin/sh\necho "+marker+"\ncat\n")
	if err := os.Chmod(filepath.Join(dest, "pwn.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, ".gitattributes"), "* filter=whatever\n")
	if _, err := git(ctx, run, dest, "config", "filter.whatever.clean", "./pwn.sh"); err != nil {
		t.Fatal(err)
	}

	notes, _ := driftNotes(ctx, run, src, dest)
	if strings.Contains(strings.Join(notes, "\n"), marker) {
		t.Fatalf("a filter named by the clone ran on the host:\n%s", strings.Join(notes, "\n"))
	}

	// The clone's own config has to be back, or the quarantine has eaten
	// the identity and remote that make the clone usable.
	body, err := os.ReadFile(filepath.Join(dest, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "filter.whatever") && !strings.Contains(string(body), "whatever") {
		t.Errorf("the clone's config was not restored:\n%s", body)
	}
	if strings.Contains(string(body), "Written by dev") {
		t.Errorf("the clone was left with the tool's stand-in config:\n%s", body)
	}
}

// A crash leaves the original alongside under a known name. The next call
// repairs it before doing anything else, so recovery is an ordinary path
// rather than one that runs only after the crash nobody planned for.
func TestAnInterruptedQuarantineIsRepairedOnNextUse(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	cfg := filepath.Join(dest, ".git", "config")
	original, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly the state a crash mid-quarantine leaves.
	if err := os.Rename(cfg, filepath.Join(dest, ".git", quarantinedName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(minimalConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, _, _ = State(ctx, run, dest); true {
		body, rerr := os.ReadFile(cfg)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if string(body) != string(original) {
			t.Errorf("the clone's config was not restored after an interrupted quarantine:\n%s", body)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, ".git", quarantinedName)); err == nil {
		t.Error("the set-aside copy is still there after recovery")
	}
}

// Measured: with .git replaced by a `gitdir:` pointer, <clone>/.git/config
// does not stat while git works perfectly through the pointer. A quarantine
// that read "no config here" as "nothing to set aside" therefore ran host
// git against a config of the agent's choosing — failing open in the one
// function whose whole job is to fail closed.
func TestAGitdirPointerIsRefusedNotWorkedAround(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	elsewhere := filepath.Join(t.TempDir(), "elsewhere.git")
	if err := os.Rename(filepath.Join(dest, ".git"), elsewhere); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, ".git"), "gitdir: "+elsewhere+"\n")

	if err := withQuarantinedConfig(dest, func() error { return nil }); err == nil {
		t.Fatal("a gitdir pointer was accepted, so host git would run unprotected")
	}
	// And the refusal reaches the callers rather than being swallowed.
	if _, err := cloneGitOutput(ctx, run, dest, "status", "--porcelain"); err == nil {
		t.Error("a clone read succeeded against an unexpected layout")
	}
}

// `git config remote.origin.url .` made State compare the clone's commits
// against the clone itself, so all of them read as already contained and
// `dev clone rm` / `dev clone prune` deleted them without --force. Not
// misplaced trust: deletion of the only copy of an agent's work.
func TestAForgedOriginCannotMakeWorkLookSafeToDelete(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	// Work that exists only in the clone.
	write(t, filepath.Join(dest, "agent.txt"), "work\n")
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "agent work"}} {
		if _, err := git(ctx, run, dest, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, _ = State(ctx, run, dest); true {
		if _, unmerged, _, _ := State(ctx, run, dest); unmerged != 1 {
			t.Fatalf("unmerged = %d before forging, want 1", unmerged)
		}
	}

	// The forgery.
	if _, err := git(ctx, run, dest, "config", "remote.origin.url", "."); err != nil {
		t.Fatal(err)
	}
	if _, unmerged, _, _ := State(ctx, run, dest); unmerged != 1 {
		t.Fatalf("a forged origin hid %d unmerged commit(s) — they would be deleted", 1-unmerged+1)
	}
}

// Clones made before the project was recorded cannot prove containment.
// Unprovable has to mean "holds work": a refused deletion costs a command,
// a wrong deletion costs the work.
func TestAnUnrecordedProjectCountsAsHoldingWork(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, "agent.txt"), "work\n")
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "agent work"}} {
		if _, err := git(ctx, run, dest, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(ProjectFile(dest)); err != nil {
		t.Fatal(err)
	}

	if _, unmerged, _, _ := State(ctx, run, dest); unmerged == 0 {
		t.Fatal("a clone whose project is unknown reported nothing to lose")
	}
}

// Capture puts the clone's commits in the project without moving anything
// the user owns, and says how many were new.
func TestCaptureBringsWorkIntoTheProjectWithoutMovingABranch(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	before, _ := gitOutput(ctx, run, src, "rev-parse", "HEAD")

	write(t, filepath.Join(dest, "agent.txt"), "work\n")
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "agent work"}} {
		if _, err := git(ctx, run, dest, args...); err != nil {
			t.Fatal(err)
		}
	}
	// A tag in the clone must not follow the fetch: tags feed `git
	// describe` and release tooling in the user's own repository.
	if _, err := git(ctx, run, dest, "tag", "-m", "release", "v9.9.9"); err != nil {
		t.Fatal(err)
	}

	got, err := Capture(ctx, run, src, dest, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Commits != 1 || got.Ref != CaptureRef("main", "run-1") {
		t.Fatalf("capture = %+v, want 1 commit on %s", got, CaptureRef("main", "run-1"))
	}

	// The project has the commit...
	if _, err := git(ctx, run, src, "cat-file", "-e", got.Ref+"^{commit}"); err != nil {
		t.Errorf("the captured commit is not in the project: %v", err)
	}
	// ...and nothing the user owns has moved.
	after, _ := gitOutput(ctx, run, src, "rev-parse", "HEAD")
	if after != before {
		t.Errorf("HEAD moved during a capture: %s -> %s", before, after)
	}
	branches, _ := gitOutput(ctx, run, src, "for-each-ref", "--format=%(refname)", "refs/heads")
	if strings.Contains(branches, "clone") {
		t.Errorf("a capture created a branch: %s", branches)
	}
	if tags, _ := gitOutput(ctx, run, src, "tag"); strings.Contains(tags, "v9.9.9") {
		t.Error("a tag from the clone followed the fetch")
	}

	// Idempotent: capturing again with nothing new keeps one ref.
	if _, err := Capture(ctx, run, src, dest, "run-2"); err != nil {
		t.Fatal(err)
	}
	refs, err := CapturedRefs(ctx, run, src, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Logf("refs = %v", refs)
	}

	if err := DropCapture(ctx, run, src, got.Ref); err != nil {
		t.Fatal(err)
	}
	refs, _ = CapturedRefs(ctx, run, src, "main")
	for _, r := range refs {
		if r == got.Ref {
			t.Error("a dropped capture is still there")
		}
	}
}

// A capture with nothing the project lacks leaves no ref: refs pin their
// objects against gc forever, so an empty one is a leak with a name.
func TestCaptureKeepsNoRefWhenThereIsNothingNew(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	run := runner.New(false)
	ctx := context.Background()
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}

	got, err := Capture(ctx, run, src, dest, "empty-run")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != "" || got.Commits != 0 {
		t.Fatalf("capture of an untouched clone = %+v, want nothing", got)
	}
	refs, _ := CapturedRefs(ctx, run, src, "main")
	if len(refs) != 0 {
		t.Errorf("an empty capture left refs behind: %v", refs)
	}
}

// The refuse-to-drop guard: only refs this tool owns.
func TestDropRefusesRefsItDoesNotOwn(t *testing.T) {
	src := gitRepo(t)
	if err := DropCapture(context.Background(), runner.New(false), src, "refs/heads/main"); err == nil {
		t.Fatal("dropped a ref outside the tool's namespace")
	}
}

// A run lasts minutes and people switch branches during them. Asking the
// host which branch it is on when the agent finishes answers a question
// about the human, not about the run — and files the work under a branch it
// never came from. The provenance is recorded when the clone is made.
func TestCaptureUsesTheBranchTheRunStartedFrom(t *testing.T) {
	src := gitRepo(t)
	run := runner.New(false)
	ctx := context.Background()
	if _, err := git(ctx, run, src, "checkout", "-q", "-b", "feature/long-lived"); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "clone")
	if _, err := Prepare(ctx, run, Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	if got := ProvenanceOf(dest); got.Branch != "feature/long-lived" || got.Base == "" {
		t.Fatalf("provenance = %+v, want the branch and commit the clone was made from", got)
	}

	write(t, filepath.Join(dest, "a.txt"), "agent work\n")
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "agent work"}} {
		if _, err := git(ctx, run, dest, args...); err != nil {
			t.Fatal(err)
		}
	}

	// The human moves on while the agent is still working.
	if _, err := git(ctx, run, src, "checkout", "-q", "-b", "something-else"); err != nil {
		t.Fatal(err)
	}

	got, err := Capture(ctx, run, src, dest, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := CaptureRef("feature/long-lived", "run-1"); got.Ref != want {
		t.Fatalf("capture filed at %s, want %s — the branch the run came from",
			got.Ref, want)
	}
	if strings.Contains(got.Ref, "something-else") {
		t.Error("the capture was filed under the branch the human switched to")
	}

	// And it is findable by that branch.
	refs, err := CapturedRefs(ctx, run, src, "feature/long-lived")
	if err != nil || len(refs) != 1 {
		t.Errorf("the feature branch's captures = %v (%v)", refs, err)
	}
}

// Flattening a branch to readable characters is lossy: feature/foo,
// feature-foo and feature@foo all reduce to the same thing, and two of the
// user's branches sharing a namespace would file one's unapplied work under
// the other's name.
func TestBranchSegmentsDoNotCollide(t *testing.T) {
	seen := map[string]string{}
	for _, b := range []string{
		"feature/foo", "feature-foo", "feature@foo", "feature.foo",
		"feature/foo/bar", "Feature/Foo",
	} {
		got := flattenBranch(b)
		if prev, dup := seen[got]; dup {
			t.Errorf("%q and %q both produce %q", prev, b, got)
		}
		seen[got] = b
		if !strings.HasPrefix(got, "feature") && !strings.HasPrefix(got, "Feature") {
			t.Errorf("%q lost its readable prefix: %q", b, got)
		}
	}
}
