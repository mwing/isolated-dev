package trust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, root, project string) *Store {
	t.Helper()
	s, err := Load(root, project)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoadMissingFilesIsEmptyNotAnError(t *testing.T) {
	s := load(t, t.TempDir(), "/proj/a")
	if cfg := s.Resolve("claude"); len(cfg.AllowHosts) != 0 {
		t.Errorf("fresh store granted %v", cfg.AllowHosts)
	}
}

func TestGrantPersistsPerProject(t *testing.T) {
	root := t.TempDir()
	s := load(t, root, "/proj/a")

	added, err := s.Grant(s.Project, "claude", []string{"internal.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 {
		t.Fatalf("added = %v", added)
	}

	again := load(t, root, "/proj/a")
	if got := again.Resolve("claude").AllowHosts; len(got) != 1 || got[0] != "internal.example.com" {
		t.Fatalf("after reload = %v", got)
	}

	// A grant in one project must not follow the user into another.
	other := load(t, root, "/proj/b")
	if got := other.Resolve("claude").AllowHosts; len(got) != 0 {
		t.Fatalf("grant leaked to another project: %v", got)
	}
}

func TestGlobalAppliesEverywhereAndProjectAdds(t *testing.T) {
	root := t.TempDir()
	g := load(t, root, "/proj/a")
	if _, err := g.Grant(g.Global, "claude", []string{"registry.corp.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Grant(g.Project, "claude", []string{"proj-only.example.com"}); err != nil {
		t.Fatal(err)
	}

	// The project layer adds to the global one rather than replacing it:
	// a project should not have to restate company-wide destinations.
	a := load(t, root, "/proj/a").Resolve("claude").AllowHosts
	if len(a) != 2 {
		t.Fatalf("project A = %v, want both", a)
	}

	b := load(t, root, "/proj/b").Resolve("claude").AllowHosts
	if len(b) != 1 || b[0] != "registry.corp.example.com" {
		t.Fatalf("project B = %v, want only the global grant", b)
	}
}

func TestDefaultKeyAppliesToEveryAgent(t *testing.T) {
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	if _, err := s.Grant(s.Project, "default", []string{"shared.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(s.Project, "claude", []string{"claude-only.example.com"}); err != nil {
		t.Fatal(err)
	}

	reloaded := load(t, root, "/proj/a")
	claude := reloaded.Resolve("claude").AllowHosts
	if len(claude) != 2 {
		t.Fatalf("claude = %v, want default + its own", claude)
	}
	codex := reloaded.Resolve("codex").AllowHosts
	if len(codex) != 1 || codex[0] != "shared.example.com" {
		t.Fatalf("codex = %v, want only the default", codex)
	}
}

func TestScalarOverridesLayerLastWins(t *testing.T) {
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	s.Global.Agents = map[string]AgentConfig{"claude": {Memory: "2g", Base: "node:22"}}
	if err := s.Global.Save(); err != nil {
		t.Fatal(err)
	}
	s.Project.Agents = map[string]AgentConfig{"claude": {Memory: "8g"}}
	if err := s.Project.Save(); err != nil {
		t.Fatal(err)
	}

	cfg := load(t, root, "/proj/a").Resolve("claude")
	if cfg.Memory != "8g" {
		t.Errorf("memory = %q, want the project value", cfg.Memory)
	}
	if cfg.Base != "node:22" {
		t.Errorf("base = %q, want the global value to survive", cfg.Base)
	}
}

func TestProjectsWithSameBasenameDoNotCollide(t *testing.T) {
	root := t.TempDir()
	one := ProjectPath(root, "/home/me/work/api")
	two := ProjectPath(root, "/home/me/personal/api")
	if one == two {
		t.Fatalf("both resolved to %s", one)
	}
	// Both keep the readable slug so the directory can be browsed.
	if !strings.Contains(filepath.Base(one), "api") {
		t.Errorf("filename lost the slug: %s", one)
	}
}

func TestPathsNormalize(t *testing.T) {
	root := t.TempDir()
	want := ProjectPath(root, "/proj/a")
	for _, variant := range []string{"/proj/a/", "/proj/a/.", "/proj/b/../a"} {
		if got := ProjectPath(root, variant); got != want {
			t.Errorf("%q -> %s, want %s", variant, got, want)
		}
	}
}

func TestGrantReportsOnlyNewEntries(t *testing.T) {
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	if _, err := s.Grant(s.Project, "claude", []string{"one.example.com"}); err != nil {
		t.Fatal(err)
	}
	added, err := s.Grant(s.Project, "claude", []string{"one.example.com", "two.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "two.example.com" {
		t.Fatalf("added = %v, want only the new one", added)
	}
}

func TestRevoke(t *testing.T) {
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	if _, err := s.Grant(s.Project, "claude", []string{"one.example.com", "two.example.com"}); err != nil {
		t.Fatal(err)
	}
	removed, err := s.Revoke(s.Project, "claude", []string{"one.example.com", "never.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "one.example.com" {
		t.Fatalf("removed = %v", removed)
	}
	if got := load(t, root, "/proj/a").Resolve("claude").AllowHosts; len(got) != 1 {
		t.Fatalf("remaining = %v", got)
	}
}

func TestFilesAreUserOnly(t *testing.T) {
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	if _, err := s.Grant(s.Project, "claude", []string{"one.example.com"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.Project.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func TestConfigLivesOutsideTheProject(t *testing.T) {
	// The security property the whole layout exists for: nothing is ever
	// written into the project directory, so a clone cannot carry grants.
	root := t.TempDir()
	project := t.TempDir()

	s := load(t, root, project)
	if _, err := s.Grant(s.Project, "claude", []string{"one.example.com"}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("wrote into the project directory: %v", entries)
	}
	if !strings.HasPrefix(s.Project.Path(), root) {
		t.Errorf("project config at %s, expected under %s", s.Project.Path(), root)
	}
}

func TestProjectFilesListsEveryProject(t *testing.T) {
	// A grant nobody can see is a grant nobody will revoke.
	root := t.TempDir()
	for _, p := range []string{"/proj/a", "/proj/b"} {
		s := load(t, root, p)
		if _, err := s.Grant(s.Project, "claude", []string{"x.example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	files, err := ProjectFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v, want 2", files)
	}
	f, err := ReadProjectFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if f.Project == "" {
		t.Error("file does not record which project it belongs to")
	}
}

func TestTemplateIsValidYAMLAndSelfExplaining(t *testing.T) {
	tmpl := Template("/proj/a", "claude", []string{"api.anthropic.com", "github.com"})
	if !strings.Contains(tmpl, "claude:") || !strings.Contains(tmpl, "allow_hosts") {
		t.Errorf("template missing the fields it should teach:\n%s", tmpl)
	}
	// The built-in allowlist must be visible, or the user edits blind and
	// re-adds destinations that are already permitted.
	for _, h := range []string{"api.anthropic.com", "github.com"} {
		if !strings.Contains(tmpl, h) {
			t.Errorf("template does not show the built-in host %q:\n%s", h, tmpl)
		}
	}
	if strings.Contains(tmpl, "\n  - api.anthropic.com") {
		t.Error("built-in hosts must be comments, not active entries")
	}
	// Editing a fresh file must not produce a parse error on the next run.
	dir := t.TempDir()
	path := filepath.Join(dir, "t.yaml")
	if err := os.WriteFile(path, []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(path); err != nil {
		t.Fatalf("template does not parse: %v", err)
	}
}

func TestMalformedFileIsAnError(t *testing.T) {
	root := t.TempDir()
	path := GlobalPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("agents: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "/proj/a"); err == nil {
		t.Fatal("expected an error rather than silently granting nothing")
	}
}

func TestProjectRequestGrantsNothingUntilAccepted(t *testing.T) {
	// The property the whole split exists for: a repository can ask, but
	// only the user can authorize.
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	requested := AgentConfig{AllowHosts: []string{"internal.example.com", "mirror.example.com"}}

	pending := s.Pending("claude", requested)
	if len(pending) != 2 {
		t.Fatalf("pending = %v, want both unaccepted", pending)
	}
	if got := s.AcceptedRequest("claude", requested); len(got) != 0 {
		t.Fatalf("unaccepted request took effect: %v", got)
	}
}

func TestAcceptRecordsConsentOutsideTheProject(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	s := load(t, root, project)
	requested := AgentConfig{AllowHosts: []string{"internal.example.com"}}

	added, err := s.Accept("claude", s.Pending("claude", requested))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 {
		t.Fatalf("added = %v", added)
	}

	reloaded := load(t, root, project)
	if len(reloaded.Pending("claude", requested)) != 0 {
		t.Error("acceptance did not persist")
	}
	if got := reloaded.AcceptedRequest("claude", requested); len(got) != 1 {
		t.Fatalf("accepted request not applied: %v", got)
	}

	entries, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("consent was written into the project: %v", entries)
	}
}

func TestPartialAcceptanceLeavesTheRestPending(t *testing.T) {
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	requested := AgentConfig{AllowHosts: []string{"one.example.com", "two.example.com"}}

	if _, err := s.Accept("claude", []string{"one.example.com"}); err != nil {
		t.Fatal(err)
	}
	pending := s.Pending("claude", requested)
	if len(pending) != 1 || pending[0] != "two.example.com" {
		t.Fatalf("pending = %v", pending)
	}
	if got := s.AcceptedRequest("claude", requested); len(got) != 1 || got[0] != "one.example.com" {
		t.Fatalf("effective = %v, want only the accepted host", got)
	}
}

func TestDroppingAHostFromTheRequestStopsApplyingIt(t *testing.T) {
	// Acceptance authorizes; the request still decides. A host the project
	// no longer asks for must not linger because it was once accepted.
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	if _, err := s.Accept("claude", []string{"old.example.com", "kept.example.com"}); err != nil {
		t.Fatal(err)
	}

	narrowed := AgentConfig{AllowHosts: []string{"kept.example.com"}}
	got := s.AcceptedRequest("claude", narrowed)
	if len(got) != 1 || got[0] != "kept.example.com" {
		t.Fatalf("effective = %v, want only what is still requested", got)
	}
}

func TestNewRequestAfterAcceptanceIsPendingAgain(t *testing.T) {
	// The project adding a host later must re-prompt, or acceptance would
	// be a blank cheque for every future edit.
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	if _, err := s.Accept("claude", []string{"one.example.com"}); err != nil {
		t.Fatal(err)
	}

	widened := AgentConfig{AllowHosts: []string{"one.example.com", "sneaky.example.com"}}
	pending := s.Pending("claude", widened)
	if len(pending) != 1 || pending[0] != "sneaky.example.com" {
		t.Fatalf("pending = %v, want the newly added host", pending)
	}
}

func TestDirectGrantSatisfiesARequest(t *testing.T) {
	// If the user already granted a destination themselves, asking them to
	// accept it again is pedantry rather than safety.
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	if _, err := s.Grant(s.Project, "claude", []string{"internal.example.com"}); err != nil {
		t.Fatal(err)
	}
	requested := AgentConfig{AllowHosts: []string{"internal.example.com"}}
	if pending := s.Pending("claude", requested); len(pending) != 0 {
		t.Fatalf("pending = %v, want none", pending)
	}
}

func TestSettingsNeedConsentAndAreValueScoped(t *testing.T) {
	root := t.TempDir()
	s := load(t, root, "/proj/a")
	ask := Ask{Key: "network", Value: "open", Effect: "turn off egress filtering"}

	if len(s.PendingSettings([]Ask{ask})) != 1 {
		t.Fatal("an unaccepted setting should be pending")
	}
	if _, err := s.AcceptSettings([]Ask{ask}); err != nil {
		t.Fatal(err)
	}
	if len(s.PendingSettings([]Ask{ask})) != 0 {
		t.Fatal("acceptance did not take")
	}

	// The VALUE is what was accepted. A project that later asks for
	// something else must ask again rather than inherit an old yes.
	other := Ask{Key: "network", Value: "host", Effect: "different"}
	if len(s.PendingSettings([]Ask{other})) != 1 {
		t.Fatal("a changed value should be pending again")
	}
}

func TestAcceptedSettingsPersistOutsideTheProject(t *testing.T) {
	root := t.TempDir()
	proj := t.TempDir()
	s := load(t, root, proj)
	ask := Ask{Key: "mount_docker_socket", Value: "true"}

	if _, err := s.AcceptSettings([]Ask{ask}); err != nil {
		t.Fatal(err)
	}
	if len(load(t, root, proj).PendingSettings([]Ask{ask})) != 0 {
		t.Error("acceptance did not persist")
	}
	entries, err := os.ReadDir(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("consent written into the project: %v", entries)
	}
}
