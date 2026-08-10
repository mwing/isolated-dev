package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mwing/isolated-dev/go/internal/project"
)

// Regenerating must read the project, not the last export of it. Reading
// its own output back appends the tools layer and the package upgrade once
// more on every run, so the file grows and stops describing anything.
func TestDropGeneratedIgnoresOurOwnOutput(t *testing.T) {
	dir := t.TempDir()
	df := filepath.Join(dir, ".devcontainer", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(df), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(df, []byte(generatedHeader+"\nFROM debian\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &project.Project{Dir: dir, Dockerfile: df}
	dropGenerated(p)
	if p.Dockerfile != "" {
		t.Fatalf("kept its own output as the source: %s", p.Dockerfile)
	}
}

// A Dockerfile someone wrote by hand is the source of truth, wherever it
// sits. Only the marker distinguishes the two.
func TestDropGeneratedKeepsAHandWrittenDockerfile(t *testing.T) {
	dir := t.TempDir()
	df := filepath.Join(dir, ".devcontainer", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(df), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(df, []byte("FROM debian\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &project.Project{Dir: dir, Dockerfile: df}
	dropGenerated(p)
	if p.Dockerfile != df {
		t.Fatal("discarded a Dockerfile it did not write")
	}
}

func TestDropGeneratedKeepsAProjectDockerfile(t *testing.T) {
	dir := t.TempDir()
	df := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(df, []byte(generatedHeader+"\nFROM debian\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &project.Project{Dir: dir, Dockerfile: df}
	dropGenerated(p)
	if p.Dockerfile != df {
		t.Fatal("discarded the project's own Dockerfile")
	}
}
