package agent

// builtins are the agents that ship with the tool. They are ordinary
// definitions with no special status: a user file at
// ~/.dev-envs/agents/<name>/agent.yaml replaces one entirely.
//
// Allowlists are deliberately tight. Each host is here because the agent
// cannot function without it, and every entry is a destination the agent
// can also write to — see ROADMAP 4.4 on what an allowlist does and does
// not contain.
func builtins() []Agent {
	return []Agent{
		{
			Name:        "claude",
			Description: "Claude Code (Anthropic)",
			// Pinned to a published version, resolved and recorded rather
			// than tracked. `dev agent update claude` moves it and says
			// what moved; until then two builds produce the same agent,
			// which is what `dev pin` asks of every other image.
			Version: "2.1.227",
			Binary:  "claude",
			// The sandbox is the boundary, which is what makes skipping
			// the per-action prompts reasonable here and nowhere else.
			Args:         []string{"--dangerously-skip-permissions"},
			ConfigDir:    "/home/dev/.claude",
			ConfigEnv:    "CLAUDE_CONFIG_DIR",
			AuthEnv:      []string{"ANTHROPIC_API_KEY"},
			Base:         "debian:bookworm-slim",
			Runtime:      "node",
			RuntimeImage: "node:22-bookworm-slim",
			Install:      "npm install -g @anthropic-ai/claude-code@{{VERSION}}",
			// Rather than allowlist the telemetry endpoints, turn the
			// traffic off at the source: nothing to block, nothing to
			// report, no noise in the notices.
			Env: []string{
				"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
				// An agent that self-updates inside the sandbox changes
				// what runs between two invocations, which defeats the
				// point of pinning the image.
				"DISABLE_AUTOUPDATER=1",
			},
			// Hosts per Claude Code's documented network requirements.
			AllowHosts: []string{
				"api.anthropic.com",
				// Sign-in: claude.com opens the page, platform.claude.com
				// does the OAuth token exchange for BOTH account types,
				// claude.ai authenticates. Missing any of these fails
				// first-run login, which reads as a broken tool.
				"claude.com",
				"claude.ai",
				"platform.claude.com",
				"console.anthropic.com",
				// Documentation lookups by the built-in guide agent.
				"code.claude.com",
				"statsig.anthropic.com",
				"registry.npmjs.org",
				"github.com",
				"api.github.com",
				"codeload.github.com",
				"*.githubusercontent.com",
			},
			// The connector proxy, off the allowlist by default. A claude.ai
			// account's connectors — Gmail, Linear, Notion — route through
			// here, so without --allow-mcp an agent cannot reach them and
			// cannot use the tokens in its config volume to read the user's
			// accounts.
			MCPHosts:   []string{"mcp-proxy.anthropic.com"},
			MCPOffArgs: []string{"--strict-mcp-config"},
		},
		{
			Name:         "codex",
			Description:  "OpenAI Codex CLI",
			Version:      "0.147.0",
			Binary:       "codex",
			Args:         []string{"--dangerously-bypass-approvals-and-sandbox"},
			ConfigDir:    "/home/dev/.codex",
			ConfigEnv:    "CODEX_HOME",
			AuthEnv:      []string{"OPENAI_API_KEY"},
			Base:         "debian:bookworm-slim",
			Runtime:      "node",
			RuntimeImage: "node:22-bookworm-slim",
			Install:      "npm install -g @openai/codex@{{VERSION}}",
			AllowHosts: []string{
				"api.openai.com",
				"auth.openai.com",
				"chatgpt.com",
				"registry.npmjs.org",
				"github.com",
				"api.github.com",
				"codeload.github.com",
				"*.githubusercontent.com",
			},
		},
	}
}
