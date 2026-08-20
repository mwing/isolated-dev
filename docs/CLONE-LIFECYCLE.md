# Clone lifecycle: making a run feel like a fresh clone (B23)

Proposal for closing `docs/BACKLOG.md` B23. The goal there is stated as a
feeling: every run should behave as though a clone were made for it and the
work merged back afterwards — no stale clone to discover, no branch to
reconcile, nothing to reason about between runs. The mechanism was left
open. This is the proposed mechanism, after review.

The first draft of this document reasoned only about losing work. Three
reviews — security, engineering, UX — found that insufficient in ways that
changed the design rather than the wording. The findings are recorded here
next to what they forced, because several of them are live defects in code
that already ships, not just objections to the plan.

## Two lines, not one

**First line: automate only operations that can neither conflict nor lose
work.** That subset is larger than it looks:

- `git fetch` from the clone into the project — adds objects, moves only
  refs the tool owns.
- `--ff-only` merge — already sanctioned; `dev clone apply` does it today.
- Deleting a clone that provably holds nothing unique.

What stays out: merge, rebase, cherry-pick — anything that can conflict.
Also anything that combines histories *cleanly*, because a conflict-free
rebase is still the judgement the founding rule forbids: a human renames a
function, an agent adds a call site, and the result has no textual conflict
and does not build. The tool can still *compute the facts of* that
judgement without *making* it — see part 3.

**Second line, which the first draft missed entirely: the clone's `.git`
is untrusted input.** An agent has unsupervised write access to it, and
every part of this mechanism runs host-side git against it — parts 1 and 2
unattended, on every run, where today the equivalent commands are at least
user-initiated. Changing the trigger from "the user asked" to "every run"
is what turns the following from a latent problem into an exposed one.
Measured during review on git 2.47.3, in the clone's own config:

```
core.fsmonitor = ./pwn.sh   +  git status --porcelain   ->  PAYLOAD-RAN
same, with -c core.fsmonitor=                           ->  (did not run)
```

`State()` runs `status --porcelain` (`internal/clone/clone.go:444`) and
`driftNotes` runs it again (clone.go:313); `dev clone diff` runs `diff
HEAD` (`internal/cli/cloneapply.go:63`). All on the host, as the host
user, with the user's SSH keys in reach — which is what `--clone` exists
to protect. `filter.*.clean`, `diff.*.textconv`, `core.hooksPath`,
`core.pager` and `alias.*` are the same class, and `[include] path =
~/.gitconfig` pulls back in exactly the settings `gitConfigCarried`
(`internal/cli/hostaccess.go:165`) filters on the way *into* the
container. That filter reads as though the problem were solved. It is
solved in one direction only.

The repository already has the vocabulary for this — "settings that run
programs" — and applies it container-inward. This proposal applies it
host-inward.

## Prerequisite: one hardened way to run git against a clone

Nothing in parts 1–3 may ship before this, and it is worth doing on its
own regardless, because the exposure above exists today.

A single helper is the only way host code runs git against a clone.
Mirroring the `runner` package's own argument, two implementations of
"read the clone safely" would be two chances for one to check less.

It does five things:

1. **Quarantines the clone's config.** Before any host read, rename
   `.git/config` aside and write a minimal tool-authored replacement
   (`repositoryformatversion`, `filemode`, `bare`, `logallrefupdates`).
   Measured: with that config, `status`, `diff --stat` and `log` behave
   normally and every payload above is inert. This is the only robust fix
   for the `.gitattributes` + `filter.*` class, where the driver name is
   attacker-chosen so there is no finite set of keys to blank. Ignore
   `.git/hooks`, `.git/info/attributes` and `.git/objects/info/alternates`
   the same way.
2. **Adds the hardened flags anyway**, as defence in depth:
   `-c core.fsmonitor= -c core.hooksPath= -c core.pager=cat
   -c protocol.file.allow=never`, and `GIT_NO_REPLACE_OBJECTS=1`.
   Implementation trap worth writing down: `runner.Command.Env` *replaces*
   the environment, so a bare `Env: []string{...}` drops `PATH` and `HOME`
   and breaks git in a way that will look like a git bug. Build it with
   `append(os.Environ(), …)`.
3. **Refuses clone-derived arguments.** Ref names may begin with `-`.
   Measured: a branch named `--upload-pack=./pwn.sh` is accepted by git,
   returned verbatim by `for-each-ref`, and executes when passed as a
   refspec. Today's code is safe by luck — `cloneapply.go:123` passes a
   literal `HEAD:<branch>`. Rule: no string read out of a clone is ever
   passed as a git argument; refspecs are tool-composed, positionals are
   preceded by `--`, and any ref name that must be *printed* is validated
   against `^[A-Za-z0-9][A-Za-z0-9._/-]*$` first.
4. **Sanitizes output.** Ref names cannot carry control characters and
   paths are quoted by `core.quotePath`, so the exposure is narrower than
   feared — but commit subjects and author names are not sanitized by git,
   and a subject containing `ESC[2K ESC[A` erases the line above it,
   including the tool's own warning. Strip C0/C1 except tab and newline,
   cap line length, print single tokens through `%q` as `driftNotes`
   already does (clone.go:277).
5. **Reports anomalies instead of normalizing them.** A clone containing
   `refs/replace/*`, `.git/info/grafts`, a `.git/shallow` the tool did not
   write, or a `.git` that is not a plain directory is not a detail to
   work around; it is a finding to print and stop on.

That last one earns its own paragraph, because it defeats the whole
"make the review fast and obvious" thesis if left out. Measured:

```
agent commits a backdoor, commits a decoy, git replace -f backdoor decoy

host review     (git show HEAD:f)                 ->  benign
what is there   (--no-replace-objects show)       ->  BACKDOOR
what fetch delivered into another repo            ->  BACKDOOR
```

The hostile commit's subject vanished from `git log` entirely. So a review
can be accurate about a history that is not the one being imported. Parts
2 and 3 therefore report from the **project, after the fetch**, on the
object names actually received — not from the clone, before it.

## The mechanism, in three parts

### 1. Refresh-when-empty, at run start

When `Prepare` finds an existing clone that provably holds nothing unique,
it is refreshed to match the project. Otherwise the run stops and presents
the decision.

This delivers the fresh-clone feeling for every run that follows a
completed round trip, which is the common case, and removes cross-branch
reuse of empty clones by construction — the afternoon lost to a clone 64
commits behind on the wrong branch cannot recur through an empty clone.

Four things the first draft got wrong here, all of them load-bearing:

**Emptiness must be proved, not read off the clone.** `State()`
(clone.go:442) asks `git log --branches --not --remotes` inside the
attacker's repository, and swallows every error into a zero. Measured, all
one-liners inside the clone:

```
git update-ref refs/remotes/fake/main HEAD   ->  candidates 3 -> 1
git stash                                    ->  dirty 0, stash entries 1
git update-index --assume-unchanged f        ->  dirty 0
append "[core" to .git/config                ->  every git command exits 128,
                                                 State() returns (0,0,"",false)
```

Each of those reads as "empty" and would be deleted. The last is not even
an attack — any corrupted clone reads as nothing-to-lose. So:

- Containment is verified **from the project side**: enumerate the clone's
  tips, then ask the project whether it contains each (`merge-base
  --is-ancestor`). The clone's own `refs/remotes/*` layout is
  attacker-authored and is not evidence of anything.
- The project directory is passed in by the caller, never read from the
  clone's `remote.origin.url` as `State()` does today (clone.go:463) —
  that both points host git at an attacker-chosen directory and forges
  containment by aiming origin at a repo that has the commits. B2 already
  named this shape: reading an identity out of the repository whose
  identity is in question.
- Tips means more than branches: `rev-list --all --not --remotes` covers
  heads, tags and notes; detached `HEAD` needs its own containment check;
  and a non-empty `git stash list` counts as holding work. Reflog-only
  commits (commit-then-reset inside the clone) are explicitly out of
  scope — that is what git itself collects — so the boundary is deliberate
  rather than accidental.
- **`State()` grows an unmeasurable state distinct from empty**, and
  refresh refuses on it. Fail-closed: anything the tool could not measure
  becomes the human decision point the design already has a UI for.

**No clone is deleted while a container has it mounted.** There is no run
registry, no lock file, no flock anywhere in the tool today. So: terminal
A is running an agent with the clone bind-mounted; terminal B starts a run
of the same project; `clone.Dir` is keyed by slug alone (clone.go:418), B
refreshes, and A's container keeps writing into an orphaned inode that
vanishes when it exits. Total silent loss, of exactly the work this tool
exists to protect. Required before part 1 ships: an active-run check
(label the container with the clone path at spec time and filter on it —
`dev.role` exists at `internal/agent/run.go:172`, `dev.project` is not set
on agent containers) and a per-clone advisory lock so two `Prepare`s
cannot both decide to delete.

This also settles the refresh mechanism. The first draft called re-clone
and `fetch && reset --hard` observably identical; they are not. Reset
preserves ignored files — dependencies and build output the clone built
inside itself, which a re-clone destroys and the next run pays minutes to
rebuild (clone.go:252 explains why they are never copied in) — and it
preserves the clone's local git identity, its reflog, and the directory
inode, so a still-mounted container sees a surprising reset instead of an
orphan. Reset is the better default; re-clone is the fallback when reset
cannot get there.

**Carried-in dirt is not work.** `carryUncommitted` (clone.go:195) copies
the project's uncommitted diff and untracked files into every fresh clone.
That dirt is in the clone from its first second, so `State()` reports
`dirty > 0` and the stop fires — on the tool's own artifact, for anyone
with a work-in-progress tree, which is most people most days. Worse, the
stop is then a trap: `apply` explicitly does not move uncommitted changes
(cloneapply.go:111) and prints "Nothing new", so the only listed exit is
`dev clone rm --force`. A guard whose routine answer is `--force` is the
failure `State()`'s own comment warns about, and the one B11 names: a
warning you cannot make go away stops being read.

So the carried-in state is fingerprinted when it is carried (a tree hash
recorded under a `refs/dev/` ref the container cannot reach). Dirt that
matches is not work, and the clone refreshes — the same changes are
re-carried from the project in their current form anyway. Dirt that
differs means the agent touched those files, and still stops. And the stop
always offers a lossless exit for a dirty clone: commit it there, then
apply.

**What the stop looks like**, leading with what happened to the run:

```
This run has stopped: the project's clone holds work the project does not.
  2 commit(s), on "old-branch" — this project is on "main", so applying
  them here will not fast-forward.

  dev clone diff          read it
  dev clone apply         bring the commits back, then re-run
  dev clone rm --force    discard it and start fresh

  Or decide in the command: --apply-first, --discard-clone, --stale-ok
```

`rm --force` is the right command rather than `prune`, which refuses
clones holding work by design and would be a dead end here. Advertising
`--force` is only acceptable *because* the fingerprint fix makes the stop
rare and the work named real.

**Headless behaviour is part of the design, not an afterthought.** Today a
scripted run proceeds through a stale clone with drift notes. Stopping is
a behaviour change, and per B12 a refusal must name the per-run flag that
resolves it — otherwise a CI job that once leaves work in the clone fails
every subsequent run until a human logs in. `--apply-first` (fetch and
ff-only, fail if not ff — still lossless), `--discard-clone`, and
`--stale-ok` (today's behaviour, for whoever depends on it). The stop
prints a menu and exits non-zero; it never holds a prompt with nobody
present, per the terminal-layer lesson in B9.

### 2. Close the loop at run end

When a run ends and the clone has new commits, they are fetched into the
project automatically — the lossless half of today's `apply` — and the
report says what came out and what finishes the job.

The first draft said "fetch `HEAD:clone-work --force`", reusing today's
refspec. Two reviews found the same hole independently, and it is the
sharpest thing they found: composed with part 1, two lossless steps make a
lossy loop. Run 1's commits land on `clone-work`; the user does not apply;
run 2 starts, and the clone now counts as empty *because its commits are
on `clone-work`*, so it is refreshed; run 2 ends and force-moves
`clone-work` to a history that does not contain run 1's work, which is now
reflog-only. Two runs before one apply is an ordinary sequence. A `--force`
ref update is not lossless once the ref holds unapplied commits.

So automatic fetches go to a tool-owned namespace — `refs/dev/clone/<run>`
— which the user cannot own and the tool may safely manage. A
user-visible `clone-work` is only created or fast-forwarded, never
force-moved, without an explicit user action. `apply` sweeps the namespace
and reports what it found. This also closes the case where real user work
on a branch that happens to be called `clone-work` is silently clobbered,
and the case where the fetch fails outright because `clone-work` is
checked out in the main tree or any linked worktree (git refuses to update
a checked-out branch, and checks every worktree).

Three more constraints on the automatic fetch:

- `--no-tags` and an explicit refspec. Measured: an explicit refspec does
  **not** stop tag following — a hostile `refs/tags/v9.9.9` transferred
  into the destination — and tags feed `git describe`, version derivation
  and release tooling. (`refs/replace/*` did not transfer, so it is a
  concern inside the clone only.)
- Object volume is measured before importing and capped, with a refusal
  that says the size rather than a silent import. "Adds objects, touches
  nothing checked out" understates this: imported objects stay reachable,
  survive in reflogs for ninety days, and are paid for by every later
  `gc`, `repack` and clone of the user's own project.
- Every ref created or moved is named, old → new.

The summary itself, computed project-side after the fetch, capped in the
B11 shape — the biggest few files, then a total, because 300 stat lines
put the one actionable line off the screen:

```
The agent made 3 commit(s), fetched into this project:
  internal/netpolicy/rules.go       | 41 ++++++---
  internal/netpolicy/rules_test.go  | 88 ++++++++++++++
  …and 4 more files: 210 insertions(+), 33 deletions(-)

Bring them onto your branch:  dev clone apply
```

Stated, not asked. An end-of-run "apply now?" adds a decision to every
successful run — the opposite of fewer decisions on the secure path — and
hangs any wrapper script.

### 3. Report whether the combine would conflict

The non-fast-forward case is where the feeling currently dies: `apply`
prints four git commands. B23 rejected automatic rebase because a
conflicting one stops half-done in a state nobody asked for. So compute
the outcome without performing it.

The first draft proposed a scratch `git worktree` in the user's project.
Both technical reviews rejected that, and they are right: a worktree
shares the user's real `.git`, so an interrupt — likely, since a person is
watching — leaves rebase state, a stale registration and a moved
`ORIG_HEAD` in their repository. That is B23's rejected outcome arriving
through a side door. It also writes a full checkout (the cost this
document defers branch-keying over), runs the project's `post-checkout`
hook as a side effect of a dry run, and materializes attacker-controlled
trees under a directory the user's IDE and dev server are watching.

Use `git merge-tree --write-tree` instead: it merges in memory, needs no
checkout and no cleanup, and lists the conflicted paths — which is exactly
the fact worth reporting. Probe for it (git ≥ 2.38) and iterate it over
the user's commits for rebase-shaped fidelity. If it is unavailable, the
fallback is a throwaway clone outside the project, never a worktree of it;
either way the dry run touches no ref in the user's repository and cleans
up on signal as well as on return.

The base commit this needs is the clone's `origin/<branch>`. The first
draft promoted that to a contract — "nothing inside the clone may move
it". That contract is unenforceable as written: verified that the agent
cannot *fetch* it (the container mounts only the clone and a home volume,
so origin's host path is unreachable — a load-bearing fact worth
recording), but nothing stops `git update-ref refs/remotes/origin/main
<sha>`. The same weakness makes today's "behind by N commits" note
(clone.go:298) untrustworthy. So the base is recorded host-side where the
container cannot reach it, and validated before use: it must exist in the
project and be an ancestor of the clone's HEAD, or the tool falls back to
a merge-base computed in the project and says the recorded base was not
trustworthy.

The wording matters as much as the mechanism. "Completes without conflict:
`git rebase clone-work`" is the tool saying *safe, run this* — the one
judgement this whole document refuses to make:

```
Your branch and the clone have both moved.
Replaying your 2 commit(s) onto the clone's work hits no textual conflict:
  git rebase clone-work
Git checked for overlap, not for meaning — build and review afterwards.
```

And when it does conflict, report the operation actually tested and do not
recommend an untested one. The first draft reported a rebase conflict and
then suggested `git merge`, which will conflict in much the same files —
sending the user into the half-done state the feature exists to spare
them, now believing the tool had vetted it:

```
Replaying your 2 commit(s) onto the clone's work conflicts in
package-lock.json and internal/api.go — the clone changed them too.
Any combine will ask you to resolve those files. Yours to make:
  git log --oneline HEAD..clone-work   # what it did
  git merge clone-work                 # keeps both histories
```

The `log` line survives in both branches of the message: it is the review
step, and dropping it would trade "here is the decision" for "here is a
command".

## What is deliberately not done

- **No automatic merge, rebase or cherry-pick**, even when it would
  complete cleanly. A clean textual combine is still a semantic judgement
  about code this tool has not read.
- **Uncommitted work in the clone stays where it is.** Committing is the
  act that says "this is work". The refresh guard never deletes
  agent-made dirt, and `apply` continues to say it is not coming.
- **Keying clones by project and branch** is deferred, not refused. With
  refresh-when-empty in place it only matters when the old branch's clone
  still holds work, which part 1 turns into an explicit decision anyway.
  Decide after measuring what a fresh clone of the largest relevant
  repository costs; these are local, no-network clones and
  `--clone-depth` shortens them further.
- **Separating the human (`dev run --clone`) and agent clone keys** is
  also deferred, and part 1 raises its cost: both point at the same
  slug-keyed directory (`internal/cli/clone.go:170`), so a human's
  exploration left uncommitted there is attributed to the agent's clone
  and now produces a hard stop rather than a note.
- **Detecting secrets, or judging the agent's code.** Unchanged from B22.

## The honest limitation

The project branch moving *while* the agent works cannot be prevented by
any keying or refresh policy — the clone is accurate when created and the
human commits afterwards. It can only be landed well, which is what parts
2 and 3 do: the divergence is detected at the boundary and the residual
decision arrives with its facts precomputed and its uncertainty stated.
That closes the question B23 left open: the feeling is produced by
lossless automation at both ends and verified facts in the middle, never
by an automatic combine.

What the reviews add to that answer is a second condition. Lossless is
necessary and not sufficient: an operation must also be *safe to perform
against a repository an adversary has written to*, and the tool must be
able to prove it measured what it thinks it measured. Refresh deletes only
on positive project-side proof; everything unmeasurable becomes the human
decision the design already has a place for.

## Order of work

1. The hardened git helper and config quarantine, plus the `Lstat` and
   error-propagation fixes in `State()` and `Remove`. Fixes live exposure;
   blocks everything else.
2. Part 2, minus nothing — it is `applyClone`'s first half relocated, once
   the ref namespace is tool-owned.
3. Part 1, which needs the active-run guard, the per-clone lock, the
   project-side containment proof and the carry-in fingerprint. The
   apparent size of this part roughly doubles once those land; it is the
   part with the data-loss risk and should not be rushed to match the
   others.
4. Part 3, on `merge-tree`, with the host-side recorded base.

## Tests this needs

The first three fail against today's code, which is the point.

1. `State()` on a clone holding only a stash, only detached-HEAD commits,
   only a tag-reachable commit, or a corrupt config — must report
   not-empty or unmeasurable, never empty.
2. `State()` against a clone whose `refs/remotes/*` or
   `remote.origin.url` has been forged — containment still proved from
   the project.
3. Host-side git against a clone carrying `core.fsmonitor`,
   `filter.*.clean`, `.gitattributes` + `diff.*.textconv`,
   `refs/replace/*`, a `--upload-pack=…` branch name, and an escape
   sequence in a commit subject — no execution, no substituted history,
   no argument injection, no terminal corruption.
4. Dirty project → run → second run does not stop; agent-modified clone →
   second run stops.
5. Refresh refused while a container has the clone mounted; two
   concurrent `Prepare`s serialized by the lock.
6. Two runs before one apply — both runs' commits still ref-reachable.
   Fetch with `clone-work` checked out in the main tree and in a linked
   worktree. Shallow clone as fetch source: the commits land and the
   project does not become shallow.
7. Part 3's prediction checked against an actual rebase performed in a
   copy; interrupt mid-dry-run leaves `git worktree list` and the
   project's refs clean; recorded base moved or absent falls back with a
   note.
8. Regression: `dev clone list` / `rm` guard behaviour under the new
   `State()` semantics (`internal/cli/clonecmd.go:219` shares it).

## Documentation this changes

Refresh-when-empty makes `dev clone prune` a disk-reclamation tool for
projects you stopped running, rather than a step in the round trip.
`apply`'s closing line (cloneapply.go:162), `docs/CONCEPTS.md:237` and
`docs/GETTING-STARTED.md:250` all currently teach it as the latter.

`dev clone diff` also needs to learn about the tool-owned refs: after an
automatic fetch it would otherwise answer "nothing in the clone the
project does not have" — true of the clone, and false of the situation,
while unapplied work sits in `refs/dev/clone/…`. The documented review
step has to keep answering the question it is documented as answering.
