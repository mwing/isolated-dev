# Using dev2

Worked examples for the jobs people actually reach for this tool to do.
Each one starts from a real problem rather than a feature.

- [Updating dependencies without trusting them](#updating-dependencies-without-trusting-them)
- [Running software you do not trust](#running-software-you-do-not-trust)
- [Sandboxing a coding agent](#sandboxing-a-coding-agent)
- [A server with no filesystem and no network](#a-server-with-no-filesystem-and-no-network)
- [Reaching a server you do want to use](#reaching-a-server-you-do-want-to-use)
- [Adding tools that stick](#adding-tools-that-stick)
- [Sharing configuration with a team](#sharing-configuration-with-a-team)
- [The console](#the-console)
- [When something looks stuck](#when-something-looks-stuck)

---

## Updating dependencies without trusting them

A dependency update runs arbitrary code from strangers: install scripts,
build hooks, `postinstall`. That code runs with whatever your shell has —
which is everything.

```sh
cd ~/code/my-project
dev2 shell
```

Inside, run the update as usual (`npm update`, `go get -u ./...`, `uv
lock --upgrade`). What that code can do is bounded:

- it reaches your language's registries and nothing else
- it sees the project directory and nothing else on your disk
- it has no SSH keys, no cloud credentials, no docker socket
- it is not root, and cannot become root

When something reaches for a host that is not allowed, the run reports it:

```
Egress: blocked destinations this run:
  blocked: telemetry.example.com:443
```

That line is worth reading rather than clearing. A package manager
fetching from its own registry is expected; a package phoning somewhere
else during install is the thing this tool exists to show you.

If a build genuinely needs another host:

```sh
dev2 run --allow-host proxy.corp.example.com -c 'go mod download'   # once
dev2 agent allow proxy.corp.example.com                             # from now on
```

**Caveat worth knowing:** image *builds* are not filtered, and neither is
`dev2 tools search`, which reads a package index over the same path. `docker build`
runs on the daemon, outside the sandbox network, so a `Dockerfile` or a
`dev2 add` install step downloads over an unfiltered path. The allowlist
is a runtime control, not a supply chain one.

---

## Running software you do not trust

A binary from a forum, a script from a gist, someone's proof of concept.

```sh
mkdir -p ~/inspect && cd ~/inspect
cp ~/Downloads/suspicious ./
dev2 run --image debian:bookworm-slim --offline -c './suspicious'
```

`--image` runs a stock image directly, so a directory that is not a
project needs no Dockerfile written before the thing can be looked at.
`--offline` gives it no network at all. It sees `~/inspect` as
`/workspace` and nothing else of yours.

Put the directory somewhere OrbStack shares — under your home directory is
safe. `/tmp` is not shared with the VM, and a bind mount from there fails
with "bind source path does not exist".

If it needs the network to be interesting, give it the network but watch
where it goes:

```sh
dev2 run --network allowlist --egress-prompt ask -c './suspicious'
```

Now every destination it reaches for stops and asks you, holding the
connection while you decide. Answer `n` and it stays blocked; the program
gets a failed connection, which is usually more informative than a
timeout.

To see the policy before running anything:

```sh
dev2 status
```

**What this does not protect against.** The sandbox contains what the
program can reach and read. It is a container, not a hypervisor: a kernel
escape is out of scope. And anything you *allow* is a place data can go —
if you permit `github.com`, a determined program can put data there.

---

## Sandboxing a coding agent

An agent acts on instructions from a model, so it does not inherit your
trust in the project. It always runs at the untrusted level regardless of
project settings.

```sh
dev2 agent list                # what is available and what each may reach
dev2 agent policy claude       # exactly what a run would permit
dev2 agent run claude          # go
```

What the agent gets: the project directory, its own tools, its own home
(a named volume, so a login survives across runs), and its API endpoint.
What it does not get: your keys, your environment, any host path but the
project.

**Committing and pushing.** The agent can commit — its identity comes from
your `user.name` and `user.email`, nothing else from your gitconfig — but
it cannot push. That is the review boundary: you read the diff and push
yourself. If you want it to push:

```sh
dev2 agent run claude --allow-push
```

That forwards your **ssh-agent socket**, never a key file. The key stays
on the host; with a hardware or biometric agent (1Password, a YubiKey)
every signature still needs your approval. Understand that this is the one
grant that lets the agent write to a remote as you.

**Running the agent with live prompts:**

```sh
dev2 console --agent claude
```

Same sandbox, but a blocked destination becomes a question while the
agent waits, instead of an error it has to work around.

**Auth without a browser** (CI, or an org that blocks device-code login):

```sh
dev2 agent run codex --auth env --auth-env OPENAI_API_KEY
```

Only the variable you name is passed. Nothing is taken implicitly.

---

## A server with no filesystem and no network

Running a service to poke at it, without letting it touch anything.

```sh
cd ~/code/service
dev2 run --offline -c './server --port 8080'
```

No network at all: it cannot call home, cannot fetch, cannot resolve. It
still sees `/workspace`.

To also deny it anything of yours — a truly empty box — run from an empty
directory:

```sh
mkdir -p ~/empty && cd ~/empty
dev2 run --image debian:bookworm-slim --offline -c 'sh'
```

To let it serve but not reach out, see the next section: published ports
are inbound, and inbound is not governed by the egress allowlist.

---

## Reaching a server you do want to use

Ports are published automatically from the language's defaults, or from
`forward_ports` in `.devenv.yaml`:

```sh
dev2 run -c 'python3 -m http.server 8080'
#   ↦ http://127.0.0.1:8080 → container :8080
```

Ports bind to `127.0.0.1`, not `0.0.0.0` — a development service should
not appear on the local network because you ran a command.

In allowlist mode the workload has no gateway of its own, so the **sidecar
publishes and relays**. That is deliberate: one component owns the whole
network boundary. Inbound is not filtered by the allowlist, because that
list answers "what may this reach", while a published port answers "what
may reach this" — and you answered that by asking for the port.

---

## Adding tools that stick

You are in a shell, and `jq` is not there. Look it up rather than
guessing at the name:

```sh
dev2 tools search jq
```

That asks the package manager inside this project's image what it has, so
a wrong name is caught before a build fails on it. Then:

```sh
dev2 tools add jq
```

It is recorded and the image rebuilt; it is there on every later run.
Nothing was configured in advance and nothing is a pet container — the
record is a declaration and the image is rebuilt from it, so the
environment stays reproducible.

```sh
dev2 tools               # what this project has
dev2 tools remove jq     # rebuilds on the next run
```

If a name turns out not to exist, the record is backed out rather than
left behind for every later build to fail on, and the error points at
`dev2 tools search`.

The record lives in `~/.dev-envs/projects/…`, outside the repository, so
it is yours until you choose to share it.

---

## Sharing configuration with a team

`.devenv.yaml` is committed and shared. It states what the project
*needs*:

```yaml
agents:
  default:
    allow_hosts:
      - proxy.golang.org
      - sum.golang.org
    base: golang:1.26
```

It grants nothing on its own. A teammate cloning the repository sees:

```
.devenv.yaml requests egress you have not accepted:

  proxy.golang.org
  sum.golang.org

Review with:  dev2 agent accept
```

One `dev2 agent accept --all` and they are working — rather than
rediscovering the allowlist by hitting blocks. Their acceptance is
recorded on their machine, never in the repository.

Acceptance is per value, not per key: if the file later asks for something
new, it asks again. Consent is not a blank cheque for a future edit.

Settings that weaken the sandbox — `network: open`, mounts, environment
passthrough — go through the same gate:

```sh
dev2 accept          # review what the project asks for
dev2 accept --all
```

---

## The console

```sh
dev2 console                    # a shell, with a live egress pane
dev2 console --agent claude     # an agent, same
dev2 console -c 'make test'     # one command
```

The console owns the screen, which is what makes a blocking prompt work:
outside it, a prompt and an interactive program fight over the keyboard.

| key | |
|---|---|
| `ctrl+q` | leave |
| `ctrl+c` twice quickly | leave |
| `ctrl+c` once | goes to the program (interrupt) |
| `o` / `p` / `n` | when a question is waiting: allow once / allow for the project / refuse |

A waiting question takes the keyboard, so an answer cannot be typed into
the shell by accident. Everything the console does has a non-interactive
equivalent — nothing is console-only.

---

## When something looks stuck

**Get out first.** `ctrl+q`, or `ctrl+c` twice. From another terminal,
`dev2 clean` in that project removes its containers.

**Then look at what it was allowed to do:**

```sh
dev2 status
```

**If a blocked host is the cause**, the run tells you the three ways
forward:

```
Allow once:       --allow-host HOST
Allow from now:   dev2 agent allow HOST
Unrestricted:     --network open
```

**If a bind mount fails with "bind source path does not exist"**, the
directory is somewhere the VM does not share. Move it under your home
directory; `/tmp` in particular is not shared.

**If the screen itself misbehaves**, record it. A screenshot shows the
result of rendering; it cannot show which size the program believed it
had, which is usually the question:

```sh
dev2 console --agent claude --record /tmp/rec.jsonl
# reproduce, then ctrl+q
dev2 console --replay /tmp/rec.jsonl | head -20
```

That prints the sizes the workload was given alongside the screen it
produced.

**If the sidecar looks wrong after an upgrade**, its image may be older
than the binary:

```sh
make proxy-image
```

The tool detects this case and says so, but rebuilding is harmless
anyway.
