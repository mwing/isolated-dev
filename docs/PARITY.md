# v1 → v2 parity checklist

M2's exit criterion (ROADMAP §5). Every v1 command is listed with a state:

- **ported** — reimplemented on RunSpec + Backend, v1 no longer needed
- **delegated** — dev2 shells out to a vendored v1 script. Permitted only
  for commands that never start a container (ROADMAP M2 bright line): a
  delegated workload would run under v1 semantics — no trust store, no
  egress policy, image `USER` instead of `--user` — while the docs describe
  the v2 model
- **dropped** — with a reason
- **todo** — not started

Status is updated as work lands, not in advance.

| v1 command | state | notes |
|---|---|---|
| `dev` (bare run) | todo | the core loop |
| `dev shell` | todo | |
| `dev build` | todo | |
| `dev clean` | todo | |
| `dev run -c '<cmd>'` | todo | command pass-through |
| `dev new` | todo | scaffolding; delegation candidate |
| `dev list` | todo | |
| `dev config` | partial | v2 reads the same files; `dev2 agent config` covers the agent half |
| `dev config validate` | todo | |
| `dev config --init` | todo | |
| `dev devcontainer` | todo | write side; read side is M4 |
| `dev env` | todo | VM lifecycle |
| `dev debug` | ported | `dev2 doctor` |
| `dev troubleshoot` | todo | |
| `dev disk` | todo | |
| `dev arch` | ported | `dev2 version` reports platform |
| `dev security scan` | todo | M3, with real exit codes |
| `dev security check` | todo | Dockerfile linting |
| `dev templates *` | todo | delegation candidate (no containers) |
| `dev interactive` | todo | **port or drop** — it starts containers, so it may not be delegated |
| `dev help` | ported | cobra |
| — | — | |
| language detection | ported | now reads `language.yaml`; see below |
| language plugins | ported | all 8 shipped plugins load unchanged |
| trust store | ported | per-project grants, request/accept flow |
| egress control | ported | agents only so far; normal runs are M2 |
| agent mode | ported | M1, new in v2 |

## Behavior changes worth noting

These are places v2 deliberately does not reproduce v1.

**Language detection reads the plugin data.** v1 declared
`detection.files` in every `language.yaml` and ignored it: the markers
lived in a hardcoded bash case statement, as did version extraction and
ports. Dropping in a new plugin directory did nothing until someone edited
the script. v2 reads the YAML, so a plugin is a plugin. The shipped
plugins are unchanged and load as they are.

**Ambiguous projects resolve deterministically.** v1 took whichever
language its directory listing reached first, so a repo with both `go.mod`
and `requirements.txt` could detect differently on two machines. v2 picks
the language with the most matching markers, ties broken by name.

**A broken plugin is reported.** v1 skipped anything it could not read.
v2 records a note so a malformed `language.yaml` is visible rather than
silently absent.
