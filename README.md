# dev

Run code in a container without learning Docker, with a sandbox that is
closed by default and opened one deliberate step at a time.

See [docs/ROADMAP.md](docs/ROADMAP.md) for where it is going and
[docs/PARITY.md](docs/PARITY.md) for what changed from the original bash
tool and why.

The bash version this replaces lives in [v1/](v1/). It installs itself as
`dev1`, so it stays available side by side rather than being deleted —
`dev migrate` says what changes.

**Adding this to a repository you already have? Start with
[QUICKSTART.md](QUICKSTART.md)** — including the awkward parts: monorepos,
production Dockerfiles, and why your first build ships 7 GB to the daemon.

**New here? Start with [USAGE.md](USAGE.md)** — it works through the
common jobs: updating dependencies, running software you do not trust,
sandboxing an agent, and running a server with no network at all.

## Install

```sh
make install        # builds dev into ~/.local/bin
make proxy-image    # builds the egress sidecar image into the OrbStack VM
dev doctor          # checks everything above
```

Shell completions, covering every command:

```sh
dev completion install     # uses $SHELL; or name bash, zsh or fish
```

Requires Go 1.26+ and OrbStack. `doctor` never changes anything — it
reports what it can see and what would block a run.

## Two minutes

```sh
cd ~/code/my-project

dev run -c 'go test ./...'   # detects the language, builds, runs
dev shell                    # a shell in the same container
dev status                   # what is running, under which policy
dev clean                    # tear it down
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

**An unattended run need not touch your working tree.** `--clone` mounts a
private copy of the repository instead, carrying your uncommitted work in
and keeping whatever happens next out. The changes stay on disk and stay
mergeable — just not in the directory you are editing.

**What the project asks for is not what it gets.** A `.devenv.yaml` in the
repository is a *request*. Running the project is not consent — anything
it asks for that you have not accepted stops the run and is shown to you
first.

## Commands

| | |
|---|---|
| `dev new LANG [dir]` | start a new project from a language plugin |
| `dev run [-c CMD]` | run a command in the project's container |
| `dev run --image IMG` | run a stock image instead, for things that are not projects |
| `dev shell` | interactive shell |
| `dev run --clone` | work in a private clone, leaving the working tree untouched |
| `dev run --clone-depth 1` | the same, copying only recent history — for large repositories |
| `dev build` | build the project image |
| `dev console` | full-screen view: workload, egress events, live prompts |
| `dev status` | what is running, what is allowed, what was blocked |
| `dev clean` | remove containers, networks and the sidecar (`--all` for every project) |
| `dev tools search TERM` | find what the image's package index has |
| `dev tools add TOOL` | add a tool to this project's environment, permanently |
| `dev tools add --shared TOOL` | add it to `.devenv.yaml` so the team gets it too |
| `dev tools list` / `remove` | list and remove those tools |
| `dev pin` | pin base images to digests, so builds are reproducible |
| `dev scan` | scan the image for vulnerabilities; exits non-zero so CI can gate |
| `dev update` | move base images and packages to current, and report what moved |
| `dev accept` | review what `.devenv.yaml` requests |
| `dev agent run NAME` | run a coding agent in the sandbox |
| `dev agent allow HOST` | grant an egress destination for this project |
| `dev clone list` | private clones on this machine, and what work is still in them |
| `dev history` | what past runs reached, and what was blocked |
| `dev agent grants` | granted destinations, and whether anything still uses them |
| `dev policy` | show the rules this machine enforces on everyone |
| `dev doctor` | diagnose the setup: backend, sidecar, disk, build context |
| `dev devcontainer` | export this environment as a devcontainer.json for teammates without dev |
| `dev migrate` | what changes for an existing v1 user |
| `dev vm` | start / inspect the container VM |

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

## Repository layout

```
cmd/ internal/          the tool
languages/<lang>/       language plugins, installed into ~/.dev-envs
docs/                   design and history
v1/                     the bash tool this replaces, installed as dev1
```

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

- **[QUICKSTART.md](QUICKSTART.md)** — adopting dev in an existing repository
- **[USAGE.md](USAGE.md)** — the common jobs, worked through
- **[docs/ROADMAP.md](docs/ROADMAP.md)** — design and security model
- **[docs/PARITY.md](docs/PARITY.md)** — what v1 did, and what this does instead
