package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/go/internal/langs"
)

// plugin writes a language plugin with the given scaffolding files.
func plugin(t *testing.T, decl string, files map[string]string) *langs.Language {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "language.yaml"), []byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := langs.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	l, ok := set.Get("demo")
	if !ok {
		t.Fatalf("plugin did not load: %v", set.Notes)
	}
	return l
}

const decl = `name: demo
versions: ["1.0", "2.0"]
detection:
  files: [demo.toml]
files:
  scaffolding: [demo.toml, "src/main.demo", .gitignore]
`

func TestScaffoldWritesNestedFilesWithSubstitutions(t *testing.T) {
	l := plugin(t, decl, map[string]string{
		"demo.toml":     "name = \"{{PROJECT_NAME}}\"\nversion = \"{{VERSION}}\"\n",
		"src/main.demo": "print(\"hello from {{PROJECT_NAME}}\")\n",
		".gitignore":    "/build\n",
	})
	dir := t.TempDir()

	plan, err := Build(l, dir, Vars{ProjectName: "my-app", Version: "2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(false); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "demo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `name = "my-app"`) ||
		!strings.Contains(string(body), `version = "2.0"`) {
		t.Fatalf("substitutions not applied:\n%s", body)
	}
	// Scaffolding may nest, e.g. src/main.rs.
	if _, err := os.Stat(filepath.Join(dir, "src", "main.demo")); err != nil {
		t.Errorf("nested file not created: %v", err)
	}
}

func TestScaffoldRefusesToOverwrite(t *testing.T) {
	// Scaffolding into a directory that already has work in it is far more
	// often a mistake than an intention, and it is unrecoverable.
	l := plugin(t, decl, map[string]string{"demo.toml": "new\n"})
	dir := t.TempDir()
	existing := filepath.Join(dir, "demo.toml")
	if err := os.WriteFile(existing, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := Build(l, dir, Vars{ProjectName: "x", Version: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf("conflicts = %v", plan.Conflicts)
	}
	if err := plan.Apply(false); err == nil {
		t.Fatal("overwrote an existing file")
	}
	body, _ := os.ReadFile(existing)
	if string(body) != "mine\n" {
		t.Fatalf("file was modified anyway: %q", body)
	}

	if err := plan.Apply(true); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(existing); string(body) != "new\n" {
		t.Errorf("--force did not overwrite: %q", body)
	}
}

func TestNothingIsWrittenWhenAnythingConflicts(t *testing.T) {
	// The plan is computed before anything is written, so a conflict stops
	// the whole operation rather than half of it.
	l := plugin(t, decl, map[string]string{
		"demo.toml":     "a\n",
		"src/main.demo": "b\n",
	})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.toml"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, _ := Build(l, dir, Vars{ProjectName: "x", Version: "1.0"})
	_ = plan.Apply(false)

	if _, err := os.Stat(filepath.Join(dir, "src", "main.demo")); err == nil {
		t.Error("a partial scaffold was written despite the conflict")
	}
}

func TestDeclaredButMissingFilesAreReportedNotInvented(t *testing.T) {
	// Content this tool made up would be a surprise attributed to the
	// plugin.
	l := plugin(t, decl, map[string]string{"demo.toml": "a\n"})
	plan, err := Build(l, t.TempDir(), Vars{ProjectName: "x", Version: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Missing) != 2 {
		t.Fatalf("missing = %v, want the two files the plugin did not ship", plan.Missing)
	}
	if len(plan.Files) != 1 {
		t.Fatalf("files = %v", plan.Paths())
	}
}

func TestPluginWithNoScaffoldingIsAnError(t *testing.T) {
	l := plugin(t, "name: demo\nversions: [\"1\"]\ndetection:\n  files: [x]\n", nil)
	if _, err := Build(l, t.TempDir(), Vars{}); err == nil {
		t.Fatal("a plugin with nothing to scaffold produced a plan")
	}
}
