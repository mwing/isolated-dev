# Roadmap: isolated-dev

Status: shipped. The Go tool is `dev`; the bash tool it replaced is `dev1`,
kept in [v1/](../v1/).
Owner: @mwing

This began as a proposal and is now the design document. Sections 1–4
describe the tool as built and are what the code's `ROADMAP 4.x` comments
point at. Section 7 is the retrospective.

It holds no task list. Open work is in [BACKLOG.md](BACKLOG.md), in one
place, so the two cannot disagree about what is left.

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
3. **Trust is per project and revocable.** Access grants attach to a project,
   are stored outside the project tree, are visible, and are revoked in one
   command.

   As built: `dev grants` lists them with the last time anything used each
   one, `dev revoke` removes one, and `dev grants prune` offers back the
   entries no recorded run has reached. The identifier is the project's
   path — not a fingerprint of the repository, which cannot be had honestly
   (4.2.1).

The bash v1 remains functional and gets bug fixes only. The CLI contract
may change freely (breaking changes are acceptable and expected), which two
releases have already used: agents clone by default, and a repository's own
Dockerfile is not built until it is accepted.

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

The honest cost of choosing this path was that the egress proxy and trust
store arrived after the skeleton and agent mode rather than next month. The
fallback, if that had slipped, was backporting the sidecar to bash. It was
not needed, and the bet paid: the argv defect class has not recurred once,
and every bug found since has been in glue or in judgement — never in
whether a value with a space survived.

## 3. Architecture

```
cmd/dev/                 main, cobra command tree
cmd/dev-proxy/           the egress sidecar's own binary
assets.go                the plugins and the sidecar's source, embedded
internal/cli/            the commands; the glue layer
internal/config/         schema, defaults, layering (flags > env > project > global), provenance
internal/trust/          per-project grants, acceptances, tools
internal/policy/         the rules that bind the user, not just projects
internal/backend/        Backend interface + drivers
    orbstack/            orb -m <vm> docker ...
    docker/              plain docker CLI (Linux, Docker Desktop, colima)
internal/runner/         exec wrapper; every external command goes through here
internal/container/      RunSpec and its rendering; build, run, exec, networks
internal/netpolicy/      proxy, filtering resolver, ClientHello inspection, sidecar lifecycle
internal/agent/          agent registry, volumes, run spec
internal/clone/          private copies of a repository
internal/history/        what each run reached, recorded per project
internal/console/        the full-screen view and its terminal emulation
internal/wizard/         the guided front door
internal/langs/          language plugin loading (same on-disk format as v1)
internal/detect/         project type, version, port detection
internal/devcontainer/   devcontainer.json, read and written
internal/scaffold/       dev new
internal/scan/           trivy/grype integration with real exit codes
internal/project/        detection results assembled into what a run needs
internal/ui/             output formatting
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

## 5. What was built, and where the work now lives

The milestone plan that used to occupy this section is gone: every
milestone shipped, and a list of finished work reads as a list of pending
work to anyone who did not watch it finish. What each milestone produced is
described above, in the architecture and security model it produced.

Open work is in [BACKLOG.md](BACKLOG.md) — one file, so there is no second
place to look and no chance of the two disagreeing.

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
- CLI: no compatibility promise, and the rename has happened. The Go tool
  is `dev`; the bash tool installs itself as `dev1` from `v1/install.sh`
  and stays usable beside it. Both read `~/.dev-envs`, so config and
  language plugins are shared rather than duplicated.
- v1 lifecycle: bug fixes only. It lives in `v1/`, still installs, and its
  test suite still runs in CI — which caught real breakage when the plugin
  templates were renamed. Retiring it is a decision for when nobody reaches
  for it, not a milestone.

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
