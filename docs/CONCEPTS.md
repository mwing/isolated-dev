# How it works

Six ideas. Once these land, the commands are guessable.

---

## 1. A run is your directory, and nothing else

```sh
dev run -c 'npm test'
```

builds an image for the project, starts a container, mounts the current
directory at `/workspace`, and runs the command as an unprivileged fixed
uid.

What is **not** in that container: your SSH keys, your `~/.gitconfig`,
your shell environment, the docker socket, any other directory on your
machine. Not because they were filtered out — because nothing puts them
there. A project `Dockerfile` that declares `USER root` does not get root
either; the tool sets the uid on every run.

This is the whole difference from `docker run` by hand. You are not
remembering to leave things out; you are adding them in when you decide
to.

---

## 2. Egress is closed by default, and closed by topology

A container reaches its language's package registries and nothing else
until you say otherwise.

The enforcement is not an environment variable a program can ignore:

```
   workload                sidecar                  internet
┌──────────────┐       ┌──────────────┐
│              │       │              │──────────────▶ allowed
│  no gateway  │──────▶│  proxy + DNS │
│              │       │              │─────x────────  everything else
└──────────────┘       └──────────────┘
   internal network        also on the egress network
```

The workload sits on a network with **no route out at all**. The sidecar
is dual-homed and is the only path. Its DNS resolver answers for
allowlisted names and refuses the rest, and its proxy checks the CONNECT
target — and the name inside the TLS session, which on a shared front is
what actually decides who answers — before relaying bytes.

Two consequences worth knowing:

- **TLS is never terminated.** The proxy matches on hostname and then
  relays untouched, so certificate pinning keeps working and the proxy
  never holds your plaintext. The cost, accepted deliberately: filtering
  is per-host, not per-path. Reading the hostname out of the opening
  handshake is not terminating it — the record is inspected on its way
  past, and the session it opens is between your client and the far end.
- **A process that ignores `HTTP_PROXY` gets nowhere.** The variables are
  a convenience for well-behaved clients, never the boundary.

The second one cuts both ways, and it is the part that surprises people:
**a grant permits a destination, it does not build a road to it.** The
proxy is the only road, and the client has to know how to use it.

There are two doors, and both enforce the same allowlist:

| | |
|---|---|
| `HTTP_PROXY`/`HTTPS_PROXY` | an HTTP CONNECT proxy — curl, wget, git over https, pip, npm, cargo, go |
| `ALL_PROXY` | a SOCKS5 endpoint (`socks5h://`), for everything that is not HTTP |
| ssh | `ProxyCommand` through either; `dev agent run --allow-push` wires it |

Both are set in the container. A client that reads `ALL_PROXY` — or that
takes `--socks5-hostname`, or is wrapped in something like `proxychains` —
reaches a granted destination on any port. One that reads neither and opens
a socket itself still gets `network is unreachable`, because what stops it
is the missing route rather than the policy. Granting a non-HTTP port says
which case you are in at the time, rather than leaving it to be discovered.

SOCKS is a second door, not a looser one. It is the same allowlist, the
same held-request prompt, the same refusal of literal addresses under a
hostname rule, the same check on the name inside the TLS session, and the
same lines in `dev history` — one function decides, and both front ends
call it.

Three modes: `allowlist` (default), `open` (no filtering), `none`
(`--offline`, no network at all).

**A blocked destination can be a question.** With `--egress-prompt ask`, or
in `dev console`, the request is *held* while you decide: allow once, allow
for this project from now on, or refuse. That is a firewall prompt, not a
report of something that already failed.

---

## 3. What a project asks for is not what it gets

Two files, and the split is the point:

| file | what it is | committed? |
|---|---|---|
| `<repo>/.devenv.yaml` | what the project **requests** | yes — shared with the team |
| `~/.dev-envs/projects/<slug>-<hash>.yaml` | what you **accepted**, plus your own grants | no — yours |

Cloning a repository and running it is not consent. Anything the project
asks for that you have not accepted stops the run and is shown to you
first:

```sh
dev accept          # everything it requests: settings and destinations
```

Those stay two decisions — accepting a setting grants no destination, and
each is checked against its own policy rules — but they are reviewed
together, because the project asked for both in one file.

Grants live outside the repository on purpose: configuration inside a
repository is configuration the repository can grant itself, so a hostile
project would widen its own access before anyone read it.

One request is not remembered at all. Mounting the docker socket is root on
the docker host, so it is granted for a single run:

```sh
dev run --allow-docker-socket          # this run, nothing written down
dev accept mount_docker_socket --remember   # if you really mean always
```

An acceptance is keyed by the project's *path*, so anything remembered is
inherited by whatever occupies that path later — and a new repository
asking for the same value looks identical to the one you approved. For most
settings that is a fair trade. For root on the host it is not.
[USE-CASES.md](USE-CASES.md#tests-that-need-docker-and-what-that-costs)
works through when it is worth it, and what it costs.

The one thing that constrains **you** is `~/.dev-envs/policy.yaml`, which
can forbid network modes, settings, registries and destinations outright.
It is not a defense against the machine's owner — who can edit it — but it
closes the unsafe paths for people who are not attacking their own laptop.

---

## 4. Everything is a declaration, rebuilt

Tools are recorded and the image is rebuilt from that record:

```sh
dev tools add ripgrep            # for you
dev tools add --shared ripgrep   # for the team, via .devenv.yaml
```

Not `docker commit`. A mutated container is an environment that exists
only on the machine where the command was typed; a declaration is one a
teammate can reproduce.

The same idea governs base images:

```sh
dev pin        # resolve every FROM to the digest it means today
dev update     # move those digests and the packages forward, and report what moved
dev scan       # what is vulnerable in the image you actually run
```

Pinning and updating are the same trade from opposite ends. A tag says
which image you *meant*; a digest says which image you *got*. Pinning
stops the ground moving under you — and also stops security patches
arriving silently, which is why `update` exists to move it on purpose and
tell you what moved.

---

## 5. An agent does not touch your working tree

```sh
dev agent run claude
```

mounts a private copy of the repository instead of the directory you are
editing — no flag, that is the default. Your uncommitted work and untracked
files are carried in, so the run starts from what you see; anything it does
afterwards, including `rm -rf`, lands in the clone.

Why agents and not you: an agent cannot reach your SSH keys, but it can
edit the things your *host* runs later — git hooks, npm scripts, Makefiles,
CI files. It also acts on instructions from a model rather than from the
person in the room.

The agent runs on **your project's image**, with the agent added on top, so
it has the toolchain of the thing it is working on — an agent that cannot
run the project's tests cannot check its own work. `base:` in `.devenv.yaml`
overrides that, and an agent's own base image is the fallback for a
directory with no project to build. The clone also carries a git identity,
copied from the project, because a container has no `~/.gitconfig` and work
that cannot be committed cannot come back.

The work is not thrown away, it is moved, and comes back in two commands:

```sh
dev clone diff     # what it did, read with your own tools
dev clone apply    # bring it back
```

**The work comes back on its own.** When a run ends — and again when the
next one starts, in case the last was killed — the clone's commits are
fetched into your project:

```
The clone has 2 commit(s) the project did not, now fetched into it.
  git log --oneline refs/dev/clone/feat-parser/20260820-141233
  dev clone apply    bring them onto your branch
```

Nothing you own moves. No branch changes, nothing is checked out, your
working tree is untouched. The commits are simply *in your repository*
rather than only in the clone — so a crashed session, a killed container or
a clone you forgot about stops being a way to lose work.

They are refs rather than branches, filed under the branch the work came
from. That means they are invisible to `git branch`, impossible to check
out by accident, never pushed to anyone, and safe from `git gc`. It also
means recovery is ordinary git, with nothing held in a format only this
tool understands:

```sh
git for-each-ref refs/dev/clone/            # every run captured
git log --oneline refs/dev/clone/<branch>/<run>
git branch recovered refs/dev/clone/<branch>/<run>
```

A run tells you when captures are waiting on your branch, and `dev clone
apply` brings them in and releases the refs.

`apply` fast-forwards when that needs no decision. Where your branch has
moved too, it fetches to a branch and shows you the merge, rebase and diff
options rather than choosing — an automatic merge is a judgement about code
the tool has not read.

`--in-place` opts out for one run. If you mostly drive agents
interactively, decide it once instead:

```yaml
# ~/.dev-envs/config.yaml
agent_clone: false
```

Your own config needs no consent — it is your machine. The same line in a
project's `.devenv.yaml` is a *request*, and stops the run until you accept
it, because a repository asking to turn the clone off is asking to edit the
files you are editing. Asking for `agent_clone: true` needs no acceptance:
it is the default already, and a prompt that grants nothing teaches people
to click through the ones that matter.

`dev console --agent` clones too, and by the same rule: what makes the
clone right is *who is driving* — a model rather than the person in the
room — not which view you happen to be watching through.

`dev run` and `dev shell` are unchanged: a human editing their own tree is
the case the plain mount is right for, and `--clone` is there when you want
it anyway. A `dev console` with no agent is that same case.

Clones are full copies, so they cost disk:

```sh
dev clone list     # every clone, its size, and what work is still in it
dev clone prune    # remove the ones holding nothing
```

`prune` keeps anything holding commits the project lacks or uncommitted
changes, and anything touched in the last week. Past 5 GiB the total is
printed when a clone is made, rather than discovered later.

---

## 6. What happened is recorded

Every filtered run is written down, because the summary that scrolls past
is the wrong lifetime for the question:

```sh
dev history          # what each run reached, and what was blocked
dev history hosts    # every destination, most recent first
dev grants           # each grant, and whether anything still uses it
```

An allowlist that only ever grows stops meaning anything. `dev grants
prune` offers back the entries no recorded run has reached — and
refuses to judge a thin history, because "never used" across three runs is
not a finding.

Records live under `~/.dev-envs` at mode 0600, never in the repository:
what your machine reached is not the repository's business.

---

## Where things live

```
~/.dev-envs/config.yaml                  global settings
~/.dev-envs/policy.yaml                  rules that bind you, not just projects
~/.dev-envs/languages/<lang>/            language plugins
~/.dev-envs/agents.yaml                  agent defaults
~/.dev-envs/projects/<slug>-<hash>.yaml  grants, tools, acceptances
~/.dev-envs/projects/<slug>-<hash>.history.jsonl   what runs reached
~/.dev-envs/clones/<slug>/               private clones
<project>/.devenv.yaml                   the project's requests, committed
```

---

## What it does not do

- **It runs one container, not your stack.** A repo needing a database
  alongside it is not what a project run gives you. Tests that start their
  own containers need the docker socket, which ends the isolation —
  [worked through here](USE-CASES.md#tests-that-need-docker-and-what-that-costs).
- **Ports do not publish in allowlist mode** unless the sidecar forwards
  them; the run says which are published.
- **A container is not a VM, and what an escape reaches depends on where
  you are.** On macOS everything already runs inside the OrbStack VM, so an
  escape reaches that VM rather than your Mac — though containers there
  share a kernel with each other. On Linux, with a local daemon, there is
  no VM in the way: an escape reaches the host. That is a real difference
  in what this tool is worth on the two platforms, and it is not one the
  sandbox can close.
- **Builds are not filtered.** Egress control governs a running container;
  what a build fetches is governed by pinning. See ROADMAP 4.3.1. Because
  of that, a repository's own `Dockerfile` is not built until you accept it
  once — `dev accept build_source`, or `--build-source template` to build a
  stock image for the language and ignore the file entirely.

---

Next: [GETTING-STARTED.md](GETTING-STARTED.md) to adopt this in a
repository you already have, or [USE-CASES.md](USE-CASES.md) for the jobs
worked through end to end.
