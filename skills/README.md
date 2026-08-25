# Skills

Instructions that teach an agent how to use this tool, packaged for
Claude Code.

A skill is advisory. It makes the safer path the one an agent reaches for;
it does not make the unsafe path unavailable, and an agent that ignores it
gets no protection while the user sees nothing different from the outside.
Where a guarantee is wanted rather than a habit, the answer is the other
direction — `dev agent run`, which puts the agent inside the sandbox and
makes forgetting impossible.

| skill | what it does |
|---|---|
| [sandboxed-execution](sandboxed-execution/SKILL.md) | run untrusted code through `dev` instead of on the host |

Install one by copying its directory into `~/.claude/skills/`, or into a
project's `.claude/skills/`.
