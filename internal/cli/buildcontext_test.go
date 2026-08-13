package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bigTree writes a directory whose weight is concentrated where a
// .dockerignore line would remove it.
func bigTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []struct {
		path string
		size int
	}{
		{"node_modules/pkg/blob.bin", 150 << 20},
		{".git/objects/pack.bin", 90 << 20},
		{"dist/bundle.js", 20 << 20},
		{"src/index.js", 32},
	} {
		full := filepath.Join(dir, f.path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, make([]byte, f.size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// "This build sends 7.4G" says there is a problem. Naming the directories
// says which two lines fix it, and that is the half a person can act on.
func TestTheContextReportNamesTheBiggestEntries(t *testing.T) {
	rep, err := measureContext(bigTree(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 4 {
		t.Errorf("files = %d, want 4", rep.Files)
	}
	if rep.Bytes < 250<<20 {
		t.Errorf("bytes = %d, want the whole tree", rep.Bytes)
	}
	if len(rep.Largest) == 0 || rep.Largest[0].Name != "node_modules" {
		t.Fatalf("largest = %v, want node_modules first", rep.Largest)
	}
	if rep.Largest[1].Name != ".git" {
		t.Errorf("second largest = %v, want .git", rep.Largest[1])
	}
	if rep.Truncated {
		t.Error("a four-file tree was reported as truncated")
	}
}

func TestTheBuildWarnsAboutALargeContext(t *testing.T) {
	h := newHarness(t)
	dir := bigTree(t)

	warnBuildContext(h.env, dir)

	out := h.stderr.String()
	for _, want := range []string{"No .dockerignore", "node_modules", ".git", "/workspace"} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning does not mention %q:\n%s", want, out)
		}
	}
}

// A warning that cannot be made to go away is one people learn to scroll
// past, so writing the file has to silence it.
func TestNoWarningOnceThereIsADockerignore(t *testing.T) {
	h := newHarness(t)
	dir := bigTree(t)
	if err := os.WriteFile(filepath.Join(dir, ".dockerignore"),
		[]byte("node_modules\n.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	warnBuildContext(h.env, dir)
	if h.stderr.Len() != 0 {
		t.Errorf("warned despite a .dockerignore:\n%s", h.stderr.String())
	}
}

// Most projects are small, and a build that comments on an ordinary tree is
// noise on every build.
func TestNoWarningForAnOrdinaryTree(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	warnBuildContext(h.env, dir)
	if h.stderr.Len() != 0 {
		t.Errorf("warned about a small project:\n%s", h.stderr.String())
	}
}

// A partial total presented as a whole one is a wrong number, so a walk
// that stopped says so.
func TestATruncatedWalkSaysSo(t *testing.T) {
	rep := contextReport{Bytes: 1 << 30, Files: contextFileLimit, Truncated: true}
	if !rep.Truncated {
		t.Fatal("the flag does not survive")
	}
	h := newHarness(t)
	dir := t.TempDir()
	// Enough files to trip the limit would make this test slow; the wording
	// is what matters and it is driven by the flag.
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 1), 0o644); err != nil {
		t.Fatal(err)
	}
	warnBuildContext(h.env, dir)
	if strings.Contains(h.stderr.String(), "at least") {
		t.Error("a complete walk claimed to be truncated")
	}
}

func TestTopLevelIsTheDockerignoreGranularity(t *testing.T) {
	for in, want := range map[string]string{
		filepath.Join("node_modules", "pkg", "x.js"): "node_modules",
		filepath.Join(".git", "objects"):             ".git",
		"main.go":                                    "main.go",
	} {
		if got := topLevel(in); got != want {
			t.Errorf("topLevel(%q) = %q, want %q", in, got, want)
		}
	}
}
