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

### B10. Tell the truth when a grant will not be usable — `done`

`dev allow db.example.com:5432` reads as "Postgres is now reachable". It is
not: the workload has no route, and a Postgres client will not speak HTTP
CONNECT. Only the proxy-aware clients and the agent's explicit
`ProxyCommand` get through.

Measured in a sandbox before writing the message, both lines from the same
container to the same granted destination in the same run:

```
raw TCP:      FAILED (OSError: [Errno 101] Network is unreachable)
via CONNECT:  HTTP/1.1 200 Connection established  →  SSH-2.0-...
```

So the grant is real and the reachability is not, and the error a user gets
names the network rather than the policy — which sends them to the wrong
place. `dev allow` and `dev accept` now say what is permitted, what works
unchanged, and what has to be wrapped. It fires only for explicitly named
non-default ports: a note that appears on ordinary grants is a note that
stops being read.

CONCEPTS carries the table. B16 built the SOCKS listener that makes the
second row work by itself rather than by explanation, and the note now
names `ALL_PROXY` instead of describing a dead end.

### B11. Warn about the build context before sending it — `done`

`dev doctor` reported an absent `.dockerignore`; the build did not, which
is where the seconds are actually lost. Nobody runs doctor while waiting
for a build, and the wait is the symptom.

A build over 200 MiB of context now says so, and names the directories
responsible:

```
⚠  No .dockerignore: this build sends 420M in 5 files to the daemon.
   Biggest: node_modules 260M, .git 120M, dist 40M
   None of it is needed to build the image — your working tree is mounted
   at /workspace when a run starts regardless. `dev` offers to write one.
```

Naming the entries is the actionable half: a total says there is a
problem, `node_modules 260M, .git 120M` says which two lines fix it. No
generic pattern list is printed — a suggestion derived from the tree in
front of the user beats one guessed from the language.

It stays quiet under the threshold and once a `.dockerignore` exists, since
a warning that cannot be made to go away is one people learn to scroll
past.

Found on the way: `contextSize` stopped walking at 20,000 files and
returned the partial total as if it were the whole one — so on exactly the
oversized tree this feature targets, doctor understated the number and the
threshold might never have tripped. The walk now reports that it stopped,
and both doctor and the build say "at least" when it did.

### B12. Docker socket as break-glass — `done`  *(promoted from P1: absorbs B2)*

Mounting it is root on the docker host, and the sandbox does not contain
it. It travelled the same path as allowing a hostname.

It also carried the one consequence that made B2 look worth doing: a
persistent acceptance at a path is inherited by whatever occupies that path
later, and value-sensitivity does not help because the new request matches
the old value exactly. Per-run closes that without needing to know whose
repository it is.

`--allow-docker-socket` on `run`, `shell` and `console` authorizes it for
one run and writes nothing down — verified against a live daemon: the
socket arrives in the container and no project record is created at all.
`dev accept` refuses the key outright unless given `--remember`, so
`dev accept --all` — a sentence about the project in front of you — cannot
also be the sentence that hands over the daemon a year from now. A blocked
run names the per-run flag, because "accept it" is the hint that works for
every other setting and is a dead end for this one.

The flag also has to satisfy the consent check, not just the grant.
Otherwise the run stops for the very setting the flag exists to permit, and
the break-glass is unreachable — there is a test for exactly that.

Agents are unaffected: they receive none of the host grants at any time.

### B13. Pin CI dependencies — `done`

Actions are referenced by tag and `govulncheck` is installed with `@latest`
— the same moving target this tool warns about for images. The linter
already broke this way once.

**Done when:** actions are pinned and `govulncheck` names a version.

---

### B24. Host-side git against a clone is unsandboxed — `doing`

*Numbered after B23 and placed here because it is a live exposure in
shipped code, not forward-looking work.*

The clone's `.git` is attacker-controlled: an agent has unsupervised write
access to it, and the host then runs git against it, as the user, with
their SSH keys in reach — which is what `--clone` exists to protect.
Verified on git 2.47.3, in a clone's own config:

```
core.fsmonitor = ./pwn.sh  +  git status --porcelain  ->  PAYLOAD-RAN
same, with -c core.fsmonitor=                         ->  (did not run)
```

`status --porcelain` is what `State()` and `driftNotes` already run, and
`dev clone diff` runs `diff HEAD`. `filter.*.clean`, `diff.*.textconv`,
`core.hooksPath`, `core.pager` and `alias.*` are the same class, and
`[include] path = ~/.gitconfig` pulls back exactly the settings
`gitConfigCarried` filters on the way *into* the container. That filter
reads as though the problem were solved; it is solved in one direction.

Two more findings of the same shape: `git replace` makes a host-side
review show benign content while a fetch delivers the real commit, so
review has to report from the project after the fetch rather than from the
clone before it; and a ref name may begin with `-`, so no string read out
of a clone may ever be passed to git as an argument.

**Done, first pass:** every git invocation the `clone` package makes now
carries `core.fsmonitor=`, `core.hooksPath=/dev/null`, `core.pager=cat`,
`core.editor=true`, `core.sshCommand=true` and
`uploadpack.packObjectsHook=`, with `GIT_NO_REPLACE_OBJECTS=1`,
`GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=` and `GIT_ATTR_NOSYSTEM=1`, built
with `append(os.Environ(), …)` because `runner.Command.Env` replaces the
environment. A test plants five executable payloads in a clone's config and
asserts none runs through `State` or `driftNotes`, and that both still read
correctly — hardening that breaks the feature it protects is not a fix.

Two flags that look like they belong and do not, both caught by the tests
rather than by reasoning: `protocol.file.allow=never` forbids the local
clone this package exists to make, and `diff.external=` makes git try to
execute the empty string. The first belongs on a specific fetch; the second
is `--no-ext-diff`, now on the one diff this package runs.

**Done, second pass: the config quarantine.** Host reads of a clone run
with its repository config renamed aside and a minimal tool-authored one in
its place, which is the only thing that closes `filter.<driver>` named by
an in-tree `.gitattributes` — the driver name is the attacker's to choose,
so there is no key to blank. Tested with exactly that shape.

Crash safety is the design rather than a caveat: the original is renamed,
never rewritten and never deleted, so the worst a crash leaves is the real
config beside the stand-in under a known name — with a comment in the
stand-in saying so. Every call repairs that before doing anything else, so
recovery is an ordinary path exercised constantly rather than one that runs
only after the crash nobody planned for. Restore refuses if it finds a
config it did not write, rather than choosing between two.

**Still to do:** `State` asks the clone where the project is
(`remote get-url origin`), which is the shape B2 named — reading an
identity out of the repository whose identity is in question. It is
deliberately outside the quarantine, because the quarantine removes it, and
the fix is for the caller to pass the project in. That is a signature change
through every caller of `State`, so it is the next increment.

Also still to do: refusing
clone-derived strings as git arguments, sanitizing control characters out
of commit subjects and author names, and reporting anomalies
(`refs/replace/*`, an unexpected `.git/shallow`) instead of working around
them. `dev clone diff` in `internal/cli` still runs unhardened.

**Done when:** one hardened helper is the only way host code runs git
against a clone — repo-config quarantined (with a restore that survives a
crash and a signal, since a half-quarantined clone has lost its identity
and its remote), hardened flags set anyway, clone-derived strings never
used as arguments, output sanitized for control characters, and anomalies
like `refs/replace/*` or an unexpected `.git/shallow` reported rather than
worked around.

## P2 — later, and only if wanted

### B14. Learning mode — `todo`

The honest replacement for B3: traffic still goes through the proxy and is
recorded, unknown destinations are permitted for that run, and afterwards
the run offers to save what it reached. Weaker for one run, auditable, and
it converges on the right allowlist instead of away from it.

### B15. Plain-docker backend, after UID/GID — `done`

**UID/GID: done.** The image is built for the host's uid — every language
template takes `DEV_UID`/`DEV_GID`, creates the account with them, and
refers to ownership numerically — and runs pass `--user <hostuid>:<hostgid>`.
The uid is part of the image tag, so two people on one machine cannot hand
each other an image whose account is the other one.

It was worse than this entry predicted. On Linux the container could not
write to the workspace *at all*:

```
sh: can't create from-container.txt: Permission denied
```

Every run that wrote anything failed. The fix had to be the image knowing
the uid rather than merely being passed it — measured on one image:

```
as baked uid 1000:  whoami -> appuser        home write: OK
as host uid 501:    whoami: unknown uid 501  home write: DENIED
```

so passing the host uid alone would have fixed the workspace and broken
home and the caches, on both platforms.

The integration test came first, deliberately, and earned it: Linux CI
covered network topology and orphan reaping but had never run a container
over a mounted workspace, so the one platform that could see this was not
looking. It went red on the first push, which was the finding.

**The rest, also done.** The backend is chosen by platform unless
`DEV_BACKEND` says otherwise — OrbStack does not run on Linux at all, so
telling a Linux user to install it was advice for a different operating
system on a machine that already had a daemon. The `--tty off` hint now
recognizes docker's wording as well as orb's; they are two sentences for
one failure and only one was understood. CONCEPTS says plainly that an
escape reaches the OrbStack VM on macOS and the host on Linux, which is a
real difference in what this tool is worth on the two platforms and not
one the sandbox can close.

The doctor items were already fixed while grouping the UI, and this entry
had gone stale saying otherwise.

**Rootless docker is detected and reported, not supported.** It maps
container uid 0 to the host user and every other uid into a subuid range,
so running as the host's own uid — the thing that makes a bind mount
writable everywhere else — lands files under a subuid instead. `doctor`
says so. Accommodating it means running as container uid 0, which is a
different posture and wants its own decision; there is no rootless daemon
here to verify against, and guessing at it would be worse than naming it.

### B16. Non-HTTP ergonomics — `done`

A SOCKS5 listener on the sidecar, so ordinary clients reach granted
destinations without weakening the topology. `ALL_PROXY` is set in the
container alongside `HTTP_PROXY`, so a client that reads it needs telling
nothing.

Verified in a sandbox, the same probe B10 was measured with:

```
raw TCP:      FAILED ([Errno 101] Network is unreachable)
via SOCKS5:   reply code 0 (0 = success)  ->  SSH-2.0-...
```

The topology is untouched — the raw socket still has nowhere to go — and
an ungranted destination is refused with code 2 (`not allowed by
ruleset`), including 169.254.169.254, with the denial in the run's report
like any other.

It is a second door, not a looser one: both front ends call one
`authorizeTunnel`, so the allowlist, the held-request prompt, the
infrastructure guard, the SNI check inside the session and the event log
are shared rather than reimplemented. Two implementations of "may this
connection happen" would be two chances for one to check less, and the one
that checks less is the one nobody notices.

Fuzzing the request parser found a zero-length domain accepted as a
destination naming no host, before it shipped. `FuzzSOCKSRequest` is in
the CI matrix.

### B17. ECH on the security roadmap — `todo`

Encrypted Client Hello (RFC 9849, March 2026) encrypts the real SNI, so
inspecting it stops answering "which site is this". Currently documented as
a known limitation that fails closed. Decide deliberately before it is
common.

### B18. Group the command help — `done`

Twenty-nine root commands in one alphabetical column read as a development
platform rather than "run this project safely", and buried the three or
four commands most people need under the twenty-five they do not.

Seven groups, titled with the words `docs/COMMANDS.md` already uses so the
reference and `--help` do not need learning twice. Nothing was removed or
renamed. A test asserts every visible command is in a real group, since
falling into cobra's "Additional Commands" is silent, and another asserts
the titles still match the reference — it caught the first drift
immediately, the help calling a group "This machine" while the docs still
said "The VM".

Also renamed `newEnvCmd`, which built the `vm` command and carried a doc
comment calling itself `vmCommands`. It named neither.

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

### B23. Clone lifecycle: make a run feel like a fresh clone — `todo`

**The goal, stated as a feeling rather than a mechanism:** every run should
behave as though a clone were made for it and the work merged back
afterwards. No stale clone to discover, no branch to reconcile, nothing to
reason about between runs. Today a clone is keyed by project path and lives
until pruned, so all of that is the user's problem.

**Why it is not simply "clone per run, merge, delete".** Combining two
moved histories is a judgement about code this tool has not read — the same
rule that makes `.devenv.yaml` a request and refuses an automatic merge in
`dev clone apply`. And a clone removed after a merge that conflicted is
work destroyed, which is the one irreversible thing here. So the merge
cannot be automatic, and the delete cannot follow it unconditionally. The
feeling has to be produced some other way.

**Three sources of drift, and the goal has to survive all three:**

1. *Reuse across branches.* Starting work on a new branch reuses the clone
   made from the old one. Cost one real afternoon: an agent worked 64
   commits behind on a branch nobody asked for, and the merge conflicted on
   a 22,000-line lockfile already applied under a different hash. Currently
   only reported — see the drift notes in `internal/clone` — which is a
   stopgap, not the answer.

2. *The project branch moving while the agent works.* This is the one that
   makes "a fresh clone per run" insufficient on its own: the clone is
   accurate when created and diverged by the time `apply` runs, because the
   human committed in the meantime. Keying clones by branch does nothing
   for it.

3. *Uncommitted work in the clone*, which `apply` deliberately does not
   move: `apply` moves commits, and committing is the act that says "this
   is work".

**Directions considered, none chosen:**

- Key the clone by project *and branch*. Removes (1) by construction and
  makes the drift note dead code; does nothing for (2). Costs a fresh clone
  per branch, which on a large repository is not free — measure before
  believing it is cheap. Makes `--clone-depth` more attractive, since a
  short-lived clone needs less history.
- Ephemeral per-run clone with an automatic merge. Delivers the feeling
  exactly and collides head-on with the review rule above.
- Automatic rebase of the clone's commits onto the moved branch when the
  clone is clean and strictly behind. Tempting because it looks like it
  cannot lose anything — until it conflicts, at which point it has stopped
  half-done in a state nobody asked for.
- Record the commit the clone was made from, so `apply` can say precisely
  what moved on each side and offer the narrowest operation that fits. Does
  not automate anything, but replaces "this is a decision" with "here is
  the decision".

**What would make this decidable:** the cost of a fresh clone of a large
repository, measured rather than assumed; and an honest answer to whether
any automatic history operation can be safe enough to perform unasked. If
the answer to the second is no, then the goal has to be met by making the
review fast and obvious rather than by removing it — which is a different
piece of work, and a legitimate one.

**Left open deliberately.** The desired outcome is agreed; the mechanism is
not.

**Proposed mechanism, reordered after review.** Lossless automation at
both ends, verified facts in the middle, and no automatic history combine
ever:

- *Close the loop at run end.* When a run leaves new commits, fetch them
  into the project automatically — the lossless half of today's `apply` —
  and report what came out. This is `applyClone`'s first half relocated,
  and it delivers most of the felt benefit for a fraction of the risk.
  **Do this one first, and live with it before building the rest.**
- *Refresh a provably-empty clone at run start*, so a completed round trip
  leaves nothing to discover. This is where all the data-loss risk is; see
  the conditions below.
- *Report whether the combine would conflict* using `git merge-tree
  --write-tree`, which merges in memory with no checkout and no cleanup.
  Report the operation actually tested, never recommend an untested one,
  and say that git checked for overlap and not for meaning.

**Conditions the middle part has to meet before it is worth building**, all
of them found by reviewing the proposal against the code rather than
against itself:

- The two lossless halves compose into a lossy loop. Run 1's commits land
  on `clone-work`; nobody applies; run 2 sees a clone whose commits are all
  on that ref, calls it empty, refreshes, and force-moves the ref past run
  1's work. Two runs before one apply is an ordinary sequence, so automatic
  fetches need a tool-owned ref namespace the user cannot own — with a
  retention rule, since refs pin objects against `gc` and would relocate
  the disk problem clones already had.
- Emptiness must be proved from the project side. `State()` reads as empty
  after a `git stash`, an `--assume-unchanged`, a forged `refs/remotes/*`,
  or a corrupt config, and empty is the condition for deletion.
- `carryUncommitted` makes every fresh clone dirty from its first second,
  so a naive guard fires on the tool's own artifact for anyone with a
  work-in-progress tree — and the only exit it can offer is `--force`,
  which is the failure B11 names.
- Nothing in the tool knows a clone is mounted. A second run of the same
  project would refresh a clone a running container is writing into, and
  the container keeps writing to an orphaned inode. That needs an
  active-run check and a per-clone lock before any refresh ships.

**Depends on B24**, which is not optional and not sequenced behind this:
automating the lifecycle turns user-initiated host-side git against an
untrusted clone into unattended host-side git against one.

**Still open:** keying clones by project *and* branch (deferred until a
fresh clone's cost is measured on a large repository); separating the
human and agent clone keys, which share a slug today.
