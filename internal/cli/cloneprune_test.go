package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mwing/isolated-dev/internal/clone"
	"github.com/mwing/isolated-dev/internal/runner"
)

// "7d" is how people describe a week; time.ParseDuration does not accept it.
func TestDurationsAcceptDays(t *testing.T) {
	cases := map[string]string{"7d": "168h", "1d": "24h", "0.5d": "12h", "24h": "24h", "30m": "30m"}
	for in, want := range cases {
		if got := normalizeDuration(in); got != want {
			t.Errorf("normalizeDuration(%q) = %q, want %q", in, got, want)
		}
	}
}

// makeClone builds a real clone under a temp home, since what prune decides
// depends on what git says about it.
func makeClone(t *testing.T, home, name string) string {
	t.Helper()
	src := t.TempDir()
	run := runner.New(false)
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
	} {
		if _, err := run.Run(ctx, runner.Command{Path: "git", Args: args, Dir: src}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		if _, err := run.Run(ctx, runner.Command{Path: "git", Args: args, Dir: src}); err != nil {
			t.Fatal(err)
		}
	}

	dest := filepath.Join(home, "clones", name)
	if _, err := clone.Prepare(ctx, run, clone.Options{Project: src, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	return dest
}

// A clone holding work is the one thing prune must never take. A clone
// holding nothing is the whole reason it exists.
func TestPruneKeepsWorkAndRemovesTheRest(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)

	empty := makeClone(t, h.paths.Home, "empty")
	dirty := makeClone(t, h.paths.Home, "dirty")
	if err := os.WriteFile(filepath.Join(dirty, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Both are new, so a zero window is what makes age irrelevant here.
	if err := pruneClones(context.Background(), h.env, 0, false, false); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(empty); err == nil {
		t.Error("a clone holding nothing survived a prune")
	}
	if _, err := os.Stat(dirty); err != nil {
		t.Error("a clone holding uncommitted work was removed")
	}
	if out := h.stdout.String(); !strings.Contains(out, "holds work") {
		t.Errorf("prune did not say why it kept one:\n%s", out)
	}
}

// Recency outranks emptiness: a run that finished minutes ago is one
// somebody may still be reading.
func TestPruneKeepsRecentClones(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	fresh := makeClone(t, h.paths.Home, "fresh")

	if err := pruneClones(context.Background(), h.env, 24*time.Hour, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("a clone made moments ago was pruned")
	}
	if out := h.stdout.String(); !strings.Contains(out, "touched") {
		t.Errorf("prune did not say it was kept for its age:\n%s", out)
	}
}

// --force is for the case the guard exists to prevent, so it has to
// actually override it.
func TestPruneForceRemovesWork(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	dirty := makeClone(t, h.paths.Home, "dirty")
	if err := os.WriteFile(filepath.Join(dirty, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := pruneClones(context.Background(), h.env, 0, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirty); err == nil {
		t.Fatal("--force did not remove a clone holding work")
	}
}

// A dry run that removes something is not a dry run.
func TestPruneDryRunRemovesNothing(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	empty := makeClone(t, h.paths.Home, "empty")

	if err := pruneClones(context.Background(), h.env, 0, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(empty); err != nil {
		t.Fatal("a dry run deleted a clone")
	}
	if out := h.stdout.String(); !strings.Contains(out, "would remove") {
		t.Errorf("dry run did not report what it would do:\n%s", out)
	}
}
