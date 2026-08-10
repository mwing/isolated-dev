# dev

Run code in a container without learning Docker, in a sandbox that is
closed by default and opened one deliberate step at a time.

```sh
cd ~/code/my-project
dev run -c 'npm test'      # detects the language, builds, runs
```

Nothing of yours is in that container unless you put it there: no SSH
keys, no `~/.gitconfig`, no host environment, no docker socket. It reaches
its language's package registries and nothing else until you say
otherwise.

---

## Install

Requires Go 1.26+ and OrbStack.

```sh
make install        # builds dev into ~/.local/bin
make proxy-image    # builds the egress sidecar image into the VM
dev doctor          # checks everything above; changes nothing
dev completion install
```

Coming from the bash version? It is now `dev1`, kept in [v1/](v1/) and
installed from `v1/install.sh`. Run `dev migrate` for what changes.

---

## Start here

```sh
dev interactive
```

Reports what is set up and what is not, then offers the next steps with an
explanation of each. Every entry maps to a command you could have typed,
and shows which one.

Then, depending on what you came for:

| | |
|---|---|
| **[docs/CONCEPTS.md](docs/CONCEPTS.md)** | how it works — six ideas, after which the commands are guessable |
| **[docs/GETTING-STARTED.md](docs/GETTING-STARTED.md)** | adopting this in a repository you already have, including monorepos |
| **[docs/USE-CASES.md](docs/USE-CASES.md)** | the jobs, worked through end to end |
| **[docs/COMMANDS.md](docs/COMMANDS.md)** | every command and flag |

---

## What you get that plain `docker run` does not

**Egress is filtered by default.** The workload sits on a network with no
route out; a sidecar is the only path, and it allows destinations by
hostname without terminating TLS. A process that ignores `HTTP_PROXY` gets
nowhere.

**A blocked destination can be a question.** In `dev console`, or with
`--egress-prompt ask`, the request is *held* while you decide: allow once,
allow for this project, or refuse.

**What the project asks for is not what it gets.** A `.devenv.yaml` in the
repository is a *request*. Running the project is not consent — anything
you have not accepted stops the run and is shown to you first.

**An unattended run need not touch your working tree.** `--clone` gives it
a private copy of the repository; the changes come back through git when
you approve them.

**What happened is recorded.** `dev history` says what past runs reached
and what was blocked; `dev agent grants` says which of your grants anything
still uses.

---

## The common commands

```sh
dev run -c 'CMD'        # run one command in the sandbox
dev shell               # interactive shell
dev console             # full-screen: output, egress decisions, prompts
dev agent run claude    # a coding agent, sandboxed (add --clone)
dev status              # what is running, under which policy
dev scan                # vulnerabilities in the image you actually run
dev clean               # tear it down
```

Full list: [docs/COMMANDS.md](docs/COMMANDS.md). Every command takes
`--verbose`, which prints the exact `docker` invocation before running it.

---

## Layout

```
cmd/ internal/          the tool
languages/<lang>/       language plugins, installed into ~/.dev-envs
docs/                   guides, design and history
v1/                     the bash tool this replaces, installed as dev1
```

Design and rationale: [docs/ROADMAP.md](docs/ROADMAP.md). What changed from
v1 and why: [docs/PARITY.md](docs/PARITY.md).
