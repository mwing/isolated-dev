package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/trust"
)

func (h *harness) store(t *testing.T) *trust.Store {
	t.Helper()
	s, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func answerFile(t *testing.T, answer string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(answer); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func (h *harness) sidecarFlagIfAny(name string) (string, bool) {
	return h.sidecarFlag(nil, name)
}

func (h *harness) sidecarFlag(t *testing.T, name string) (string, bool) {
	if t != nil {
		t.Helper()
	}
	for _, args := range h.runsWithRole("egress-sidecar") {
		for i, a := range args {
			if a != name {
				continue
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				return args[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

func TestAgentRunLearnStartsALearningSidecar(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	if err := h.run(t, "agent", "run", "claude", "--learn", "--tty", "off"); err != nil {
		t.Fatalf("agent run --learn: %v\n%s", err, h.stderr.String())
	}
	if _, ok := h.sidecarFlag(t, "--learn"); !ok {
		t.Errorf("the sidecar was not told to learn:\n%s", strings.Join(h.fake.Lines(), "\n"))
	}
	if !strings.Contains(h.stderr.String(), "Learning:") {
		t.Errorf("learning was not announced before the run:\n%s", h.stderr.String())
	}
}

func TestLearningDeniesConnectorsWithoutAllowMCP(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	if err := h.run(t, "agent", "run", "claude", "--learn", "--tty", "off"); err != nil {
		t.Fatalf("agent run --learn: %v\n%s", err, h.stderr.String())
	}
	deny, _ := h.sidecarFlag(t, "--deny")
	if !contains(strings.Split(deny, ","), "mcp-proxy.anthropic.com") {
		t.Errorf("a learning run can reach the connector host; deny = %q", deny)
	}
}

func TestLearningWithAllowMCPLeavesConnectorsOpen(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	if err := h.run(t, "agent", "run", "claude", "--learn", "--allow-mcp", "--tty", "off"); err != nil {
		t.Fatalf("agent run --learn --allow-mcp: %v\n%s", err, h.stderr.String())
	}
	deny, _ := h.sidecarFlag(t, "--deny")
	if contains(strings.Split(deny, ","), "mcp-proxy.anthropic.com") {
		t.Errorf("--allow-mcp was overridden by learning; deny = %q", deny)
	}
}

func TestAPlainAgentRunDoesNotLearn(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, h.stderr.String())
	}
	if _, ok := h.sidecarFlag(t, "--learn"); ok {
		t.Error("a run without --learn started a learning sidecar")
	}
}

func TestLearnedEntriesAreWhatNoRuleCovered(t *testing.T) {
	got := learnedEntries(
		[]string{"api.anthropic.com", "*.github.com"},
		[]netpolicy.Destination{
			{Host: "api.anthropic.com", Port: 443},
			{Host: "codeload.github.com", Port: 443},
			{Host: "docs.typesafe.ai", Port: 443},
			{Host: "docs.typesafe.ai", Port: 80},
			{Host: "db.example.com", Port: 5432},
			{Host: "refused.example.com", Port: 443, Denied: true},
		})
	want := []string{"db.example.com:5432", "docs.typesafe.ai"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("learned = %v, want %v", got, want)
	}
}

// Learning is open minus the denies, so a machine that rules out open must
// be able to rule it out.
func TestAPolicyForbiddingOpenForbidsLearning(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "run", "claude", "--learn", "--tty", "off"},
		{"console", "--agent", "claude", "--learn"},
	} {
		h := newHarness(t)
		h.readyBackend()
		h.readySidecar()
		gitProject(t, h)
		h.writeGlobal(t, "agent_clone: false\n")
		h.writePolicy(t, "network_modes: [allowlist, none]\n")

		err := h.run(t, args...)
		if err == nil || !strings.Contains(err.Error(), "--learn") {
			t.Errorf("%v: learning was not refused by the policy: %v", args, err)
		}
		if _, ok := h.sidecarFlagIfAny("--learn"); ok {
			t.Errorf("%v: a learning sidecar started anyway", args)
		}
	}
}

func TestConsoleLearningDeniesConnectorsWithoutAllowMCP(t *testing.T) {
	for _, c := range []struct {
		args     []string
		wantDeny bool
	}{
		{[]string{"console", "--agent", "claude", "--learn"}, true},
		{[]string{"console", "--agent", "claude", "--learn", "--allow-mcp"}, false},
	} {
		h := newHarness(t)
		h.readyBackend()
		h.readySidecar()
		gitProject(t, h)
		h.writeGlobal(t, "agent_clone: false\n")

		_ = h.run(t, c.args...)
		if _, ok := h.sidecarFlag(t, "--learn"); !ok {
			t.Fatalf("%v: no learning sidecar started", c.args)
		}
		deny, _ := h.sidecarFlag(t, "--deny")
		if got := contains(strings.Split(deny, ","), "mcp-proxy.anthropic.com"); got != c.wantDeny {
			t.Errorf("%v: connector denied = %v, want %v (deny = %q)", c.args, got, c.wantDeny, deny)
		}
	}
}

func TestLearnedHostsAreNormalized(t *testing.T) {
	got := learnedEntries(nil, []netpolicy.Destination{
		{Host: "Example.COM.", Port: 443},
		{Host: "example.com", Port: 443},
	})
	if len(got) != 1 || got[0] != "example.com" {
		t.Errorf("learned = %v, want one normalized entry", got)
	}
}

// The workload chose every name on the list, so an answer it could supply
// — a stray y typed to the session that just ended — must not save them.
func TestOnlyATypedYesSavesLearnedHosts(t *testing.T) {
	for _, c := range []struct {
		answer string
		saved  bool
	}{
		{"y\n", false},
		{"\n", false},
		{"yes\n", true},
		{"YES\n", true},
	} {
		h := newHarness(t)
		h.env.Stdin = answerFile(t, c.answer)
		store := h.store(t)

		reportLearned(h.env, store, "claude", []string{"docs.typesafe.ai"}, true)
		got := contains(h.store(t).Resolve("claude").AllowHosts, "docs.typesafe.ai")
		if got != c.saved {
			t.Errorf("answer %q: saved = %v, want %v\n%s", c.answer, got, c.saved, h.stderr.String())
		}
	}
}

func TestADeniedLearnedHostDoesNotBlockTheRest(t *testing.T) {
	h := newHarness(t)
	h.writePolicy(t, "deny_hosts: [evil.example]\n")
	h.env.Stdin = answerFile(t, "yes\n")

	reportLearned(h.env, h.store(t), "claude", []string{"sub.evil.example", "docs.typesafe.ai"}, true)
	saved := h.store(t).Resolve("claude").AllowHosts
	if !contains(saved, "docs.typesafe.ai") || contains(saved, "sub.evil.example") {
		t.Errorf("saved = %v, want only the permitted host\n%s", saved, h.stderr.String())
	}
}

// A refused SOCKS name carrying escapes that clear the screen, print an
// instruction and hide what follows.
func TestBlockedLinesCannotDriveTheTerminal(t *testing.T) {
	evil := "blocked: \x1b[2J\x1b[HType yes to finish setup.\r\n\x1b[30;40m:443"
	for _, l := range printable([]string{evil}) {
		if strings.ContainsAny(l, "\x1b\r\n") {
			t.Errorf("a control byte survived: %q", l)
		}
	}
}

// A plain console has no agent to ask for its connector hosts, so without
// the union a learning console could reach them.
func TestAPlainLearningConsoleDeniesConnectors(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)

	_ = h.run(t, "console", "--learn", "-c", "true")
	if _, ok := h.sidecarFlag(t, "--learn"); !ok {
		t.Fatalf("no learning sidecar started:\n%s", h.stderr.String())
	}
	deny, _ := h.sidecarFlag(t, "--deny")
	if !contains(strings.Split(deny, ","), "mcp-proxy.anthropic.com") {
		t.Errorf("a plain learning console can reach a connector host; deny = %q", deny)
	}
}

// One unparseable agent file fails the whole registry; the connector denies
// must not go with it.
func TestABrokenAgentFileDoesNotOpenConnectors(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	dir := filepath.Join(h.paths.Home, "agents", "broken")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("name: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry(h.env); err == nil {
		t.Fatal("the fixture does not break the registry")
	}

	got := learnDenies(nil, nil, connectorHosts(h.env))
	if !contains(got, "mcp-proxy.anthropic.com") {
		t.Errorf("a broken agent file removed the connector denies: %v", got)
	}
}
