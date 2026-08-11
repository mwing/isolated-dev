package agent

import (
	"strings"
	"testing"
)

// Version was declared, warned about when unpinned, and baked into the
// image tag — while the install command fetched whatever npm felt like. So
// the tag named a version nothing had installed, and two builds of the same
// "pinned" agent could differ. A pin that does not reach the fetch is
// decoration.
func TestThePinReachesTheInstallCommand(t *testing.T) {
	a := &Agent{
		Name: "demo", Binary: "demo", ConfigDir: "/c", Base: "b",
		Version: "1.2.3",
		Install: "npm install -g @scope/pkg@{{VERSION}}",
	}
	got := a.InstallCommand()
	if !strings.Contains(got, "@1.2.3") {
		t.Fatalf("install command = %q, want the pinned version in it", got)
	}
	if strings.Contains(got, "{{VERSION}}") {
		t.Fatalf("placeholder survived: %q", got)
	}
}

// An unset version installs latest rather than a literal placeholder,
// which would fail the build with a message about a version nobody wrote.
func TestAnUnsetVersionInstallsLatest(t *testing.T) {
	a := &Agent{Install: "npm install -g pkg@{{VERSION}}"}
	if got := a.InstallCommand(); got != "npm install -g pkg@latest" {
		t.Fatalf("install command = %q", got)
	}
}

// A definition that pins by other means is used as written.
func TestAnInstallWithoutThePlaceholderIsLeftAlone(t *testing.T) {
	a := &Agent{Version: "1.2.3", Install: "curl -sSf https://example.test/install.sh | sh"}
	if got := a.InstallCommand(); got != a.Install {
		t.Fatalf("install command was rewritten: %q", got)
	}
}

// The package name is what an image can be asked about, which is how the
// update command learns a version instead of guessing one.
func TestPackageNameIsReadFromTheInstallCommand(t *testing.T) {
	cases := map[string]string{
		"npm install -g @anthropic-ai/claude-code@{{VERSION}}": "@anthropic-ai/claude-code",
		"npm install -g @openai/codex@0.147.0":                 "@openai/codex",
		"npm install -g typescript":                            "typescript",
	}
	for install, want := range cases {
		a := &Agent{Install: install}
		if got := a.Package(); got != want {
			t.Errorf("Package() = %q for %q, want %q", got, install, want)
		}
	}
}

// The built-ins have to practise what `dev pin` preaches.
func TestBuiltInAgentsArePinned(t *testing.T) {
	for _, a := range NewRegistry().List() {
		if !a.Pinned() {
			t.Errorf("%s ships unpinned (version %q): rebuilding it could "+
				"produce a different agent", a.Name, a.Version)
		}
		if strings.Contains(a.Install, "npm install") &&
			!strings.Contains(a.Install, "{{VERSION}}") {
			t.Errorf("%s pins a version its install command ignores: %q",
				a.Name, a.Install)
		}
	}
}
