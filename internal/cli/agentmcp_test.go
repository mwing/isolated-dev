package cli

import (
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"strings"
	"testing"
)

// The leak this closes: an agent running untrusted code could reach the
// user's cloud connectors — Gmail, Linear — because the host they route
// through was in the default allowlist and the config volume carries their
// tokens. Off by default now: the connector host is not allowed, and the
// agent is told to ignore inherited MCP config (which also stops a hostile
// repo's own .mcp.json in the clone).
func TestAgentRunBlocksMCPByDefault(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, h.stderr.String())
	}

	if contains(h.sidecarAllow(t), "mcp-proxy.anthropic.com") {
		t.Errorf("the connector host is allowed by default: %v", h.sidecarAllow(t))
	}
	if !contains(h.workloadRun(t), "--strict-mcp-config") {
		t.Errorf("the agent was not told to ignore inherited MCP:\n%s",
			argv(h.workloadRun(t)))
	}
}

func TestAllowMCPFlagTurnsConnectorsOn(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	if err := h.run(t, "agent", "run", "claude", "--allow-mcp", "--tty", "off"); err != nil {
		t.Fatalf("agent run --allow-mcp: %v\n%s", err, h.stderr.String())
	}

	if !contains(h.sidecarAllow(t), "mcp-proxy.anthropic.com") {
		t.Errorf("--allow-mcp did not allow the connector host: %v", h.sidecarAllow(t))
	}
	if contains(h.workloadRun(t), "--strict-mcp-config") {
		t.Errorf("--allow-mcp still ignored MCP config:\n%s", argv(h.workloadRun(t)))
	}
}

// The console-agent path has the same connectors and the same leak, so it
// blocks by default too — the safe default must not depend on which command
// started the agent.
func TestConsoleAgentBlocksMCPByDefault(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	// The console needs a terminal to draw; -c with no tty runs the agent
	// non-interactively, which is enough to inspect the workload run.
	_ = h.run(t, "console", "--agent", "claude")

	for _, run := range h.runsWithRole("agent") {
		if !contains(run, "--strict-mcp-config") {
			t.Errorf("console --agent did not block MCP by default:\n%s", argv(run))
		}
	}
	if contains(h.sidecarAllow(t), "mcp-proxy.anthropic.com") {
		t.Errorf("console --agent allowed the connector host by default: %v",
			h.sidecarAllow(t))
	}
}

// The flag exists on both commands, or the block is a wall with no door on
// one of them.
func TestAllowMCPFlagIsOnBothCommands(t *testing.T) {
	h := newHarness(t)
	root := NewRootCmd(h.env)
	for _, path := range [][]string{{"agent", "run"}, {"console"}} {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if c.Flags().Lookup("allow-mcp") == nil {
			t.Errorf("`dev %s` has no --allow-mcp", strings.Join(path, " "))
		}
	}
}

// The gap a review found: --allow-mcp was one of several doors. `dev allow`
// of the connector host, a project `.devenv.yaml` requesting it plus `dev
// accept`, and `--allow-host` all re-added it to the sidecar allowlist
// without the dedicated gate — and the run's own summary prompts the user
// toward exactly those. The worst is the accept path: a hostile cloned repo
// can request the connector host, and a routine accept would hand over
// Gmail. The connector host is now grantable only through --allow-mcp.
func TestGrantingTheConnectorHostDoesNotReopenMCP(t *testing.T) {
	// The forms that must not slip past, including the two the string gate
	// missed: a port suffix and a wildcard both reach the connector.
	for _, tc := range []struct {
		name    string
		command []string
		host    string // what to grant, and to look for in the allowlist
	}{
		{"agent run --allow-host", []string{"agent", "run", "claude"},
			"mcp-proxy.anthropic.com"},
		{"agent run --allow-host :443", []string{"agent", "run", "claude"},
			"mcp-proxy.anthropic.com:443"},
		{"agent run --allow-host wildcard", []string{"agent", "run", "claude"},
			"*.anthropic.com"},
		{"console --allow-host", []string{"console", "--agent", "claude"},
			"mcp-proxy.anthropic.com"},
		{"console --allow-host :443", []string{"console", "--agent", "claude"},
			"mcp-proxy.anthropic.com:443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.readyBackend()
			h.readySidecar()
			gitProject(t, h)
			h.writeGlobal(t, "agent_clone: false\n")

			args := append(append([]string(nil), tc.command...), "--allow-host", tc.host)
			if tc.command[0] == "agent" {
				args = append(args, "--tty", "off")
			}
			_ = h.run(t, args...)

			// The precise question, asked with the sidecar's own rules: is
			// the connector reachable? A port suffix or a wildcard makes the
			// string absent while the rule still matches, which is the bug.
			al, err := netpolicy.Parse(h.sidecarAllow(t))
			if err != nil {
				t.Fatalf("parsing the sidecar allowlist: %v", err)
			}
			if al.AllowsName("mcp-proxy.anthropic.com") {
				t.Errorf("%s left the connector reachable without --allow-mcp:\n%v",
					tc.name, h.sidecarAllow(t))
			}
			if !strings.Contains(h.stderr.String(), "allow-mcp") {
				t.Errorf("%s dropped the host with no explanation:\n%s",
					tc.name, h.stderr.String())
			}
		})
	}
}

// A persistent `dev allow` of the connector host is likewise gated: the
// stored grant reaches the run through the same machinery as a project
// accept, so this stands for that path too.
func TestAPersistentAllowOfTheConnectorHostIsStillGated(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n")

	if err := h.run(t, "allow", "mcp-proxy.anthropic.com"); err != nil {
		t.Fatalf("dev allow: %v\n%s", err, h.stderr.String())
	}
	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v\n%s", err, h.stderr.String())
	}
	if contains(h.sidecarAllow(t), "mcp-proxy.anthropic.com") {
		t.Errorf("a stored `dev allow` of the connector host reopened it without "+
			"--allow-mcp:\n%v", h.sidecarAllow(t))
	}
}

// reachesConnector is the primitive under all three gate points — the agent
// allowlist, the console allowlist, and the console prompt. It matches by
// the sidecar's rules, so the forms that defeated a string compare are the
// ones it must catch.
func TestReachesConnectorMatchesByRuleNotString(t *testing.T) {
	mcp := []string{"mcp-proxy.anthropic.com"}
	for _, reach := range []string{
		"mcp-proxy.anthropic.com",
		"mcp-proxy.anthropic.com:443",
		"*.anthropic.com",
	} {
		if !reachesConnector(reach, mcp) {
			t.Errorf("%q reaches the connector but was not caught", reach)
		}
	}
	for _, safe := range []string{
		"api.anthropic.com",                // a real, unrelated host
		"mcp-proxy.anthropic.com.evil.com", // a lookalike, not the host
		"github.com",
	} {
		if reachesConnector(safe, mcp) {
			t.Errorf("%q does not reach the connector but was caught", safe)
		}
	}
}
