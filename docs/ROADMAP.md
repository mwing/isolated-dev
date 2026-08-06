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
  net/http, encoding/json, x/crypto; cobra for the CLI, goccy/go-yaml (or sigs.k8s.io/yaml; gopkg.in/yaml.v3
  is effectively unmaintained) for real YAML parsing (replaces three ad hoc
  bash parsers).
- Correct argument arrays by construction; the whole class of quoting and
  word-splitting bugs in v1 cannot exist.
- Testable: unit tests with a fake runner interface instead of 1400 lines of
  output-grepping bash.
- goroutines make the agent-mode orchestration (proxy sidecar + container +
  signal handling + log streaming) straightforward.

Non-goals of the rewrite: no daemon, no state database beyond flat files in
`~/.dev-envs`, no Kubernetes, no reimplementation of docker compose.

### 2.1 The alternative we are rejecting

Harden v1 in place. This deserves stating because it is genuinely viable:
the pre-rewrite fix PR left v1 at 158 passing tests and shellcheck-clean at
error level, and neither headline feature is language-bound — the egress
sidecar is mostly `docker network create --internal` plus a proxy image, and
the trust store is a YAML file and a prompt. Both could ship in bash within
weeks, against a tool users already have installed.

It is rejected for one reason, and it is not aesthetics: v1's defect class
is argument handling. The fix PR was almost entirely quoting and
word-splitting repairs — env arrays, `-c` pass-through, project-name
sanitization, three separate ad hoc YAML parsers — and each was found by a
user or a failing test rather than by construction. A security tool whose
central operation is assembling an argv cannot keep relitigating whether a
value with a space survives. `RunSpec` plus typed argv makes those bugs
unrepresentable; that is worth a rewrite in a way that "Go is nicer" is not.

The honest cost of choosing this path is that the egress proxy and trust
store, which users want now, arrive after M0 and M1 instead of next month.
If that trade stops looking correct — if M1 slips badly — backporting the
sidecar to v1 is the fallback, and it is a real one.

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
| runtime identity | non-root, fixed UID (1000) mapped to the invoking user, enforced by the tool via `--user` | same | same |
| host filesystem | workspace is the only writable host path | workspace + explicitly granted mounts | same, plus the docker socket if opted in |
| workspace mount | rw | rw | rw |
| egress | enforced allowlist (see 4.3) | open | open |
| pass_env_vars | ignored | honored | honored |
| ssh | none | agent forwarding (socket only, never key files) | agent forwarding |
| gitconfig | identity-only generated file | filtered host copy | filtered host copy |
| docker socket | never | never | opt-in per run |
| caps | drop ALL + minimal adds, no-new-privileges, pids/mem limits | same | same, socket exception |

Two v1 behaviors this table deliberately changes:

- **Identity is set by the tool, not by the image.** v1 skips `--user` and
  relies on the `USER` directive in the language template
  (`scripts/lib/security.sh:52`). Since the project supplies its own
  Dockerfile, an untrusted repo can simply declare `USER root` and get root,
  at which point the retained `DAC_OVERRIDE`/`SETUID`/`SETGID` capabilities
  become meaningful again. v2 passes `--user` explicitly on every run; the
  image's `USER` is a default, never the control.
- **No blanket `apparmor:unconfined`.** v1 sets it for all runs
  (`security.sh:57`). v2 keeps the default profile and drops the confinement
  only where a backend demonstrably requires it, per backend driver.

### 4.2 Trust-on-first-use

`dev` extracts the project config's *security-relevant asks* (mounts, env
patterns and explicit names, docker socket, network mode, published ports),
normalizes them, and hashes that grant set, not the raw files. First run in a
project, or any time the grant set changes, an interactive confirmation shows
exactly what is being requested before any of it is honored. Routine edits to
the Dockerfile or to non-security config (container prefix, template choice)
never re-prompt; prompting only on real surface changes is what keeps users
reading the prompt instead of reflexively confirming it. Decisions are
recorded in `~/.dev-envs/trust.yaml` keyed by project path. `dev trust`,
`dev trust list`, `dev trust revoke [path]` manage the store. `--yes` never
auto-grants trust elevation; CI uses explicit flags instead.

This closes the v1 hole where cloning a malicious repo and typing `dev` would
honor the repo's own config (env passthrough, mounts) against the repo's own
Dockerfile.

Scope honesty: TOFU covers what the project is *granted*, not what its code
does. A malicious postinstall script runs regardless of any prompt; the
sandbox (non-root, capability drop, egress control, no host paths beyond the
workspace) is what contains it. TOFU must never be presented as protection
against the code itself.

### 4.3 Egress enforcement

Egress control is enforced by network topology, not by environment variables:

- The workload container attaches ONLY to a per-project internal Docker
  network (`--internal`: no gateway, no default route out).
- The proxy sidecar is dual-homed (internal network + egress) and is the
  only path to the outside. A process that ignores proxy settings does not
  fall through to the open internet; it has no route at all.
- DNS resolves only through the proxy's resolver on the internal network,
  so name resolution cannot be used as a side channel to bypass hostname
  checks, and raw-IP connections fail for lack of a route. The resolver
  *allowlists*, it does not merely forward: non-allowlisted names get
  NXDOMAIN and are logged alongside denied connections. Forwarding without
  filtering would leave DNS tunnelling wide open. (With CONNECT proxying the
  client does not strictly need working DNS at all — the proxy resolves — so
  a filtering resolver is a compatibility affordance, not a requirement.)
- The proxy has no runtime control plane reachable from the internal
  network. Policy is fixed at sidecar start from the resolved trust state;
  there is no admin socket, config endpoint, or reload API bound to the
  interface the workload can see. Otherwise the workload rewrites its own
  allowlist.
- The proxy allowlists by CONNECT target / SNI hostname only. It does NOT
  terminate TLS: no injected CA, no MITM, certificate pinning keeps working,
  and the proxy never sees LLM traffic plaintext. The cost is that filtering
  is per-host, not per-path; that trade is accepted deliberately.
- `HTTP(S)_PROXY`/`NO_PROXY` are injected as a convenience for well-behaved
  clients (npm, pip, git, curl, the agent CLIs); they are not the security
  boundary.
- Denied connection attempts are logged and summarized at exit ("blocked:
  evil.example.com x3").

### 4.4 Threat model, stated honestly

**What is being promised.** The product guarantee is about defaults and
visibility, not about a ceiling on what a container may hold. A user can
expose anything they need — credentials, sockets, host paths, open egress —
and complex setups will legitimately need most of it to run at all. Full
access is a supported end state, not a failure mode. What v2 guarantees is
that nothing sensitive gets there *by accident*: every exposure is the
result of an explicit grant, the grant is visible (`dev trust list`), scoped
to a project, and revocable in one command. The tool's job is to make the
trusted configuration the deliberate one, not to keep the user poor.

This is why "there is nothing worth stealing in the container" is a
statement about the untrusted default, not an invariant of the tool. It is
what M1's exit criteria test because agent runs are pinned to that level; at
`trusted` and `privileged` the user has decided otherwise, and that decision
is the feature.

**What the allowlist contains:** accidental and undirected exfiltration —
telemetry, typosquatted packages phoning home, an agent following a prompt
injection to an arbitrary URL.

**What it does not contain:** a determined adversary exfiltrating THROUGH an
allowlisted host. If github.com and the npm registry are reachable, they are
usable as data channels (a push to an attacker repo, a package publish).

**Structural exception — the agent's own API channel.** Agent mode requires
the LLM endpoint to be allowlisted or the agent cannot function. That
endpoint is bidirectional by nature: an agent acting on injected
instructions can encode repository contents into its own completions
traffic. No hostname allowlist can close this, and no version of this design
will. It is a property of running an agent on data at all, and it belongs in
the docs rather than in a footnote.

Documentation and marketing copy must preserve these distinctions. v1
overclaimed; v2 should be precise about which promise is being made at which
trust level.

### 4.5 Suggested grants, not silent grants

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
- **Egress proxy sidecar** (`internal/netpolicy`), implementing section 4.3
  exactly: internal network with no route out, dual-homed SNI-allowlisting
  proxy (LLM API + language registries + git hosts), proxy-only DNS, no TLS
  interception, denied-connection log surfaced at exit.
- **Agent home volumes**: named volume per agent (configurable per-project),
  OAuth login persists across runs; `dev2 agent logout <name>` removes it.
- Auth modes: `volume` (default) and `env` (API key by name, for CI).
- Agent runs are always `untrusted` level + allowlist regardless of project
  trust; `--allow-host` adds destinations per run.
- Safe YOLO: agents launched with their auto-approve flags inside the sandbox.
- **Git-write story (explicit default)**: agents can commit inside the
  container (identity-only gitconfig) but cannot push; the human reviews the
  diff and pushes from the host. This is a feature, not a gap: it is the
  review boundary. An optional `dev agent --allow-push` grant exists for
  workflows that want it — it adds the git host to the allowlist and
  forwards the ssh agent socket (preferred: the key itself never enters the
  container and the grant dies with the agent), falling back to a
  repo-scoped token by name where ssh is not an option. Never on by default,
  and the confirmation states plainly that the run no longer satisfies
  section 4.4's untrusted-default posture.
- **Agent versioning**: agent CLIs ship on their own weekly-ish cadence, so
  `agents/<name>/agent.yaml` pins a version and the overlay layer is keyed
  by it; `dev2 agent update <name>` re-resolves deliberately. Unpinned
  "latest" would silently change what runs inside the sandbox between runs.

Exit criteria: Claude Code and Codex both complete a real task in a sample
repo with egress logging showing only allowlisted hosts; credentials
demonstrably absent from the container when not granted.

### M2: Core loop parity

Scope honesty up front: this is the largest milestone, bigger than M0+M1
combined. v1 is ~4k lines of bash plus 8 language plugins, interactive mode,
scaffolding, and devcontainer generation. Two rules keep it from stranding
the project in a permanent dev/dev2 split:

1. The exit criterion is an explicit command-by-command parity checklist
   against v1 (every command and flag in v1's usage output, each marked
   ported / delegated / dropped-with-reason). No vague "core loop works".
2. dev2 may DELEGATE long-tail commands to a vendored copy of the v1 scripts
   during the transition, so cutover is gated on the security-relevant path
   (run, shell, build, trust, egress), not on the least interesting code.
   The vendored scripts ship inside the release artifact with checksums;
   they are an implementation detail, not a parallel install.

   **Bright line: a delegated command may never start a container.**
   Delegation is permitted for commands that only read state or write files
   — `templates update/stats/prune`, scaffolding, completions. It is
   forbidden for anything that runs a workload, because a delegated workload
   would run under v1 semantics (no trust store, no egress policy, image's
   `USER` instead of `--user`) while the docs describe the v2 model. Note
   that this puts `interactive` on the port-or-drop side of the line: it
   launches containers.

Work items:

- `run`, `shell`, `build`, `clean`, `new`, `list`, `config`, `devcontainer`
  ported onto RunSpec + Backend. `-c` command pass-through. Port/language
  detection ported from v1 semantics.
- Trust store + TOFU (section 4.2) wired into `run`/`shell`.
- ssh-agent forwarding replaces key-file mounts.
- The egress proxy from M1 becomes available to normal runs:
  `network: allowlist|open|none` per project, `--offline` flag.
- v1 config migration: `dev2 migrate` reads `~/.dev-envs/config.yaml`,
  writes v2 config, reports dropped/renamed keys (v1 stopped emitting the
  never-implemented keys in the pre-rewrite fix PR, so the stray-key
  population is frozen at whatever users already have).

Exit criteria: parity checklist complete; daily-drivable replacement for v1
on OrbStack; v1 marked maintenance-only in README.

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

Relative sizes, so the cut order below is actionable rather than a
sentiment. These are ratios between milestones, not calendar estimates;
whoever picks this up should replace them with real ones against their own
capacity before committing to a date:

| | size | notes |
|---|---|---|
| M0 | S | scaffolding, no product surface |
| M1 | L | the proxy sidecar is the hard part and it is novel work |
| M2 | XL | larger than M0+M1 combined; ~4k lines of bash, 8 language plugins |
| M3 | M | mostly integration; the docker backend is well-understood |
| M4 | M | policy file is small, devcontainer read-side is not |

M1 before M2 is intentional: agent mode is the new value and it builds the
primitives (proxy, volumes, trust) that M2 then reuses. If effort must be
cut, M4 drops first, then M3's extra backends; the trust model and egress
control are not cuttable, they are the point of the tool.
