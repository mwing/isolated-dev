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
target before relaying bytes.

Two consequences worth knowing:

- **TLS is never terminated.** The proxy matches on hostname and then
  relays untouched, so certificate pinning keeps working and the proxy
  never holds your plaintext. The cost, accepted deliberately: filtering
  is per-host, not per-path.
- **A process that ignores `HTTP_PROXY` gets nowhere.** The variables are
  a convenience for well-behaved clients, never the boundary.

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
dev accept          # settings it requests
dev agent accept    # egress destinations it requests
```

Grants live outside the repository on purpose: configuration inside a
repository is configuration the repository can grant itself, so a hostile
project would widen its own access before anyone read it.

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

## 5. A run does not have to touch your working tree

```sh
dev agent run claude --clone
```

mounts a private copy of the repository instead of the directory you are
editing. Your uncommitted work and untracked files are carried in, so the
run starts from what you see; anything it does afterwards — including
`rm -rf` — lands in the clone.

The work is not thrown away, it is moved:

```sh
git -C "$(dev clone path)" status     # review with your own tools
git fetch "$(dev clone path)" HEAD    # bring it back
```

Use it when the run is unattended, when the thing running is not
trustworthy, or when you want a change to arrive as a diff you approve
rather than as edits already made. For your own interactive work the plain
mount is better — the edits just appear.

---

## 6. What happened is recorded

Every filtered run is written down, because the summary that scrolls past
is the wrong lifetime for the question:

```sh
dev history          # what each run reached, and what was blocked
dev history hosts    # every destination, most recent first
dev agent grants     # each grant, and whether anything still uses it
```

An allowlist that only ever grows stops meaning anything. `dev agent
grants prune` offers back the entries no recorded run has reached — and
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
  alongside it is not what a project run gives you.
- **Ports do not publish in allowlist mode** unless the sidecar forwards
  them; the run says which are published.
- **A container is not a VM.** On macOS everything already runs inside the
  OrbStack VM, so a container escape reaches that VM rather than your Mac —
  but containers there share a kernel with each other.
- **Builds are not filtered.** Egress control governs a running container;
  what a build fetches is governed by pinning. See ROADMAP 4.3.1.

---

Next: [GETTING-STARTED.md](GETTING-STARTED.md) to adopt this in a
repository you already have, or [USE-CASES.md](USE-CASES.md) for the jobs
worked through end to end.
