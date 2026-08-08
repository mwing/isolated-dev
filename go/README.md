# dev2

Run code in a container without learning Docker, with a sandbox that is
closed by default and opened one deliberate step at a time.

This is the Go rewrite of isolated-dev. It ships as `dev2` while the bash
`dev` still exists, and takes the `dev` name when v1 retires. See
[docs/ROADMAP.md](../docs/ROADMAP.md) for where it is going and
[docs/PARITY.md](../docs/PARITY.md) for what changed from v1 and why.

**New here? Start with [USAGE.md](USAGE.md)** — it works through the
common jobs: updating dependencies, running software you do not trust,
sandboxing an agent, and running a server with no network at all.

## Install

```sh
cd go
make install        # builds dev2 into ~/.local/bin
make proxy-image    # builds the egress sidecar image into the OrbStack VM
dev2 doctor         # checks everything above
```

Requires Go 1.26+ and OrbStack. `doctor` never changes anything — it
reports what it can see and what would block a run.

## Two minutes

```sh
cd ~/code/my-project

dev2 run -c 'go test ./...'   # detects the language, builds, runs
dev2 shell                    # a shell in the same container
dev2 status                   # what is running, under which policy
dev2 clean                    # tear it down
```

The first run detects the project's language, renders that language's
Dockerfile template, and builds an image. A project with its own
`Dockerfile` uses it instead.

## What is different from running docker yourself

**Nothing sensitive is in the container unless you put it there.** No SSH
keys, no `~/.gitconfig`, no docker socket, no host environment variables.
The container runs as an unprivileged fixed uid that the tool sets — a
project Dockerfile declaring `USER root` does not get root.

**Egress is filtered by default.** A container reaches its language's
package registries and nothing else until you say otherwise. The workload
sits on a network with no route out; a proxy sidecar is the only path, and
it allows destinations by hostname without terminating TLS.

**A blocked destination can be a question rather than a failure.** In the
console, or with `--egress-prompt ask`, the request is *held* while you
decide: allow once, allow for this project from now on, or refuse.

**What the project asks for is not what it gets.** A `.devenv.yaml` in the
repository is a *request*. Running the project is not consent — anything
it asks for that you have not accepted stops the run and is shown to you
first.

## Commands

| | |
|---|---|
| `dev2 run [-c CMD]` | run a command in the project's container |
| `dev2 run --image IMG` | run a stock image instead, for things that are not projects |
| `dev2 shell` | interactive shell |
| `dev2 build` | build the project image |
| `dev2 console` | full-screen view: workload, egress events, live prompts |
| `dev2 status` | what is running, what is allowed, what was blocked |
| `dev2 clean` | remove containers, networks and the sidecar |
| `dev2 tools search TERM` | find what the image's package index has |
| `dev2 tools add TOOL` | add a tool to this project's environment, permanently |
| `dev2 tools add --shared TOOL` | add it to `.devenv.yaml` so the team gets it too |
| `dev2 tools list` / `remove` | list and remove those tools |
| `dev2 pin` | pin base images to digests, so builds are reproducible |
| `dev2 accept` | review what `.devenv.yaml` requests |
| `dev2 agent run NAME` | run a coding agent in the sandbox |
| `dev2 agent allow HOST` | grant an egress destination for this project |
| `dev2 doctor` | diagnose the setup |
| `dev2 migrate` | what changes for an existing v1 user |
| `dev2 vm` | start / inspect the container VM |

Every command takes `--verbose`, which prints the exact `docker`
invocation before running it. A security tool should never make you guess
what it ran.

## Network modes

| mode | what it means |
|---|---|
| `allowlist` (default) | language registries plus what you granted; everything else denied |
| `open` | no filtering — v1 behavior |
| `none` | no network at all (`--offline`) |

Set per project in `.devenv.yaml` (`network: open`), or per run
(`--network open`). A project asking for `open` needs your acceptance,
because it switches the filtering off.

## Where things are kept

```
~/.dev-envs/config.yaml                  global settings
~/.dev-envs/languages/<lang>/            language plugins (same format as v1)
~/.dev-envs/agents.yaml                  agent defaults for every project
~/.dev-envs/projects/<slug>-<hash>.yaml  per-project grants, tools, acceptances
<project>/.devenv.yaml                   the project's requests, committed and shared
```

Grants live **outside** the repository on purpose. Configuration inside a
repository is configuration the repository can grant itself, so cloning a
hostile project would widen its own access before anyone read it.

## Where to go next

- **[USAGE.md](USAGE.md)** — the common jobs, worked through
- **[../docs/ROADMAP.md](../docs/ROADMAP.md)** — design and security model
- **[../docs/PARITY.md](../docs/PARITY.md)** — what v1 did, and what v2 does instead
