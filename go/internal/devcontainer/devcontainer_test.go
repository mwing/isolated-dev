package devcontainer

import (
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
	dir := write(t, `{"image": "a/b:1", "forwardPorts": [3000], "workspaceFolder": "/workspace"}`)
	path, _ := Find(dir)
	c, _ := Load(path)
	if got := c.Ignored(); len(got) != 0 {
		t.Errorf("Ignored() = %v, want nothing", got)
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
