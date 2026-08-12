# Backlog

Forward-looking work, from a second external review of `8e9423e`. Unlike
the queue this replaces, nothing here is a known defect in a security
promise — those were closed. These are places where the *product* makes the
secure path harder to stay on than it should be, plus the work between
"good beta" and something a stranger can install.

The test to apply to each: **does this make the secure path require fewer
decisions, or does it merely add another capability?**

Status: `todo` · `doing` · `done` · `dropped` (with the reason)

---

## P0 — before calling it v1

### B1. Consent before building a repository's own Dockerfile — `done`

`dev run` builds before it sandboxes, and builds are not egress-filtered
(ROADMAP 4.3.1). A repository's own `Dockerfile` also takes precedence over
the language template. So for an unfamiliar repo, the first thing that
happens is arbitrary code fetching arbitrary things over an unrestricted
network — before the sandbox everyone came for exists.

The user's mental model is "dev run means run this safely". The gap between
that and the build path is the largest remaining design problem, and it is
not closed by documenting it.

**Do:** make the build source an explicit, remembered choice. Name it, say
builds are unfiltered, offer the language template as the alternative.

**Done when:** a project whose Dockerfile has never been accepted stops
before building, names the file, and offers the template; the decision is
recorded per project; `--build-source` chooses without prompting; a changed
Dockerfile is reported at build time.

### B2. Trust keyed to repository identity, not just path — `dropped`

The finding is real: grants live at `~/.dev-envs/projects/<slug>-<hash>.yaml`,
hashed from the absolute path, so `rm -rf foo && git clone something-else foo`
inherits foo's grants.

The proposed fix does not work, and the reason is worth keeping.

Binding trust to the git origin means reading an identity **out of the
repository whose identity is in question**. One command defeats it:

```sh
git clone evil foo && cd foo && git remote set-url origin <the-old-url>
```

That is self-asserted data, which is exactly what this tool's founding rule
says not to believe — `.devenv.yaml` is a request rather than authority
*because* configuration inside a repository can grant itself. A remote URL
is the same class of data, and treating it as identity would be a hole in
the middle of the model.

The bind is structural rather than a matter of picking a better field. Any
signal stable enough to be an identity is self-asserted; the one signal
that is not — the content itself, a HEAD commit — changes on every commit,
so it fires constantly and teaches people to click through the prompts that
matter. There is no version of this that both holds and stays quiet.

What survives:

- The dangerous payload — a new project at an old path silently inheriting
  `mount_docker_socket: true`, because value-sensitivity matches on
  sameness — is **B12**, which depends on no identity signal at all.
- The documentation duty: say plainly that grants follow the path, not the
  code and not the repository. Done in ROADMAP 4.2.1.

### B3. Stop recommending `--network open` for unfamiliar repos — `done`

`docs/GETTING-STARTED.md` suggests running open first and tightening after,
then immediately notes that an open run is not proxied, so the allowlist
cannot be derived from it. The advice argues against itself and points a
newcomer at the one mode with no observation.

**Done when:** the advice is gone, and the section points at
`--egress-prompt ask` and `dev history` instead. See B14 for the feature
that should replace it properly.

### B4. Pin the built-in agent versions — `done`

Both built-ins declare `Version: "latest"` and install with unversioned
npm commands, so rebuilding an agent image can silently produce a different
agent. That contradicts everything `dev pin` says about tags.

**Done when:** built-ins name tested versions, `dev agent update <name>`
moves them deliberately and reports old → new, and `dev agent list` shows
what is pinned.

### B5. Real distribution — `done`

Requiring Go 1.26 and `make install` is now the adoption blocker: the
sidecar image and the language plugins already travel inside the binary, so
the binary is the whole product and nothing ships it.

**Done when:** tagged releases carry checksummed binaries for darwin and
linux on both architectures, a brew formula installs it, and `dev version`
reports something other than `source`.

---

## P1 — best security per unit of effort

### B6. Agent runs default to a clone — `done`

An agent cannot reach `~/.ssh`, but it can edit git hooks, `package.json`,
Makefiles and CI files — things the *host* runs later. `--clone` exists and
is not the default, and bringing work back is `dev clone path` plus a
manual fetch, which is too much ceremony for the safer path.

**Done when:** `dev agent run` clones by default, `--in-place` opts out,
and `dev clone diff` / `dev clone apply` make the round trip one command
each. `dev run` and `dev shell` stay in place — a human editing their own
tree is the case the plain mount is right for.

### B7. Fuzz the ClientHello parser — `done`

Hand-rolled binary parsing of attacker-controlled bytes, which has already
had one bypass (record fragmentation, `8e9423e`).

Three targets in `internal/netpolicy/fuzz_test.go`, run per-push for 60s
each and for 10 minutes on a weekly schedule. The properties are the ones a
bypass would break, not "does not crash": a name is only reported for input
that contains one, an unreadable handshake reports unreadable rather than
absent, and a rule survives being printed and read back.

The ClientHello parser held at 43M executions. The allowlist parser did
not: `.`, `..`, `*.a b.com` and `[#]:1` all parsed into rules whose printed
form was empty, unparseable, or — for the last one — a comment, so the
grant vanished on the next read. The fix replaced a list of banned
characters with the actual invariant (a hostname label is letters, digits,
hyphen or underscore, and is never empty), which is what the character
blacklist had been approximating one finding at a time.

### B8. One `dev accept` — `done`

`dev accept` took settings and `dev agent accept` took egress, and the docs
had to explain that egress grants belong to the project rather than the
agent. The split was an implementation detail leaking into the UX.

This is not the merge previously rejected: the two decisions stay separate
objects and separate records, checked against their own policy rules and
acceptable one at a time. One *workflow* presents both, under `Settings`
and `Network` headings, because the project asked for both in one file and
reading half of what a stranger's repository wants is not reviewing it.

`dev agent accept` is now a hidden alias for the same workflow, which does
widen what its `--all` accepts: it covers settings too. That is the merge
working as intended — one review, one confirmation — and the review still
prints everything before anything is recorded.

### B9. Bare `dev` is the front door — `done`

The root command had no default action while the README told newcomers to
run `dev interactive`. Someone who has to be told the name of the guided
mode has already been failed by it.

Bare `dev` runs the guided view in a project, and prints help outside one
or when there is no terminal to draw a full-screen menu on. `dev
interactive` remains as the explicit spelling.

Building it surfaced a bug worth more than the feature: `isTerminal` tested
for a character device, and `/dev/null` is one. So the most common way of
running without a terminal was reported as having one. That made `--tty
auto` ask docker for a terminal it could not attach — the "the input device
is not a TTY" failure that `--tty off` was the workaround for — and it
would have made bare `dev` open a menu with nobody there to read it. It now
asks the terminal layer. The test harness had been carrying a comment
describing the bug and working around it.

### B10. Tell the truth when a grant will not be usable — `todo`

`dev allow db.example.com:5432` reads as "Postgres is now reachable". It is
not: the workload has no route, and a Postgres client will not speak HTTP
CONNECT. Only the proxy-aware clients and the agent's explicit
`ProxyCommand` get through.

**Done when:** granting a non-HTTP port says plainly that the destination
is permitted but the client must be taught to use the proxy, and the docs
say which protocols work out of the box.

### B11. Warn about the build context before sending it — `todo`

`dev doctor` reports an absent `.dockerignore`; the build does not, which
is where the seconds are actually lost. The measured case in the docs is
7.4 GB.

**Done when:** a build whose context exceeds a threshold says so and offers
the `.dockerignore` the wizard already generates.

### B12. Docker socket as break-glass — `todo`  *(promoted from P1: absorbs B2)*

Mounting it is root on the docker host, and the sandbox does not contain
it. It currently travels the same path as allowing a hostname.

It also carries the one consequence that made B2 look worth doing: a
persistent acceptance at a path is inherited by whatever occupies that path
later, and value-sensitivity does not help because the new request matches
the old value exactly. Making this per-run closes that without needing to
know whose repository it is.

**Done when:** it is per-run rather than quietly persistent, remembering it
takes a deliberate flag, and the run header keeps saying what it means.

### B13. Pin CI dependencies — `done`

Actions are referenced by tag and `govulncheck` is installed with `@latest`
— the same moving target this tool warns about for images. The linter
already broke this way once.

**Done when:** actions are pinned and `govulncheck` names a version.

---

## P2 — later, and only if wanted

### B14. Learning mode — `todo`

The honest replacement for B3: traffic still goes through the proxy and is
recorded, unknown destinations are permitted for that run, and afterwards
the run offers to save what it reached. Weaker for one run, auditable, and
it converges on the right allowlist instead of away from it.

### B15. Plain-docker backend, after UID/GID — `todo`

Selection is `DEV_BACKEND=docker` and undocumented, which is the right
amount of exposure for now. Every hardened run is `1000:1000`; macOS file
sharing hides that and Linux will not. Identity mapping comes first, then
auto-detection, then the docs.

The driver is done and works: a full `dev run` through it builds, mounts,
filters DNS, blocks egress and writes back. What is left, from driving it:

- **UID is the whole task.** `runspec.go` sets `1000:1000` and all eight
  language templates create `appuser` at 1000 and chown `/workspace`, the
  home directory and the caches to it. So passing the host's uid instead
  hands the container an image whose own directories belong to someone
  else — either those become group-writable or this needs userns-remap.
  Not a one-line change, and macOS cannot show the failure: a container
  write there arrives on the host owned by the host user.
- Linux still defaults to the OrbStack driver, so the first run fails with
  "orb not on PATH" rather than using the daemon that is present.
- `doctor` says `✓ orb CLI (/usr/bin/docker)` and prints a `vm_name` for a
  backend that has no VM.
- The "re-run with `--tty off`" hint is on the OrbStack path only; the
  docker path leaks docker's own `cannot attach stdin to a TTY-enabled
  container`.
- `docs/CONCEPTS.md` says an escape "reaches that VM rather than your Mac".
  With a local daemon there is no VM and that sentence is false. It has to
  say so before this is a documented configuration.
- Rootless docker is unexamined.

The gap that would let the UID break ship: Linux CI covers network
topology but never runs a container over a mounted workspace, so nothing
would catch it. That test comes first — it is what makes the rest
verifiable.

### B16. Non-HTTP ergonomics — `todo`

A SOCKS listener on the sidecar would let ordinary clients reach granted
destinations without weakening the topology. Pairs with B10.

### B17. ECH on the security roadmap — `todo`

Encrypted Client Hello (RFC 9849, March 2026) encrypts the real SNI, so
inspecting it stops answering "which site is this". Currently documented as
a known limitation that fails closed. Decide deliberately before it is
common.

### B18. Group the command help — `todo`

Twenty-odd root commands read as a development platform rather than "run
this project safely". Grouping the help output is cheap and changes the
impression without removing anything.

### B19. SBOM emission — `todo`

`dev scan` reports vulnerabilities; it does not emit a bill of materials.
Worth doing when something consumes one — an SBOM nobody reads is a file
this tool has to keep correct for no reader. Carried over from the
milestone plan, where it was open for the same reason.

### B20. Codex end to end — `todo`

The definition runs and the image builds, but it has never been driven
through a real session with a key. Everything it shares with the claude
path is exercised; what is unverified is the part that is its own.

### B21. Brokered credentials — `todo`

A narrow host-side broker for specific privileged operations, generalizing
what `--allow-push` already does with the ssh-agent socket: the container
asks for an action and never holds the secret. See ROADMAP 4.3.2 for why
this rather than injecting credentials at the proxy, which would cost the
no-TLS-interception property. Wanted when a real case demands it, not
before.

### B22. Detecting secrets in the workspace — `dropped`

Projects contain `.env` files and other credentials, and the workspace is
mounted into the container. An agent reads files by default and its own API
endpoint is an allowlisted bidirectional channel (ROADMAP 4.4), so a secret
in the tree is the payload that path is worth having.

**Dropped, deliberately:** this tool will not parse project files looking
for secrets. Detection is a different job with its own failure modes — false
positives that train people to ignore it, false negatives that imply a
coverage it does not have — and doing it badly beside a security promise is
worse than not doing it. There are tools for this.

What is true and free is worth knowing instead: a clone carries tracked
files and untracked ones, and skips ignored files. So a `.gitignore`d `.env`
never reaches an agent, which is the common case. A committed secret does,
and no exclusion this tool could make would help — deleting it from the
clone's checkout leaves it in the clone's history, one `git show` away.

The remedies that work are the ordinary ones: gitignore it, `git rm` it, or
run `--offline`.

**Dropped:** removing `dev new`. The conceptual-surface argument is fair,
but scaffolding is in use and its removal is a product decision, not a
security one.
