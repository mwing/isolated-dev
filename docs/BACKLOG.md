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

**Done, third pass**, from an external review that found six things:

- The quarantine failed open on an unexpected layout. Measured: with `.git`
  replaced by a `gitdir:` pointer, `<clone>/.git/config` does not stat while
  git works perfectly through the pointer — so "no config here" was read as
  "nothing to set aside" and host git ran against a config of the agent's
  choosing. `Lstat` now requires a plain `.git` directory and a regular
  config file, and refuses anything else. Failing open in the one function
  whose job is to fail closed.
- Restore errors were discarded, so a command could succeed while leaving
  the clone quarantined. They are returned now.
- The test helpers inherited the runner's global `commit.gpgSign`, so the
  suite failed for anyone whose signing key was unavailable — a public repo
  whose tests only pass on one machine. Disabled per test repository.

**Done, fourth pass — the review's two criticals.**

*#3, the forged origin.* The project path is recorded beside the clone
(`<clone>.project`, a sibling, never a file within it — inside would be
inside the bind mount and so inside the agent's reach). `State` reads that
instead of asking the clone. No signature change: `clone list` and `prune`
enumerate clones whose projects the caller does not know, so passing it in
was never going to work for them, and recording it at `Prepare` answers for
every caller at once. Unprovable fails closed — a clone with no recorded
project counts everything as unmerged and is refused rather than deleted.

*#1, the unhardened cli path.* `internal/cli` had grown its own `cloneGit`
forwarding straight to git. There is now one exported way to read a clone
(`clone.Read`) and one for operations that read it from outside
(`clone.WhileQuarantined`, used by the fetch, which starts `upload-pack`
inside the clone where `uploadpack.packObjectsHook` names a host program).
A test plants four payloads plus a `.gitattributes` filter and drives
`dev clone diff` and `dev clone apply` through them.

**Still to do, and sharper than this entry had it.** The review turned the
`origin` trust from a principle into an exploit: setting `origin = .` in a
clone makes `State` check the clone's commits against *itself*, so they all
look contained and `dev clone rm` / `dev clone prune` delete them without
`--force`. That is data loss, not just misplaced trust. `State` asks the
clone where the project is
(`remote get-url origin`), which is the shape B2 named — reading an
identity out of the repository whose identity is in question. It is
deliberately outside the quarantine, because the quarantine removes it, and
the fix is for the caller to pass the project in. That is a signature change
through every caller of `State`, so it is the next increment.

Two more from the review, both real and unfixed: `--clone` cannot start
from an unborn repository (`git diff HEAD` fails with no commits —
verified), and untracked filenames with leading or trailing whitespace are
mishandled because the file list is newline-split and trimmed rather than
read with `-z`.

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

### B28. Test the invariants, not the features — `todo`  *(do first)*

The dangerous bugs in this codebase are increasingly composition failures
rather than missing mitigations: reuse meeting branch changes, captures
meeting wildcard semantics, a lossless fetch meeting non-unique names, a
sandboxed clone meeting host-side git. The individual pieces are
defensive; the pairs are not.

So write the invariants down — eight to twelve of them — and test those
adversarially rather than testing features:

- an agent can never modify the source repository
- an agent never receives git objects unrelated to its starting history
- no successful run can make previously captured work unreachable
- two simultaneous runs can never write the same git working tree
- host git never executes configuration an agent controls
- a project request can never widen access without a user decision

Every finding of the last two reviews violates one of those and none
violated a feature test — including the ones introduced while fixing
something else. First rather than last: written before B25 and B26, these
invariants are the tests those fixes should be verified against, and the
thing most likely to catch the next composition failure before a reviewer
does.

### B25. A clone hands over more history than the run needs — `done`

*Pre-release. Numbered late, placed here because it is a confidentiality
property that does not hold today.*

`git clone --no-hardlinks <path>` is still git's *local* clone: it copies
the whole object database. `--no-hardlinks` protects the source from
modification and does nothing about what is readable. Measured on a
controlled repository:

```
--no-hardlinks --single-branch:  branch -a hides secret-branch
                                 cat-file -e <sha> -> readable
                                 `git show` prints the secret
                                 objects: source 6, clone 6
file:// --single-branch --no-tags: objects 3, secret absent
```

So `--single-branch` on a local clone is cosmetic: it hides refs while
copying every object, and `cat-file --batch-all-objects` enumerates the
lot — other branches, deleted blobs, everything. Only the smart transport
narrows the data. The reasoning here had been about integrity ("the agent
cannot corrupt the parent") and never about confidentiality.

**Measured cost, which changes the remedy.** On a 22,655-commit repository
with 435M of history:

| mode | time | .git | objects |
|---|---|---|---|
| `--no-hardlinks` (today) | 1.13s | 435M | 207,152 |
| `file:// --single-branch --no-tags` | 4.21s | 263M | 183,344 |
| the same, `--depth 50` | 1.63s | 118M | 11,709 |

The smart transport removes 12% of the objects, and that 12% is the whole
point: it is precisely what is *not* reachable from the cloned branch —
other branches' unique commits, deleted blobs, abandoned work. The figure
is small because branches in a long-lived repository share nearly all
their ancestry, not because the protection is partial. It removes exactly
the data that should not be there.

The 3.7x on time is 1.13s to 4.21s, which is not a number anyone waits
for. There is no cost argument against this change.

Depth is a separate axis and removes volume rather than exposure:
single-branch plus a modest depth is faster than single-branch alone and
carries 6% of the objects.

That reopens the depth-default question shelved earlier, on different
grounds: not "clones are large" but "a clone should carry the history the
run needs". Note from that earlier measurement that depth 20 and depth 1
are within 1MB of each other, so a default of 20 costs nothing over 1 and
keeps `git log` and `blame` useful.

**Done:** clones use `file:// --single-branch --no-tags`, always, with
`--depth` when asked for. The invariant test that found it now asserts a
commit from another branch is absent from the clone's object store.

**The cost is real and accepted, not hidden.** An agent cannot see `main`
to rebase onto, and cannot fetch it — the container cannot reach the
project path, which this design relies on elsewhere. That is a narrowing
of what an agent can do, chosen because the alternative was handing over
every branch and every deleted blob in the repository. If a run genuinely
needs the base branch, the answer is to fetch that one ref deliberately
rather than to widen the clone back to everything; that is a smaller
feature and is not built.

### B26. Composition failures around clones and captures — `done`

*Pre-release. Three defects that each arise from two individually-safe
mechanisms meeting.*

- **Detached HEAD deletes every branch's captures — done.** `CapturedRefs`
  now treats an empty branch as matching nothing, and a wildcard has to be
  asked for by name (`AllCapturedRefs`), so "I do not know which branch"
  can no longer be spelled the same way as "all of them". Deletion is
  governed by one invariant: a capture is dropped only once its tip is
  reachable from a branch, which also means an apply of one piece of work
  cannot take an unrelated capture with it. The invariant test detaches
  HEAD, applies, and asserts the other branch's capture survives.
- ~~**Detached HEAD deletes every branch's captures.**~~ `CurrentBranch`
  returns `""` for a detached HEAD and `CapturedRefs(…, "")` means *all
  branches*, so one sentinel means both "this branch" and "everything".
  `apply` on a detached HEAD sweeps and drops captures belonging to every
  branch. Live data loss, one line, and it needs no coincidence — do this
  first. Fix: a separate `AllCapturedRefs` so detached cannot mean
  wildcard, and one invariant for deletion — never drop a capture unless
  its tip is reachable from a local branch.
- **Reuse overwrites provenance — done.** `Prepare` records provenance when
  the clone is made and never rewrites it, filling it in only when missing.
  Re-recording on reuse said the clone came from wherever the host had moved
  to since, which is the capture-path bug one level up. The invariant test
  creates a clone on one branch, switches the host to another, reuses, and
  asserts the record did not move.

  The branch-mismatch *stop* the review asked for is not here: refusing
  needs a per-run flag to resolve it (B12's rule), and that flag is part of
  part 2's refresh design, which already lists it. The loud warning stays
  until then.
- ~~**Reuse overwrites provenance.**~~ `Prepare` records the host's *current*
  branch and base when it reuses a clone, which reintroduces one level up
  the bug the capture path was just fixed for: the clone may still be on
  the previous branch. Keep what was recorded at creation, and fail closed
  on a branch mismatch rather than warning and continuing.
- **Captures are append-only — done.** The fetch no longer uses `--force`,
  so a capture ref can fast-forward and nothing else. Two runs sharing an
  id and holding different histories now produce two refs, the second
  named by its own tip, rather than one overwriting the other. The
  invariant that found it asserts earlier work stays reachable. Random ids
  are still worth doing; they are no longer load-bearing.
- **Concurrent runs share one working tree — done.** A run takes an flock
  on a file beside the clone and holds it for its lifetime; a second run of
  the same project is refused with what is wrong rather than quietly
  writing the same index. flock rather than a file this tool creates and
  removes, because the kernel releases it when a process dies and a crash
  is the case nobody cleans up after. The invariant test asserts the second
  attempt fails and that the clone is usable again afterwards.

  Random capture ids are still worth doing but are no longer load-bearing:
  captures became append-only, so a shared id now produces a second ref
  rather than an overwrite.
- ~~**Concurrent runs share one working tree.**~~ `clone.Dir` is one mutable
  directory per project with no cross-process serialization, so two runs
  write the same index and refs. Related: `captureID` is
  second-resolution while its comment promises uniqueness, and capture
  fetches with `--force`, so two runs in the same second address and
  overwrite one ref. Random ids and append-only capture refs; a lock held
  for the life of a run.

### B27. Smaller findings from the same review — `done`

- ~~The agent's home volume is per-agent, not per-project, and all of
  `/home/dev` persists — so project A can write agent configuration,
  instructions or MCP settings that project B later consumes.~~ **Done.**
  The volume is `dev-agent-<name>-config` and mounts at `ConfigDir`; the
  rest of the home lives and dies with the container, as it already did for
  `dev run` and `dev shell`. An existing login is carried out of the old
  home volume once — the config directory only, or the narrowing would undo
  itself on every machine that had run an agent before.

  Agents are told where that directory is (`config_env`:
  `CLAUDE_CONFIG_DIR`, `CODEX_HOME`), so state they would otherwise write
  beside it lands inside the one place that persists.

  **Residual, and inherent:** the config directory itself is still shared
  between projects, because one login means one place the credential lives
  and these agents keep their settings beside it. A per-project config
  directory would close it at the cost of a login per project; worth
  offering as a choice if anyone wants it, not worth imposing.
- ~~`build_source` consent hashes the Dockerfile, but `COPY . .` plus
  `RUN ./build.sh` means the trusted program is the Dockerfile *and the
  build context*.~~ **Done.** Hashing the context would re-ask on every
  save, which is a prompt nobody reads, so the wording now says what is
  actually accepted — this repository supplying build instructions, the
  file and whatever it runs from the directory — and a test states the
  limit so it cannot quietly become a claim: changing `build.sh` does not
  ask again, changing the Dockerfile does. The real fix is still
  egress-filtered builds (ROADMAP 4.3.1).
- ~~Agents have no default memory or CPU ceiling.~~ **Done:** 8g and
  NumCPU-1, printed when they came from the default. The bind-mounted
  clone is still an easy disk DoS.
- ~~`RuntimeImage` is described as pinned while `debian:bookworm-slim` and
  `node:22-bookworm-slim` are mutable tags.~~ **Done.** The overlay is now
  built through `project.ApplyPins` like any other Dockerfile, and
  `dev pin` resolves every agent's images alongside the project's own — so
  the one build the tool performs itself is no longer the one exempt from
  the rule it asks of every project. The overlay tag does not carry the
  pins, so `dev pin` says when a `--rebuild` is what picks a new digest up.
- Path as the identity of trust (B2) is worth reopening on a variant B2 did
  not consider. Written up as **B29** rather than left as a bullet: it
  reopens a dropped decision, which deserves the argument in full.

### B29. A marker the tool minted, not one the repository asserts — `todo`

B2 was dropped for a good reason and on an incomplete search. The reason:
binding trust to the git origin reads an identity out of the repository
whose identity is in question, and `git remote set-url` defeats it in one
command. The gap: every candidate considered was a signal *found in* the
repository. There is another kind — a value this tool generates itself.

On acceptance, write a random id into the repository's local git metadata
(`git config --local dev.id <nonce>`, under `--git-common-dir` so worktrees
and submodules resolve to the same place) and record it beside the grants.
Then on each run:

| state | meaning | behaviour |
|---|---|---|
| present, matches | the tree that was accepted | trust applies |
| absent | something else is at this path now | ask again, and say why |
| present, differs | a different accepted tree moved here | ask again |

This survives the objection that killed B2 because nothing is *believed*:
the id is a nonce the tool minted, and the repository is only storing it.
`git clone` does not copy local config, so `rm -rf foo && git clone evil
foo` arrives with no id and inherits nothing — which is the exact payload
B2 set out to stop. It is also quiet, unlike keying on HEAD: the id does
not change when the code does, so it fires when the identity changes and
at no other time.

What it does not do, stated so it is not later mistaken for more:

- It is not a defence against someone who can already write inside the
  accepted tree's `.git`. They are inside the thing that was accepted; B2
  was about a *new* project at an *old* path.
- A whole-directory copy — `cp -a`, rsync, a restored backup, an unpacked
  tarball — carries `.git/config` with it, so the copy inherits. Arguably
  right (same tree, moved) and worth saying rather than discovering.
- A non-git project has nowhere to put a marker that is not also a file in
  the tree. Those keep path-only trust, and the honest thing is to say so
  where trust is explained rather than imply a guarantee that only some
  projects get.
- Re-cloning your own repository at the same path asks again. Correct, and
  cheap: it is one prompt at the moment the tree was replaced.

Ordering: this is a real narrowing but not a hole — **B12** already
removed the dangerous payload (a new project at an old path silently
inheriting `mount_docker_socket: true`), which is why B2 could be dropped
at all. So this is post-1.0 work, and the documentation duty from B2
stands until it lands.

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

- *Close the loop at run end.* — **done.** A run's commits are fetched into
  the project under `refs/dev/clone/<timestamp>`: reachable and safe from
  `gc`, but not a branch, so nothing the user owns moves and nothing is
  checked out. It happens at both ends, because a session killed by a crash
  or an OOM never reaches its own ending — the start-of-run capture is the
  recovery path, and is free because the operation is idempotent. Empty
  captures leave no ref (a ref pins its objects forever), `apply` sweeps the
  namespace and drops the refs once the work is on a branch, and a failed
  capture warns rather than failing a run that worked. `--no-tags`, so a
  tag in a clone cannot reach the user's release tooling.
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

**Fetch at both ends, not only at the end.** As first written, the
automatic fetch happened when a run finished — so a container that is
killed, runs out of memory or hits a bug never fetches, and its work sits
in the clone exactly as it does today. Silently, and precisely when it is
most wanted. The fetch is idempotent and lossless, so it belongs at run
*start* as well: a crashed session's commits are then captured by the next
run without anyone remembering to ask. That makes the mechanism
self-healing rather than dependent on a clean shutdown, which is the right
property for something that runs unattended.

**Recovery is plain git, deliberately.** Because these are ordinary refs,
nothing is trapped in a tool-specific format:

```sh
git for-each-ref refs/dev/clone/          # every run captured, with dates
git log --oneline refs/dev/clone/<id>     # what one run did
git branch recovered refs/dev/clone/<id>  # make it a branch and carry on
```

What the tool still has to add, because things will go wrong in ways this
list does not predict: `dev clone list` showing unapplied runs; `apply`
naming each ref it sweeps; a way to drop refs once landed, since a ref
pins its objects against `gc` forever; and `dev clone diff` learning about
the namespace — after an automatic fetch it would otherwise report
"nothing in the clone the project does not have", which is true of the
clone and false of the situation.

**Not worktrees.** A linked worktree is a window into the parent
repository, not an isolation boundary. Measured:

```
<worktree>/.git            ->  gitdir: <parent>/.git/worktrees/linked
git -C <worktree> branch -a ->  main, secret-branch  (all of them)
git rev-parse --git-common-dir -> <parent>/.git
```

It shares the parent's object store and every ref, so handing one to a
container would give it read and write access to the repository the clone
exists to protect — worse than the hard-linked objects `--no-hardlinks`
already refuses. It would not even function there without mounting the
parent's `.git` as well. And a worktree's `.git` is a `gitdir:` pointer
file, which is the layout B24 now refuses on sight. Worktrees are also the
wrong tool for part 3's dry run, for a quieter reason: sharing the user's
real `.git` means an interrupt leaves rebase state and a moved `ORIG_HEAD`
in their repository. `merge-tree` needs neither.

**Capture refs are keyed by branch — done.** `refs/dev/clone/<branch>/<id>`,
so "what have I not finished on this branch?" is answerable by construction
rather than by reading timestamps. Done now precisely because the feature
is unused: changing the ref layout later would mean migrating refs holding
work, and the argument for doing it at all only appears once someone has
long-lived work to lose track of.

The branch is flattened into one path segment. Git refuses a ref that is
both a file and a directory, so `refs/dev/clone/feat/<id>` and
`refs/dev/clone/feat/foo/<id>` could not coexist — a collision between two
of the user's own branches, and the least acceptable kind.

**Not done: keying the clone directory by branch.** Same idea, different
cost. Existing clones sit at slug-keyed paths, so changing that orphans
them — which is a migration, not a rename — and the fresh-clone cost that
deferred it is still unmeasured. The refs were free to fix; the directories
are not.

**A workflow that argues for branch keying**, recorded because it changes
the cost side of that deferral rather than restating it: incremental work
on a long-lived feature, interleaved with other features. One slug-keyed
clone means that feature's accumulated work shares a directory with
everything else done in that project, and captures from several branches
pile up in one namespace — so "what have I not finished on this branch?"
has no clean answer. Keying the clone by branch, and the capture refs with
it (`refs/dev/clone/<branch>/<timestamp>`), makes that question answerable
by construction. The cost is still a fresh clone per branch, still
unmeasured on a large repository.

**Still open:** keying clones by project *and* branch (deferred until a
fresh clone's cost is measured on a large repository); separating the
human and agent clone keys, which share a slug today.
