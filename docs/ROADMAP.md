# Roadmap: isolated-dev v2 (Go rewrite)

Status: proposal
Owner: @mwing

## 1. Vision

The original idea of this project, restated as the product definition for v2:

> Frictionless containerized development without needing to understand Docker
> intricacies, with sane secure defaults, and an easy, explicit way to grant
> more access based on trust.

Three principles follow from that:

1. **Zero knowledge required.** `dev` in a project directory does the right
   thing: detects the language, builds an image, mounts the code, forwards the
   right ports. Docker remains an implementation detail the user never has to
   see.
2. **Deny by default, easy to allow.** Nothing sensitive (credentials, SSH
   keys, sockets, host folders) enters a container unless the user opted in.
   Opting in must be one obvious step, and the tool should *suggest* the
   common grants at the moment they are needed ("git push failed: enable
   ssh agent forwarding for this project? dev trust ssh"), never silently
   apply them.
3. **Trust is per project and revocable.** Access grants attach to a project
   (identified by path + config hash), are stored outside the project tree,
   are visible (`dev trust list`), and are revoked in one command.

v2 is a rewrite in Go. The bash v1 remains functional during the transition;
v1 gets bug fixes only, no new features. The CLI contract may change freely
in v2 (breaking changes are acceptable and expected).

## 2. Why Go

- Single static binary: fast startup (single-digit ms), trivial install
  (brew tap, curl), signable and checksummable, no interpreter surface.
- Excellent standard library and ecosystem fit for this domain: os/exec,
  net/http, encoding/json, x/crypto; cobra for the CLI, gopkg.in/yaml.v3 for
  real YAML parsing (replaces three ad hoc bash parsers).
- Correct argument arrays by construction; the whole class of quoting and
  word-splitting bugs in v1 cannot exist.
- Testable: unit tests with a fake runner interface instead of 1400 lines of
  output-grepping bash.
- goroutines make the agent-mode orchestration (proxy sidecar + container +
  signal handling + log streaming) straightforward.

Non-goals of the rewrite: no daemon, no state database beyond flat files in
`~/.dev-envs`, no Kubernetes, no reimplementation of docker compose.

## 3. Architecture

```
cmd/dev/                 main, cobra command tree
internal/config/         schema, defaults, layering (flags > env > project > global), validation
internal/trust/          per-project trust store, TOFU hashing, grant/revoke
internal/backend/        Backend interface + drivers
    orbstack/            v1 behavior: orb -m <vm> docker ...
    docker/              plain docker CLI/socket (Linux, Docker Desktop, colima)
internal/runner/         exec wrapper; every external command goes through here (mockable, logged with --verbose)
internal/container/      run/build/clean/shell; assembles docker args from a RunSpec struct
internal/netpolicy/      egress proxy sidecar lifecycle, allowlists, internal networks
internal/agent/          agent registry, agent volumes, agent run orchestration
internal/langs/          language plugin loading (same on-disk format as v1: languages/<lang>/)
internal/detect/         project type, version, port detection
internal/scaffold/       dev new, scaffolding, devcontainer.json generation
internal/scan/           trivy/grype integration with real exit codes
internal/ui/             prompts, confirmations, --yes handling, output formatting
```

Key design decisions:

- **RunSpec struct, not string concatenation.** Everything that becomes a
  `docker run` invocation is a typed struct (mounts, env, ports, caps,
  network, limits). One function renders it to argv. Unit tests assert on the
  struct, golden tests on the argv.
- **Backend interface** (`Build`, `Run`, `Exec`, `ImageExists`, `Save`,
  `NetworkCreate`, `VolumeCreate`, ...). OrbStack driver ships first and
  reproduces v1 behavior; the plain-docker driver makes the tool usable on
  Linux and non-OrbStack Macs. Apple's native container tooling can be a
  third driver later without touching command code.
- **Language plugins stay data.** The `languages/<lang>/language.yaml` +
  `Dockerfile.template` + scaffolding format is preserved so existing plugins
  work unchanged. Agents use a parallel `agents/<name>/agent.yaml` format.
- **Every external command visible.** `--verbose` prints the exact argv for
  every process the tool spawns. A security tool should never make the user
  guess what it ran.

## 4. Security model (v2)

### 4.1 Trust levels

Every project runs at one of three levels. The level is chosen automatically
(untrusted by default) and raised only by explicit user action.

| | untrusted (default) | trusted | privileged |
|---|---|---|---|
| workspace mount | rw | rw | rw |
| egress | allowlist via proxy (registries for the detected language) | open | open |
| pass_env_vars | ignored | honored | honored |
| ssh | none | agent forwarding (socket only, never key files) | agent forwarding |
| gitconfig | identity-only generated file | filtered host copy | filtered host copy |
| docker socket | never | never | opt-in per run |
| caps | drop ALL + minimal adds, no-new-privileges, pids/mem limits | same | same, socket exception |

### 4.2 Trust-on-first-use

`dev` hashes the project's `.devenv.yaml` and Dockerfile on every run. First
run in a project, or any time the hash changes, an interactive confirmation
shows what the project config *asks for* (mounts, env patterns, ports) before
honoring any of it. Decisions are recorded in
`~/.dev-envs/trust.yaml` keyed by project path. `dev trust`, `dev trust list`,
`dev trust revoke [path]` manage the store. `--yes` never auto-grants trust
elevation; CI uses explicit flags instead.

This closes the v1 hole where cloning a malicious repo and typing `dev` would
honor the repo's own config (env passthrough, mounts) against the repo's own
Dockerfile.

### 4.3 Suggested grants, not silent grants

When an operation fails in a way the tool recognizes (git push permission
denied, AWS SDK credential error), it prints the one-line grant command that
would fix it and what that grant exposes. Friction lives at the moment of
need, with an informed decision, instead of being removed in advance.

## 5. Milestone plan

Agent mode ships first. It forces the new foundations (egress proxy, named
volumes, trust store, backend abstraction) to exist, and those primitives are
exactly what the rest of the tool then reuses. Porting the crud commands
first would delay the only genuinely new capability.

### M0: Skeleton (small)

- Repo layout under `go/` (or a `v2` branch), cobra CLI, goreleaser config,
  CI matrix (macOS + Linux, go test, golangci-lint, govulncheck).
- `internal/runner` with fake for tests; `internal/config` reading the v1
  YAML files (schema-compatible where keys survive).
- `dev2 version`, `dev2 doctor` (checks orb/docker presence, VM state; ports
  the v1 `debug` checks).

Exit criteria: binary builds on both platforms in CI, doctor works against a
real OrbStack.

### M1: Agent mode (the flagship)

- `dev2 agent claude`, `dev2 agent codex`, `dev2 agent list`.
- Agent plugin format: `agents/<name>/agent.yaml` (install steps, binary,
  default allowlist hosts, config dir path inside home).
- Image overlay: project image + agent layer built on demand (project
  Dockerfile untouched).
- **Egress proxy sidecar** (`internal/netpolicy`): per-project internal
  network, allowlisting CONNECT proxy (LLM API + language registries +
  git hosts), HTTP(S)_PROXY/NO_PROXY injection, denied-connection log
  surfaced at exit ("agent tried to reach X, blocked").
- **Agent home volumes**: named volume per agent (configurable per-project),
  OAuth login persists across runs; `dev2 agent logout <name>` removes it.
- Auth modes: `volume` (default) and `env` (API key by name, for CI).
- Agent runs are always `untrusted` level + allowlist regardless of project
  trust; `--allow-host` adds destinations per run.
- Safe YOLO: agents launched with their auto-approve flags inside the sandbox.

Exit criteria: Claude Code and Codex both complete a real task in a sample
repo with egress logging showing only allowlisted hosts; credentials
demonstrably absent from the container when not granted.

### M2: Core loop parity

- `run`, `shell`, `build`, `clean`, `new`, `list`, `config`, `devcontainer`
  ported onto RunSpec + Backend. `-c` command pass-through. Port/language
  detection ported from v1 semantics.
- Trust store + TOFU (section 4.2) wired into `run`/`shell`.
- ssh-agent forwarding replaces key-file mounts.
- The egress proxy from M1 becomes available to normal runs:
  `network: allowlist|open|none` per project, `--offline` flag.
- v1 config migration: `dev2 migrate` reads `~/.dev-envs/config.yaml`,
  writes v2 config, reports dropped/renamed keys (network_mode and the other
  v1 no-op keys are dropped loudly).

Exit criteria: daily-drivable replacement for v1 on OrbStack; v1 marked
maintenance-only in README.

### M3: Multi-backend + supply chain

- Plain docker backend (Linux, colima, Docker Desktop); backend auto-detect
  with config override.
- Digest pinning: `dev2 new` resolves the base tag to a sha256 digest and
  records it in the Dockerfile; `dev2 update` re-resolves deliberately.
- `dev2 scan` with real exit codes as the CI gate; SBOM emission (syft or
  `docker sbom`) as an option.
- Signed releases: goreleaser + cosign, checksums in release notes,
  brew tap.

### M4: Team features

- Policy file (`~/.dev-envs/policy.yaml` or org-distributed): forbidden
  mounts, mandatory limits, allowed registries, minimum scan severity;
  `dev2` refuses configs that violate it. Aimed at the infosec-owner use
  case: hand teammates a tool where the unsafe paths are closed org-wide.
- devcontainer.json interop: read (not just write) the essentials, so
  projects standardized on devcontainers work with `dev2` unmodified.
- `dev2 status` / `dev2 ps` across projects.

### Deliberately out of scope

Compose-style multi-service orchestration, Kubernetes, Windows, remote
(cloud) backends, GUI. Revisit only after M4 is real.

## 6. Compatibility and migration

- On-disk language plugin format: unchanged (v2 reads the same
  `~/.dev-envs/languages`).
- Config: keys that describe real behavior carry over; v1's never-implemented
  keys (network_mode, auto_host_networking, port_range, port health checks)
  are dropped with a loud migration note. `pass_env_vars` carries over but is
  gated behind trust level.
- CLI: no compatibility promise. The binary ships as `dev2` during M1-M2 and
  takes over the `dev` name when v1 is retired (installer keeps `dev` as an
  alias to `dev2` from M2 on).
- v1 lifecycle: bug fixes only from M0; removed from the installer at M3;
  directory kept in-tree as `legacy/` until M4.

## 7. Testing strategy

- Unit: config layering, trust hashing, allowlist matching, RunSpec
  rendering (golden argv files).
- Integration: against a real docker daemon in Linux CI (no OrbStack needed
  thanks to the backend interface); OrbStack path exercised by a macOS CI
  runner where available, otherwise a documented manual checklist per
  release.
- Security regression tests as first-class: "untrusted project cannot read
  host env var", "agent cannot reach non-allowlisted host", "docker socket
  absent unless privileged + opt-in". These are the tests that define the
  product; they gate every release.

## 8. Sequencing summary

```
M0 skeleton -> M1 agent mode -> M2 core parity + trust -> M3 backends/supply chain -> M4 team/policy
```

M1 before M2 is intentional: agent mode is the new value and it builds the
primitives (proxy, volumes, trust) that M2 then reuses. If effort must be
cut, M4 drops first, then M3's extra backends; the trust model and egress
control are not cuttable, they are the point of the tool.
