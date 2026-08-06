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
			Version:     "latest",
			Binary:      "claude",
			// The sandbox is the boundary, which is what makes skipping
			// the per-action prompts reasonable here and nowhere else.
			Args:      []string{"--dangerously-skip-permissions"},
			ConfigDir: "/home/dev/.claude",
			AuthEnv:   []string{"ANTHROPIC_API_KEY"},
			Base:      "node:22-bookworm-slim",
			Install:   "npm install -g @anthropic-ai/claude-code",
			AllowHosts: []string{
				"api.anthropic.com",
				// Login. Without these the OAuth flow fails on first run,
				// which reads as a broken tool rather than a policy.
				"platform.claude.com",
				"console.anthropic.com",
				"claude.ai",
				"statsig.anthropic.com",
				"registry.npmjs.org",
				"github.com",
				"api.github.com",
				"codeload.github.com",
				"*.githubusercontent.com",
			},
		},
		{
			Name:        "codex",
			Description: "OpenAI Codex CLI",
			Version:     "latest",
			Binary:      "codex",
			Args:        []string{"--dangerously-bypass-approvals-and-sandbox"},
			ConfigDir:   "/home/dev/.codex",
			AuthEnv:     []string{"OPENAI_API_KEY"},
			Base:        "node:22-bookworm-slim",
			Install:     "npm install -g @openai/codex",
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
