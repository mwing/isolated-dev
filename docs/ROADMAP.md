# Roadmap: isolated-dev

Status: shipped. The Go tool is `dev`; the bash tool it replaced is `dev1`,
kept in [v1/](../v1/).
Owner: @mwing

This began as a proposal and is now partly a record. Sections 1–4 are the
design and still describe the tool as built; section 5 is the milestone
plan with what actually happened; section 9 is what is left.

**If you are here to use the tool rather than to understand its design,
start with [../README.md](../README.md).**

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

**What the binary actually does:**
there are no three named levels in the code, and there is no `dev trust`
command. What exists is the untrusted column plus a per-setting acceptance:
`mount_git_config`, `mount_docker_socket` and `pass_env_vars` are honored
once this user has accepted them for this project (`dev accept`), or
immediately when they come from the user's own global config rather than
from a project file. Read the table below as the design's vocabulary and the
following paragraph as the behavior:

- **untrusted** is every run that has accepted nothing. It is the default and
  the common case.
- **trusted** and **privileged** are not states a project is in; they are
  what accepting a particular setting buys, one setting at a time. Accepting
  `mount_docker_socket` does not also open egress.
- **ssh keys are never mounted, at any level.** `mount_ssh_keys` is retired
  with a note pointing at ssh-agent forwarding (`--allow-push`), which is
  what the "socket only, never key files" row below means.
- **gitconfig** is a filtered host copy when granted, mounted read-only at
  `/etc/gitconfig`. It is not generated per-level; an ungranted run gets no
  git configuration at all, and `dev agent run` sets only user.name and
  user.email as environment variables.

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

### 4.2.1 Two files, two jobs: requests and acceptances

`.devenv.yaml` lives in the repository and is shared by the team.
`~/.dev-envs/projects/<slug>-<hash>.yaml` lives on one machine and belongs
to one user. Both must work, and the split between them is not an
accident of history — it is the trust model expressed as two files:

- **The project file states what the project needs.** "This service talks
  to `internal.example.com` and our package mirror." It is committed,
  reviewed like code, and shared, so a new teammate does not rediscover
  the allowlist by trial and error.
- **The user file records what this user accepted.** It is never
  committed and never written by anything but an explicit user action.

A project file is therefore a *request*, never a grant. On each run the
tool diffs what the project asks for against what the user has accepted,
and anything new stops the run with a confirmation showing exactly what is
being added. `dev agent accept` records the decision; from then on the
project's config applies silently until it changes again. This is the
trust-on-first-use flow of 4.2 with the request written down instead of
inferred, which makes it *better* than a purely local file: the team can
propose a policy, and each user still consents to it.

Non-security preferences in the project file — base image, memory, cpus —
apply directly without a prompt. They change how the sandbox is built, not
what it may reach or read.

The failure this design exists to prevent: honoring `allow_hosts` from a
cloned repository, which would let a hostile project widen its own egress
before anyone read a line of it.

**What a grant is attached to, stated plainly: the path.** Not the code,
and not the repository. A decision recorded for `~/src/foo` applies to
whatever is in `~/src/foo` afterwards — a `git pull` that brings new code,
or a different project cloned into the same directory.

This is a real limit and it is not fixable by fingerprinting the
repository. Binding trust to the git origin would mean reading an identity
out of the repository whose identity is in question, and `git remote
set-url` defeats it in one command — the same self-asserted-configuration
problem this whole section exists to avoid. The only unforgeable signal is
the content itself, which changes on every commit and would ask constantly,
which is how people are taught to stop reading prompts.

So the mitigations are elsewhere, and are the reason these exist: grants
are per-destination rather than blanket, `dev grants` reports which ones
anything has actually used, pruning removes the rest, and the settings with
consequences severe enough that inheritance would matter — the docker
socket above all — should be per-run rather than persistent.

Status: implemented. `.devenv.yaml` may carry an `agents:` section; a run
stops on anything requested but not accepted, and `dev agent accept`
records the decision. Acceptance is an intersection, not a union: a host the
project stops requesting stops applying, and a host added later is pending
again, so consent is never a blank cheque for future edits.

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
  *allowlists*, it does not merely forward: non-allowlisted names are
  REFUSED — not NXDOMAIN, which asserts a name does not exist — and are
  logged alongside denied connections. Forwarding without filtering would
  leave DNS tunnelling wide open.

  **A wildcard grant reopens part of it, and this is not fully closed.**
  With `*.example.com` allowed, `<payload>.example.com` is a permitted
  name whose *query* carries data out; no answer is needed. Both the
  resolver and the CONNECT path refuse names whose shape suggests
  encoding — over-long, deeply nested, or long-labelled — which raises the
  cost and lowers the bandwidth. It does not close the channel: chunked,
  low-volume encoding inside ordinary-looking names still passes, by
  design, because the alternative refuses names real services use. Treat
  DNS exfiltration under a wildcard grant as slowed, not prevented. (With CONNECT proxying the
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

  Both halves of that sentence are enforced, which they were not for a long
  time. The CONNECT authority decides which address is dialled; the SNI
  decides which site answers on a shared front, so checking only the first
  left an allowed CDN name as a way to reach a denied one. The opening TLS
  record is now read as it streams past — not held, not decrypted, not
  rewritten — and a name that does not match the CONNECT target is refused
  with a fatal `access_denied` alert. Two cases pass deliberately: a
  connection that is not TLS at all (ssh on port 22 is a supported one) has
  no name to check, and a ClientHello carrying no SNI reaches whatever the
  dialled host serves by default, which is the host already approved. A
  handshake record that cannot be read is refused rather than passed,
  because fragmenting the ClientHello is how a check like this is evaded —
  including across two TLS records, which is legal, reassembles perfectly
  at the far end, and is chosen by the client.

  **Known limitation: Encrypted Client Hello.** Where a client uses ECH,
  the visible SNI is the ECH public name rather than the destination, so it
  will not match the CONNECT target and the connection is refused. That is
  the safe direction — a name this proxy cannot read is not one it can
  vouch for — but it means ECH clients cannot use a filtered run until
  this is handled deliberately.
- `HTTP(S)_PROXY`/`NO_PROXY` are injected as a convenience for well-behaved
  clients (npm, pip, git, curl, the agent CLIs); they are not the security
  boundary.
- Denied connection attempts are logged and summarized at exit ("blocked:
  evil.example.com x3").

### 4.3.1 What egress control does not cover: image builds

The proxy governs what a *running* container reaches. It does not govern
image builds: `docker build` runs on the daemon, outside the internal
network, with whatever access the daemon has. So a Dockerfile, a language
template, or a `dev add` install step downloads over an unfiltered path.

This is worth stating rather than leaving to be discovered. It is also the
right trade for now — a build that cannot reach a package index is not a
build — but it means the allowlist is a runtime control, not a supply
chain one. Digest pinning (M3) is the lever that addresses builds, and it
addresses a different risk: what you get, rather than where you went.

### 4.3.2 Credentials, and why the proxy does not inject them

Docker Sandboxes keeps agent credentials on the host and has its proxy
inject auth headers into outbound requests, so the key never enters the
sandbox. It is the strongest idea in that design, and it is rejected here
for a reason worth recording rather than rediscovering.

Injection requires reading and rewriting the request, which requires
terminating TLS for that host. This tool deliberately does not (4.3): the
proxy checks the CONNECT target and relays bytes untouched, so certificate
pinning keeps working and the proxy never holds anyone's plaintext.

Scoping the injection is the easy half. A credential can be bound to one
host, one path prefix, one method and one named header, and refused
everywhere else — that part is ordinary matching, and it would stop a
credential leaking to every destination on the allowlist.

What scoping does not fix is the confused deputy. Any process in the
sandbox can then issue requests to that host and have the credential
attached on its way out. The key is protected; the *authority* is not.
That is still a real improvement — a stolen key outlives the sandbox and
borrowed authority does not — but it is a smaller improvement than it
looks, bought with TLS interception, a CA certificate installed in the
container's trust store, and per-host configuration that users have to
maintain and get right.

The better fit for this model is to broker the operation rather than the
credential, which is already what `--allow-push` does: the ssh-agent
socket is forwarded, so the container can ask for a signature and can
never read the key. Generalizing that — a narrow host-side broker for the
specific privileged actions a workload needs — gets the same property
without touching TLS. Prefer it where a future case demands one.

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

### M0: Skeleton — done

- Repo layout under `go/` (or a `v2` branch), cobra CLI, goreleaser config,
  CI matrix (macOS + Linux, go test, golangci-lint, govulncheck).
- `internal/runner` with fake for tests; `internal/config` reading the v1
  YAML files (schema-compatible where keys survive).
- `dev version`, `dev doctor` (checks orb/docker presence, VM state; ports
  the v1 `debug` checks).

Exit criteria: binary builds on both platforms in CI, doctor works against a
real OrbStack.

Status: implemented on the `v2-go-rewrite` branch under `go/`. `runner`
(with fake), `config` (layering, provenance, grant extraction, dead-key
reporting), `backend` + orbstack probe, `version`, `doctor`, CI matrix,
goreleaser. `doctor` verified against a live OrbStack VM.

### M1: Agent mode — done

- `dev agent claude`, `dev agent codex`, `dev agent list`.
- Agent plugin format: `agents/<name>/agent.yaml` (install steps, binary,
  default allowlist hosts, config dir path inside home).
- Image overlay: project image + agent layer built on demand (project
  Dockerfile untouched).
- **Egress proxy sidecar** (`internal/netpolicy`), implementing section 4.3
  exactly: internal network with no route out, dual-homed SNI-allowlisting
  proxy (LLM API + language registries + git hosts), proxy-only DNS, no TLS
  interception, denied-connection log surfaced at exit.
- **Agent home volumes**: named volume per agent (configurable per-project),
  OAuth login persists across runs; `dev agent logout <name>` removes it.
- **Live egress notices**: blocked destinations are surfaced the moment
  they happen, not only in the exit summary. A denial mid-run is
  actionable — the user can decide whether to allow it — but only while it
  is still happening. Repeats of the same destination are counted and
  suppressed rather than printed: a retrying client emits hundreds of
  identical denials per second, and a notifier that prints all of them is
  one the user switches off, at which point it protects nobody.
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
  by it; `dev agent update <name>` re-resolves deliberately. Unpinned
  "latest" would silently change what runs inside the sandbox between runs.

Exit criteria: Claude Code and Codex both complete a real task in a sample
repo with egress logging showing only allowlisted hosts; credentials
demonstrably absent from the container when not granted.

Status (in progress on `v2-go-rewrite`): the sandbox and its enforcement
are built and verified against a real OrbStack daemon — allowlist, CONNECT
proxy with no TLS interception, filtering resolver, internal-network
topology, agent registry, overlay images, home volumes, live notices.
`dev agent run claude` runs Claude Code in the sandbox today. Verified in
the live container: uid 1000 (not root), no host credentials present, an
allowlisted API reachable, a non-allowlisted host blocked and reported.

Interactive sessions confirmed working from a real terminal: the TTY
survives `orb -m <vm> sudo docker`, the login persists in the named volume
across runs, and a normal session completes with no egress denials — the
tightened default allowlist covers ordinary use rather than merely being
strict.

`--allow-push` is implemented as ssh-agent forwarding: the socket only,
never a key file, so nothing exfiltratable enters the container and
revoking the grant is killing the agent rather than rotating a credential.
Two things this surfaced that the design had not accounted for:

- The socket arrives group-owned by whatever the host's file sharing
  decided, so the container could not open it. Fixed with a supplementary
  group discovered from inside a container, rather than by changing the uid
  the container runs as — that would hand it the host user's identity.
- ssh does not speak HTTP proxying, and the workload has no other route
  out, so a forwarded agent alone produced "network unreachable". ssh is
  now routed through the same CONNECT proxy, which means git is subject to
  the allowlist like everything else: the project's own origin host works
  under the grant — read from `git remote get-url origin`, on the port that
  remote names — and every other host is blocked and reported. Punching a
  hole for ssh instead
  would have been invisible to the policy, since traffic that never reaches
  the proxy is never reported as blocked.

An agent completed a real task in the sandbox (deterministic egress
summary, doctor sidecar check) with no egress denials during the session.

Remaining before the milestone closes: Codex exercised end to end. Only
Claude has been; `--auth env` exists for orgs that disable device-code
login, but it has not been run against a live key.

### M2: Core loop parity — done

Scope honesty up front: this is the largest milestone, bigger than M0+M1
combined. v1 is ~4k lines of bash plus 8 language plugins, interactive mode,
scaffolding, and devcontainer generation. Two rules keep it from stranding
the project in a permanent dev/dev split:

1. Every v1 command gets an explicit decision recorded in
   docs/PARITY.md — keep, redesign, drop or defer, each with a reason. No
   vague "core loop works", and equally no obligation to reproduce
   something merely because v1 had it.
2. dev may DELEGATE long-tail commands to a vendored copy of the v1 scripts
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
- v1 config migration: `dev migrate` reads `~/.dev-envs/config.yaml`,
  writes v2 config, reports dropped/renamed keys (v1 stopped emitting the
  never-implemented keys in the pre-rewrite fix PR, so the stray-key
  population is frozen at whatever users already have).

Exit criteria: v2 is the tool the maintainer reaches for, with every v1
capability present, deliberately redesigned, or dropped with a reason
recorded in docs/PARITY.md; v1 marked maintenance-only in README.

Note on framing: this milestone was originally specified as parity with a
command-by-command checklist. That was the wrong target. v1 is a
prototype, and parity would have forced v2 to reproduce behavior that was
never good while making a deliberate removal look like a regression. The
checklist survives as a disposition record — keep, redesign, drop, defer —
rather than as a list of boxes that must all be ticked.

### M3: Multi-backend + supply chain — partly done

- Plain docker backend (Linux, colima, Docker Desktop); backend auto-detect
  with config override.
- Digest pinning: done, as `dev pin`. It resolves every image the
  Dockerfile builds FROM and records the digests in the project file
  rather than rewriting the Dockerfile — a language template is shared by
  every project using that language, so the pin belongs to the project.
  `dev pin --update` re-resolves deliberately, and `dev build` reports
  what is still unpinned so the gap is visible rather than assumed.
  This is the answer to 4.3.1: egress control governs a running container,
  and pinning governs what a build fetches.
- `dev scan`: done. Runs trivy and grype against the image the project
  actually runs, including its tools layer, and exits non-zero at or above
  a threshold so CI can gate on it. A scanner that cannot run is a
  failure, not a pass: "no findings" and "no scan" are different answers.
  SBOM emission is still open.
- Signed releases: goreleaser + cosign, checksums in release notes,
  brew tap. Deferred until there is a release to sign — signing is a
  release-time concern and building it early means maintaining it before
  it protects anyone.

### M3.1: Requested during dogfooding — done

**Shell completions.** Done. `dev completion install` writes the script
where the shell will find it, since printing a script is not installing
it, and --image completes to a short curated list. Original note follows.

v1 shipped bash and zsh completions and v2 had
none. cobra generates most of it, but the valuable part is completing
`--image` with a short curated list — the general language images and a
slim debian — because the sandboxing case starts with someone who does not
yet know which image to name. A completion that offers every image on the
daemon would be noise; a completion that offers the five sensible starting
points is a teaching device.

**`dev update`.** Done. Original note follows.

Rebuild the project image with its packages upgraded to
current patched versions, and record what changed. Pinning fixes what a
build fetches, which is the right default and also means a pinned project
stops receiving security updates silently — the two features are the same
trade seen from opposite ends, so `update` should re-resolve the pin and
report the move rather than quietly floating.

The logging matters as much as the upgrade: "these packages moved, this
base image moved" is the record that makes a pinned project safe to leave
pinned.

### M4: Team features — done

- Policy file: done. `~/.dev-envs/policy.yaml` restricts network modes,
  forbids settings outright, denies egress destinations, floors the scan
  threshold, restricts registries and imposes resource limits. It is
  enforced at every route in — a flag, a project request, an acceptance
  already recorded, a user's own grant — because a rule with one polite
  entrance is not a rule.

  What "every route in" is, concretely, since the claim was written before
  it was true: `--allow-host` on `run`, `shell`, `console`, `agent run` and
  `agent policy`; `dev allow`; `dev accept` and `dev agent accept`; the
  blocking prompt and the console's dialog, both of which grant while a run
  is in flight; and `dev agent run`, which was reached by none of it — the
  runs with the most reason to be constrained were the ones the policy did
  not bind. Each of those refuses. The assembled allowlist is then filtered
  once more on the way to the sidecar, because the trust file can be edited
  by hand and a grant recorded before a rule must not outlive it; a
  destination dropped there is named on stderr rather than vanishing.

  Stated in the package doc rather than left implied: it is not a defense
  against the machine's owner, who can edit the file. It closes the unsafe
  paths for people who are not attacking their own laptop and makes an
  override deliberate. An org needing more than that needs device
  management.
- devcontainer.json interop: read side done. The image or Dockerfile it
  names and its forwardPorts are used, so a project standardized on
  devcontainers works unmodified. What is deliberately not honored —
  containerEnv, mounts, remoteUser, postCreateCommand — is reported on
  every build: those are grants in this model rather than settings, and a
  config half-honored silently would leave the user believing the file
  describes what is running. The write side stays with the M2 long tail.
- `dev status` / `dev ps` across projects.

### M5: Live console — done

The aspirational shape: a full-screen terminal UI that owns the session,
with the workload in one pane and everything the tool knows in another —
egress decisions as they happen, container events, and dialogs that ask
for a decision at the moment of need rather than reporting it afterwards.

This is the natural home for things the CLI can only do awkwardly:

- **Denials become questions.** Done ahead of the UI: `--egress-prompt
  ask` holds the connection while the user answers "once", "this project
  from now on", or "no", and the held request proceeds if the policy gains
  the destination. Firewall semantics — the request waits for a verdict
  rather than failing and needing a retry nobody is watching for. It falls
  back to reporting where nobody can answer, because blocking with no one
  present is a hang, which is worse than a clear failure.

  The limit this exposed was the argument for the console: only one reader
  can own stdin, so outside the console a prompt and an interactive shell
  cannot both have it. Inside it the conflict is gone — the console owns
  the keyboard and routes it, a waiting question outranking the shell.
  Verified with a shell running: a curl blocked mid-command, the question
  appeared, one keystroke answered it, and the held request completed.
- **Agents in the console.** Done: `dev console --agent claude` runs the
  agent with its stored login — the OAuth token lives in the same named
  volume either way — while its blocked destinations become questions
  rather than failures it has to work around. The agent is resolved
  through the same request/acceptance path `dev agent run` uses, so the
  same agent cannot end up on a different image depending on which command
  started it.
- **Ports.** Done: a workload on an internal network cannot publish ports
  itself, since docker needs a gateway and an internal network has none.
  The sidecar publishes and relays instead — it already straddles both
  networks, and one component owning the whole network boundary is easier
  to reason about than two. Inbound is deliberately not subject to the
  egress allowlist: that list answers what the workload may reach, while a
  published port answers what may reach it, and the user answered that by
  asking for the port.
- **Live environment changes.** Done: `dev add <tool>` records the tool
  outside the repository and rebuilds the image with it, so the need is
  met when it appears rather than configured in advance. The record is a
  declaration and the image is rebuilt from it, never `docker commit` —
  the environment stays reproducible and a teammate given the same list
  gets the same result. The derived tag carries a hash of the tool set, so
  an unchanged list reuses the built image and any change produces a new
  one; the tag cannot lie about its contents.
- **Multiple panes for what is already collected**: the egress log, the
  agent's output, build progress, the resolved policy for this run.
- **An interactive shell in the pane.** Done: the workload runs on a
  pseudo-terminal and its screen is rendered through a VT emulator. The
  console cannot pass bytes through, since it draws its own layout — a
  shell's cursor movement and redraws would otherwise corrupt it. The
  emulator interprets them instead. ctrl+] leaves, because every other key
  belongs to the shell.

Two design constraints that are not negotiable, because the whole point of
the console is to relax something the current design deliberately froze.

**A control plane must never be reachable from the workload.** Section 4.3
fixes the sidecar's policy at startup precisely so a compromised workload
cannot rewrite its own allowlist. A console that edits policy live needs a
channel — and that channel must exist only on the host side: a unix socket
bind-mounted into the sidecar from the host filesystem, or the sidecar
holding a connection out to the console process, never a listener on the
internal network. If the workload can reach the thing that changes the
policy, the policy is advisory. This is the single highest-risk part of
the feature and it should be built with that stated in the code.

**Live changes must be recorded as declarations, not baked into a pet
container.** The tempting implementation of "install it and keep it" is
`docker commit`, which produces an image nobody can reproduce and a
project that works only on the machine where the command was typed. The
console should instead append to the project's configuration — a package
list, a tool list — and rebuild the image from it. The user gets
persistence; the project keeps a Dockerfile that explains itself and a
teammate gets the same environment. If a change cannot be expressed as a
declaration, it should be explicitly ephemeral and labelled as such.

A third, softer constraint: the console must not become the only way to
use the tool. Everything it does needs a non-interactive equivalent, or CI
and scripted use fall off a cliff.

Sequencing: this depends on M2's core loop existing (it wraps run, shell
and build) and on M1's egress events (it renders them). It replaces v1's
`dev interactive`, which is a menu wrapping commands rather than a live
view, and which cannot be delegated to v1 because it starts containers.

### Deliberately out of scope

Compose-style multi-service orchestration, Kubernetes, Windows, remote
(cloud) backends, GUI. Revisit only after M4 is real.

## 6. Compatibility and migration

- On-disk language plugin format: unchanged (v2 reads the same
  `~/.dev-envs/languages`).
- Config: keys that describe real behavior carry over; v1's never-implemented
  keys (network_mode, auto_host_networking, port_range, port health checks)
  are dropped with a loud migration note. `pass_env_vars` carries over but is
  a grant: a project asking for it needs `dev accept` before any run honors
  it. `mount_ssh_keys` is the one key v1 honored that v2 withdraws — a
  private key inside a container is an exfiltratable secret — and `dev
  migrate` says so, naming ssh-agent forwarding as the replacement.
- CLI: no compatibility promise. The binary ships as `dev` during M1-M2 and
  takes over the `dev` name when v1 is retired (installer keeps `dev` as an
  alias to `dev` from M2 on).
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

## 8. What actually happened

The plan held. Agent mode first was right: the proxy sidecar, the trust
store and the backend abstraction were all built for it and everything
since has reused them unchanged.

Three things were bigger than the estimate, and all three for the same
reason — they touched a terminal:

- **The live console.** The proxy, the resolver and the trust model were
  each straightforward. Rendering a workload's own TUI inside another one
  was not: it took a recording harness, a bisect, and the discovery that
  reading the workload's pty shared a goroutine with rendering it, so at
  188×52 the reader stalled and the workload blocked at exactly 1087 bytes.
- **Blocking prompts.** Holding a request while a human decides means the
  prompter and the workload both want stdin. Which one gets it is now an
  explicit decision the run reports, rather than a race.
- **Escapes.** `ctrl+]` is untypeable on a Finnish keyboard. Obvious in
  hindsight, invisible from inside a US layout.

Things that were smaller than expected: the egress topology (an internal
network plus a dual-homed sidecar does the whole job — no iptables, no
NET_ADMIN), and reading devcontainer.json.

Several capabilities were not in the original plan at all and came out of
using the tool: run history, grant review against it, private clones,
the devcontainer write side, and the guided front door.

## 9. What is left

Nothing here blocks daily use.

- **Plain docker backend** (M3). Only matters off OrbStack — Linux, colima,
  Docker Desktop. The abstraction exists and is exercised by one
  implementation, which is the part that usually rots.
- **SBOM emission** (M3). Worth doing when something consumes it.
- **Signed releases** (M3). A release-time concern: goreleaser, cosign,
  checksums. Building it before there is a release to sign means
  maintaining it before it protects anyone.
- **Codex end to end.** Runs, but has never been driven through a real
  session with a key.
- **Brokered credentials** (4.3.2). A narrow host-side broker for specific
  privileged operations, generalizing what `--allow-push` already does with
  the ssh-agent socket. Wanted only when a real case demands it.

### Cut order, if it ever matters again

M4 first, then M3's extra backends. The trust model and egress control are
not cuttable: they are the tool.
