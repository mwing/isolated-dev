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

### B7. Fuzz the ClientHello parser — `todo`

Hand-rolled binary parsing of attacker-controlled bytes, which has already
had one bypass (record fragmentation, `8e9423e`).

**Done when:** `FuzzParseClientHello` runs in CI with a seed corpus, and
the DNS name parser and allowlist entry parser have the same treatment.

### B8. One `dev accept` — `todo`

`dev accept` takes settings and `dev agent accept` takes egress, and the
docs have to explain that egress grants belong to the project rather than
the agent. The split is an implementation detail leaking into the UX.

This is not the merge previously rejected: the two decisions stay separate
objects and separate records. One *workflow* presents both.

**Done when:** `dev accept` shows host access and network together, the old
spellings remain as hidden aliases, and no doc has to explain the
difference.

### B9. Bare `dev` is the front door — `todo`

The root command has no default action while the README tells newcomers to
run `dev interactive`. Someone who has to be told the name of the guided
mode has already been failed by it.

**Done when:** bare `dev` in a project runs the guided view, and bare `dev`
outside one prints help.

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

**Dropped:** removing `dev new`. The conceptual-surface argument is fair,
but scaffolding is in use and its removal is a product decision, not a
security one.
