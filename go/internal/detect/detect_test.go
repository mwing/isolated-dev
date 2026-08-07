package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/go/internal/langs"
)

// plugins writes language plugin directories and loads them.
func plugins(t *testing.T, defs map[string]string) *langs.Set {
	t.Helper()
	dir := t.TempDir()
	for name, body := range defs {
		d := filepath.Join(dir, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "language.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := langs.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// project writes files into a fresh project directory.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const pythonPlugin = `name: python
versions: ["3.11", "3.12", "3.13"]
ports: [8000, 5000]
detection:
  files: [requirements.txt, pyproject.toml, setup.py]
  version_files:
    - file: .python-version
`

const nodePlugin = `name: node
versions: ["20", "22"]
ports: [3000]
detection:
  files: [package.json]
  version_files:
    - file: .nvmrc
`

const goPlugin = `name: golang
versions: ["1.24", "1.26"]
ports: [8080]
detection:
  files: [go.mod, main.go]
  version_files:
    - file: go.mod
      extract: 'go ([0-9]+\.[0-9]+)'
`

func TestDetectsFromPluginDataNotAHardcodedTable(t *testing.T) {
	// The point of the rewrite for this package: a plugin nobody wrote
	// code for must still be detected.
	set := plugins(t, map[string]string{
		"elixir": "name: elixir\nversions: [\"1.17\"]\nports: [4000]\ndetection:\n  files: [mix.exs]\n",
	})
	dir := project(t, map[string]string{"mix.exs": "defmodule X do\nend\n"})

	res := Detect(dir, set)
	if !res.Found() || res.Language.Name != "elixir" {
		t.Fatalf("result = %+v", res)
	}
	if res.Version != "1.17" {
		t.Errorf("version = %q, want the plugin default", res.Version)
	}
	if len(res.Ports) != 1 || res.Ports[0] != 4000 {
		t.Errorf("ports = %v, want the plugin's", res.Ports)
	}
}

func TestNoLanguageDetected(t *testing.T) {
	set := plugins(t, map[string]string{"python": pythonPlugin})
	res := Detect(project(t, map[string]string{"README.md": "hi"}), set)
	if res.Found() {
		t.Fatalf("detected %v in an empty project", res.Language.Name)
	}
	if !strings.Contains(res.Explain(), "no language") {
		t.Errorf("Explain() = %q", res.Explain())
	}
}

func TestVersionFromPlainFile(t *testing.T) {
	set := plugins(t, map[string]string{"python": pythonPlugin})
	dir := project(t, map[string]string{
		"requirements.txt": "flask\n",
		".python-version":  "3.12\n",
	})

	res := Detect(dir, set)
	if res.Version != "3.12" {
		t.Fatalf("version = %q", res.Version)
	}
	if res.VersionFrom != ".python-version" {
		t.Errorf("VersionFrom = %q", res.VersionFrom)
	}
}

func TestVersionFromRegexpCapture(t *testing.T) {
	set := plugins(t, map[string]string{"golang": goPlugin})
	dir := project(t, map[string]string{
		"go.mod": "module example.com/x\n\ngo 1.26\n\nrequire ()\n",
	})

	res := Detect(dir, set)
	if res.Version != "1.26" {
		t.Fatalf("version = %q, want the captured group", res.Version)
	}
}

func TestVersionMarkersAreNormalized(t *testing.T) {
	// .nvmrc conventionally carries a leading v; manifests carry ranges.
	set := plugins(t, map[string]string{"node": nodePlugin})
	dir := project(t, map[string]string{
		"package.json": "{}",
		".nvmrc":       "v22.3.0\n",
	})

	if got := Detect(dir, set).Version; got != "22.3.0" {
		t.Fatalf("version = %q, want the v stripped", got)
	}
}

func TestFallsBackToPluginDefaultVersion(t *testing.T) {
	set := plugins(t, map[string]string{"python": pythonPlugin})
	dir := project(t, map[string]string{"requirements.txt": "flask\n"})

	res := Detect(dir, set)
	if res.Version != "3.13" {
		t.Errorf("version = %q, want the newest declared", res.Version)
	}
	if res.VersionFrom != "" {
		t.Errorf("VersionFrom = %q, want empty for a default", res.VersionFrom)
	}
	if !strings.Contains(res.Explain(), "plugin default") {
		t.Errorf("Explain() should say the version was a default: %q", res.Explain())
	}
}

func TestMostMarkersWinsAndIsStable(t *testing.T) {
	// v1 took whichever language its directory listing reached first, so
	// a polyglot repo's answer depended on filesystem order.
	set := plugins(t, map[string]string{
		"python": pythonPlugin,
		"golang": goPlugin,
	})
	dir := project(t, map[string]string{
		"go.mod":           "module x\n\ngo 1.26\n",
		"main.go":          "package main\n",
		"requirements.txt": "flask\n",
	})

	first := Detect(dir, set)
	if first.Language.Name != "golang" {
		t.Fatalf("language = %q, want the one with two markers", first.Language.Name)
	}
	for i := 0; i < 20; i++ {
		if got := Detect(dir, set); got.Language.Name != first.Language.Name {
			t.Fatalf("detection unstable: %q then %q", first.Language.Name, got.Language.Name)
		}
	}
}

func TestTiesBreakByNameNotFilesystemOrder(t *testing.T) {
	set := plugins(t, map[string]string{
		"python": pythonPlugin,
		"node":   nodePlugin,
	})
	dir := project(t, map[string]string{
		"requirements.txt": "flask\n",
		"package.json":     "{}",
	})
	// One marker each: the name decides, and it must not wobble.
	got := Detect(dir, set)
	if got.Language.Name != "node" {
		t.Fatalf("language = %q, want the alphabetically first", got.Language.Name)
	}
}

func TestGlobMarkers(t *testing.T) {
	set := plugins(t, map[string]string{
		"bash": "name: bash\nversions: [\"5\"]\ndetection:\n  files: ['*.sh']\n",
	})
	if !Detect(project(t, map[string]string{"run.sh": "echo hi"}), set).Found() {
		t.Error("glob marker did not match")
	}
	if Detect(project(t, map[string]string{"run.py": "x"}), set).Found() {
		t.Error("glob matched the wrong file")
	}
}

func TestDirectoryDoesNotCountAsAMarker(t *testing.T) {
	// A directory named go.mod is not a go module.
	set := plugins(t, map[string]string{"golang": goPlugin})
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "go.mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if Detect(dir, set).Found() {
		t.Error("a directory satisfied a file marker")
	}
}

func TestExplainNamesTheEvidence(t *testing.T) {
	set := plugins(t, map[string]string{"golang": goPlugin})
	dir := project(t, map[string]string{"go.mod": "module x\n\ngo 1.26\n"})

	got := Detect(dir, set).Explain()
	for _, want := range []string{"golang", "1.26", "go.mod"} {
		if !strings.Contains(got, want) {
			t.Errorf("Explain() = %q, missing %q", got, want)
		}
	}
}

func TestExplicitPortsOverrideTheLanguage(t *testing.T) {
	set := plugins(t, map[string]string{"python": pythonPlugin})
	res := Detect(project(t, map[string]string{"requirements.txt": "x"}), set)

	if got := Ports("", res); len(got) != 2 || got[0] != 8000 {
		t.Fatalf("default ports = %v", got)
	}
	got := Ports("3000, 9999", res)
	if len(got) != 2 || got[0] != 3000 || got[1] != 9999 {
		t.Fatalf("explicit ports = %v", got)
	}
}

func TestPortsIgnoresGarbage(t *testing.T) {
	res := Result{Ports: []int{8000}}
	got := Ports("not-a-port, 3000, 99999, -1, ", res)
	if len(got) != 1 || got[0] != 3000 {
		t.Fatalf("ports = %v, want only the valid one", got)
	}
}

func TestBrokenPluginIsReportedNotSilentlyIgnored(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	// Declares no detection files, so it could never match anything.
	if err := os.WriteFile(filepath.Join(bad, "language.yaml"),
		[]byte("name: broken\nversions: [\"1\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := langs.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if set.Len() != 0 {
		t.Errorf("unusable plugin loaded")
	}
	if len(set.Notes) == 0 {
		t.Error("broken plugin vanished without a note")
	}
}

func TestRealPluginsFromTheRepoLoad(t *testing.T) {
	// The plugins that ship with v1 must load unchanged: the format is
	// preserved, only the reading of it is new.
	set, err := langs.Load("../../../languages")
	if err != nil {
		t.Skipf("languages dir unavailable: %v", err)
	}
	if set.Len() == 0 {
		t.Skip("no plugins found")
	}
	for _, note := range set.Notes {
		t.Errorf("shipped plugin failed to load: %s", note)
	}
	for _, name := range []string{"python", "node", "golang", "rust"} {
		if _, ok := set.Get(name); !ok {
			t.Errorf("plugin %q did not load, got %v", name, set.Names())
		}
	}
}
