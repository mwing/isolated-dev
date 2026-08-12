package cli

import (
	"os"

	"github.com/mwing/isolated-dev/internal/config"
	"path/filepath"
	"strings"
	"testing"
)

// gitProject makes the harness project a repository, since a clone needs
// one and the fallback would otherwise hide what is being tested.
func gitProject(t *testing.T, h *harness) {
	t.Helper()
	h.realGit()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		if _, err := gitIn(t.Context(), h.env, h.paths.ProjectDir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(h.paths.ProjectDir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		if _, err := gitIn(t.Context(), h.env, h.paths.ProjectDir, args...); err != nil {
			t.Fatal(err)
		}
	}
}

// Someone who always runs agents interactively should set this once rather
// than type a flag forever.
func TestGlobalConfigCanTurnTheCloneDefaultOff(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, h.stderr.String())
	}
	if strings.Contains(argv(h.workloadRun(t)), "clones") {
		t.Fatal("agent_clone: false was ignored; the run used a clone")
	}
}

// A flag still wins over configuration, in both directions.
func TestAFlagOverridesTheConfiguredDefault(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	if err := h.run(t, "agent", "run", "claude", "--clone", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, h.stderr.String())
	}
	if !strings.Contains(argv(h.workloadRun(t)), "clones") {
		t.Fatal("--clone did not override agent_clone: false")
	}
}

// The same setting in a project file is a request, because turning the
// clone off is a repository asking to edit the files you are editing.
func TestAProjectCannotTurnTheCloneDefaultOffWithoutConsent(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeProject(t, "agent_clone: false\n")

	err := h.run(t, "agent", "run", "claude", "--tty", "off")
	if err == nil {
		t.Fatal("a project turned off the clone default without being accepted")
	}
	if out := h.stderr.String(); !strings.Contains(out, "agent_clone") {
		t.Fatalf("the refusal does not name the setting:\n%s", out)
	}
}

// Accepted, it applies — the point of the mechanism is that the decision is
// the user's and it is remembered.
func TestAnAcceptedProjectSettingApplies(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeProject(t, "agent_clone: false\n")
	if err := h.run(t, "accept", "--all"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	h.stderr.Reset()

	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("agent run after accepting: %v\n%s", err, h.stderr.String())
	}
	if strings.Contains(argv(h.workloadRun(t)), "clones") {
		t.Fatal("an accepted agent_clone: false was not applied")
	}
}

// The strengthening direction needs no consent: a project asking for the
// default it already gets should not produce a prompt, because prompts that
// ask nothing are how people learn to accept the ones that do.
func TestAProjectAskingForTheCloneDefaultNeedsNoConsent(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeProject(t, "agent_clone: true\n")

	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("a project asking for the default was stopped: %v\n%s", err, h.stderr.String())
	}
}

// `dev console --agent X` used to be the one way to start an agent that
// mounted the working tree. What makes the clone right is who is driving —
// a model rather than the person in the room — not which view they are
// watching through, and the console documents itself as a view over the
// same run rather than a second way to start one.
func TestConsoleClonesForAnAgentByDefault(t *testing.T) {
	def := config.Config{AgentClone: true}
	for _, tc := range []struct {
		name  string
		cfg   config.Config
		agent string
		cl    cloneOpts
		want  bool
	}{
		{"an agent clones with no flag", def, "claude", cloneOpts{}, true},
		{"--in-place opts out", def, "claude", cloneOpts{inPlace: true}, false},
		{"--clone wins over agent_clone: false",
			config.Config{AgentClone: false}, "claude", cloneOpts{use: true}, true},
		{"agent_clone: false is honoured",
			config.Config{AgentClone: false}, "claude", cloneOpts{}, false},

		// A console with no agent is a person running their own command,
		// which is the case the plain mount is right for.
		{"no agent stays in the working tree", def, "", cloneOpts{}, false},
		{"no agent, --clone opts in", def, "", cloneOpts{use: true}, true},
		{"--clone-depth implies --clone", def, "", cloneOpts{depth: 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := consoleWantsClone(tc.cfg, tc.agent, tc.cl); got != tc.want {
				t.Errorf("consoleWantsClone = %v, want %v", got, tc.want)
			}
		})
	}
}

// The flags have to exist for the behaviour above to be reachable, and
// `dev console` was the only workspace command without them.
func TestConsoleHasTheCloneFlags(t *testing.T) {
	h := newHarness(t)
	root := NewRootCmd(h.env)
	c, _, err := root.Find([]string{"console"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clone", "clone-depth", "in-place"} {
		if c.Flags().Lookup(name) == nil {
			t.Errorf("`dev console` has no --%s", name)
		}
	}
}
