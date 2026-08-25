package devcontainer

import (
	"fmt"

	"github.com/mwing/isolated-dev/internal/container"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	dc := filepath.Join(dir, ".devcontainer")
	if err := os.MkdirAll(dc, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dc, "devcontainer.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParsesRealWorldJSONC(t *testing.T) {
	// devcontainer.json is JSON with C comments and trailing commas, both
	// of which encoding/json rejects. A real file fails to parse without
	// handling them, which would look like "no devcontainer support".
	dir := write(t, `{
  // The image to use
  "name": "Example",
  "image": "mcr.microsoft.com/devcontainers/go:1.22", /* inline */
  "forwardPorts": [3000, 8080],
}`)
	path, ok := Find(dir)
	if !ok {
		t.Fatal("file not found")
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Image != "mcr.microsoft.com/devcontainers/go:1.22" {
		t.Errorf("image = %q", c.Image)
	}
	if len(c.ForwardPorts) != 2 || c.ForwardPorts[0] != 3000 {
		t.Errorf("ports = %v", c.ForwardPorts)
	}
}

func TestCommentMarkersInsideStringsSurvive(t *testing.T) {
	// A URL contains // and stripping it would corrupt the value.
	dir := write(t, `{"name": "https://example.com/x", "image": "a/b:1"}`)
	path, _ := Find(dir)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "https://example.com/x" {
		t.Fatalf("name = %q, a URL was mangled by comment stripping", c.Name)
	}
}

func TestDockerfileResolvesRelativeToTheFile(t *testing.T) {
	// The spec resolves paths against the devcontainer.json's directory,
	// which is why "../Dockerfile" is the common spelling.
	dir := write(t, `{"build": {"dockerfile": "../Dockerfile"}}`)
	path, _ := Find(dir)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.DockerfilePath(); got != filepath.Join(dir, "Dockerfile") {
		t.Fatalf("DockerfilePath = %q, want %q", got, filepath.Join(dir, "Dockerfile"))
	}
}

func TestOlderDockerFileSpellingStillWorks(t *testing.T) {
	dir := write(t, `{"dockerFile": "../Dockerfile"}`)
	path, _ := Find(dir)
	c, _ := Load(path)
	if c.DockerfilePath() == "" {
		t.Error("the older dockerFile key was ignored")
	}
}

func TestIgnoredPartsAreNamed(t *testing.T) {
	// A config half-honored silently is worse than one not read at all:
	// the user believes the file describes what is running.
	dir := write(t, `{
  "image": "a/b:1",
  "containerEnv": {"AWS_PROFILE": "dev", "TOKEN": "x"},
  "mounts": ["source=/host,target=/c,type=bind"],
  "remoteUser": "vscode",
  "postCreateCommand": "npm install",
  "workspaceFolder": "/code"
}`)
	path, _ := Find(dir)
	c, _ := Load(path)

	got := strings.Join(c.Ignored(), "\n")
	for _, want := range []string{
		"containerEnv", "AWS_PROFILE", "mounts", "remoteUser",
		"postCreateCommand", "/code",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Ignored() does not mention %q:\n%s", want, got)
		}
	}
}

func TestNothingIgnoredWhenNothingUnsupported(t *testing.T) {
	dir := write(t, `{"image": "a/b:1", "workspaceFolder": "/workspace"}`)
	path, _ := Find(dir)
	c, _ := Load(path)
	if got := c.Ignored(); len(got) != 0 {
		t.Errorf("Ignored() = %v, want nothing", got)
	}
}

// forwardPorts used to be honored, which made a devcontainer.json the one
// file in the repository that could still publish a socket on the user's
// machine — the same request `.devenv.yaml` is refused, in the other file
// the repository owns. It joins containerEnv and mounts: a grant here,
// not a setting.
func TestForwardPortsIsIgnoredAndSaidSo(t *testing.T) {
	dir := write(t, `{"image": "a/b:1", "forwardPorts": [3000, 5000]}`)
	path, _ := Find(dir)
	c, _ := Load(path)
	got := strings.Join(c.Ignored(), "\n")
	if !strings.Contains(got, "forwardPorts") {
		t.Errorf("a devcontainer's forwardPorts is dropped without saying so:\n%s", got)
	}
}

func TestFindLooksInBothPlaces(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Find(dir); ok {
		t.Fatal("found a file that does not exist")
	}
	if err := os.WriteFile(filepath.Join(dir, ".devcontainer.json"),
		[]byte(`{"image":"a/b:1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Find(dir); !ok {
		t.Error("the root .devcontainer.json spelling was not found")
	}
}

func TestMalformedFileIsAnError(t *testing.T) {
	dir := write(t, `{"image": }`)
	path, _ := Find(dir)
	if _, err := Load(path); err == nil {
		t.Fatal("malformed file parsed")
	}
}

func TestGenerateUsesImageWhenGiven(t *testing.T) {
	g := Generate(Options{Name: "app", Image: "ghcr.io/acme/dev:1"})
	js := g.Files[filepath.Join(".devcontainer", "devcontainer.json")]
	if !strings.Contains(js, `"image": "ghcr.io/acme/dev:1"`) {
		t.Fatalf("image not referenced: %s", js)
	}
	if _, ok := g.Files[filepath.Join(".devcontainer", "Dockerfile")]; ok {
		t.Fatal("wrote a Dockerfile for a project that names an image")
	}
}

func TestGenerateShipsDockerfileWhenThereIsNoImage(t *testing.T) {
	g := Generate(Options{Name: "app", Dockerfile: "FROM debian\n"})
	df, ok := g.Files[filepath.Join(".devcontainer", "Dockerfile")]
	if !ok || df != "FROM debian\n" {
		t.Fatalf("Dockerfile not written: %q", df)
	}
	js := g.Files[filepath.Join(".devcontainer", "devcontainer.json")]
	if !strings.Contains(js, `"dockerfile": "Dockerfile"`) {
		t.Fatalf("Dockerfile not referenced: %s", js)
	}
}

// A generated file that an editor cannot parse is worse than none, and the
// comments this writes are the part a strict JSON parser would reject.
func TestGeneratedConfigParsesAsJSONC(t *testing.T) {
	g := Generate(Options{Name: "app", Dockerfile: "FROM debian\n", Ports: []int{8000, 5000}})
	path := filepath.Join(t.TempDir(), "devcontainer.json")
	if err := os.WriteFile(path,
		[]byte(g.Files[filepath.Join(".devcontainer", "devcontainer.json")]), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("cannot read back what we wrote: %v", err)
	}
	if cfg.Name != "app" {
		t.Fatalf("name = %q", cfg.Name)
	}
	if len(cfg.ForwardPorts) != 2 || cfg.ForwardPorts[0] != 8000 {
		t.Fatalf("ports = %v", cfg.ForwardPorts)
	}
	if cfg.WorkspaceFolder != "/workspace" {
		t.Fatalf("workspace = %q", cfg.WorkspaceFolder)
	}
}

// The exported container has ordinary network access. Someone who believes
// otherwise is worse off than someone who never had the file, so both the
// file and the command's output have to say it.
func TestGenerateSaysEgressIsNotReproduced(t *testing.T) {
	g := Generate(Options{Name: "app", Dockerfile: "FROM debian\n", EgressFiltered: true})
	js := g.Files[filepath.Join(".devcontainer", "devcontainer.json")]
	if !strings.Contains(js, "NOT reproduce") {
		t.Fatalf("config does not mention it: %s", js)
	}
	var found bool
	for _, n := range g.Notes {
		if strings.Contains(n, "egress filtering is not reproduced") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notes do not mention it: %v", g.Notes)
	}
}

func TestGenerateRunsAsTheSameUnprivilegedUser(t *testing.T) {
	js := Generate(Options{Name: "app", Dockerfile: "FROM debian\n"}).
		Files[filepath.Join(".devcontainer", "devcontainer.json")]
	for _, want := range []string{fmt.Sprintf(`"remoteUser": "%d"`, container.HostUID()), "--cap-drop=ALL", "no-new-privileges"} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %q in %s", want, js)
		}
	}
}
