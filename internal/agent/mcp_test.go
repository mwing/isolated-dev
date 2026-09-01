package agent

import (
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/netpolicy"
)

func mcpAgent() *Agent {
	return &Agent{
		Name: "claude", Binary: "claude", ConfigDir: "/home/dev/.claude",
		Args:       []string{"--dangerously-skip-permissions"},
		AllowHosts: []string{"api.anthropic.com"},
		MCPHosts:   []string{"mcp-proxy.anthropic.com"},
		MCPOffArgs: []string{"--strict-mcp-config"},
	}
}

// A connector reaches an account outside the sandbox — Gmail, with a live
// token in the config volume — so it is off unless asked for. Off means
// two things: the host it routes through is not in the allowlist, and the
// agent is told to ignore its inherited MCP config.
func TestMCPIsOffByDefault(t *testing.T) {
	a := mcpAgent()
	o := Options{Agent: a}

	if contains(o.Allowlist(), "mcp-proxy.anthropic.com") {
		t.Errorf("the connector host is in the default allowlist: %v", o.Allowlist())
	}
	cmd := strings.Join(Spec(o, netpolicy.Topology{}).Command, " ")
	if !strings.Contains(cmd, "--strict-mcp-config") {
		t.Errorf("the agent was not told to ignore inherited MCP config: %q", cmd)
	}
}

// --allow-mcp turns it on: the host joins the allowlist so the connector is
// reachable, and the ignore-MCP flag is dropped so the agent loads it.
func TestAllowMCPTurnsItOn(t *testing.T) {
	a := mcpAgent()
	o := Options{Agent: a, AllowMCP: true}

	if !contains(o.Allowlist(), "mcp-proxy.anthropic.com") {
		t.Errorf("--allow-mcp did not add the connector host: %v", o.Allowlist())
	}
	cmd := strings.Join(Spec(o, netpolicy.Topology{}).Command, " ")
	if strings.Contains(cmd, "--strict-mcp-config") {
		t.Errorf("--allow-mcp still told the agent to ignore MCP config: %q", cmd)
	}
}

// The connector host must not be reachable by default even for an agent
// that has no other reason to name it — the whole point is that it is not
// in the baseline allowlist. This guards the builtin definition against a
// future edit that puts it back.
func TestTheClaudeBuiltinKeepsTheConnectorHostOffTheAllowlist(t *testing.T) {
	var claude *Agent
	for _, a := range builtins() {
		if a.Name == "claude" {
			cp := a
			claude = &cp
		}
	}
	if claude == nil {
		t.Fatal("no claude builtin")
	}
	if contains(claude.AllowHosts, "mcp-proxy.anthropic.com") {
		t.Error("mcp-proxy.anthropic.com is back in the default allowlist; an " +
			"agent running untrusted code could reach the user's connectors")
	}
	if !contains(claude.MCPHosts, "mcp-proxy.anthropic.com") {
		t.Error("the connector host is not in MCPHosts, so --allow-mcp cannot " +
			"turn it back on")
	}
}
