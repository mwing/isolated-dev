package clone

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mwing/isolated-dev/go/internal/runner"
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
	res, err := Prepare(context.Background(), runner.New(false), src, dest)
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
	if _, err := Prepare(context.Background(), runner.New(false), src, dest); err != nil {
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
	if _, err := Prepare(context.Background(), runner.New(false), src, dest); err != nil {
		t.Fatal(err)
	}

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
	_, err := Prepare(context.Background(), runner.New(false), t.TempDir(),
		filepath.Join(t.TempDir(), "clone"))
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
	_, err := Prepare(context.Background(), runner.New(false), sub,
		filepath.Join(t.TempDir(), "clone"))
	if err == nil || !strings.Contains(err.Error(), "repository root") {
		t.Fatalf("err = %v", err)
	}
}

// A second run must continue the first one's work, not discard it.
func TestPrepareReusesAndReportsDrift(t *testing.T) {
	src := gitRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	if _, err := Prepare(ctx, runner.New(false), src, dest); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dest, "work.txt"), "agent output\n")

	res, err := Prepare(ctx, runner.New(false), src, dest)
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
