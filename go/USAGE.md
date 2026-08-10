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

## Starting a project

```sh
dev2 new python my-app
cd my-app
dev2 run -c 'python main.py'
```

The files come from the language plugin, so adding a language means adding
a directory rather than changing this tool. `dev2 new` with an unknown
language lists what is available, and with an unknown version says which
ones the plugin declares — a version that does not exist would otherwise
fail much later, inside a build.

It does **not** write a Dockerfile. The language template is rendered at
build time and stays current, whereas a copy in the project is a copy that
goes stale. Write one when you want to change it: `dev2 build` prefers a
project Dockerfile over the template.

Scaffolding never overwrites. If anything would collide, nothing is
written at all — a half-scaffolded directory is worse than a refusal, and
`--force` is there when you mean it.

---

## Letting an agent run without risking your working tree

```sh
dev2 agent run claude --clone
dev2 run --clone -c './migrate.sh'
```

The container gets a private clone of the repository rather than the
directory you are editing. Your uncommitted changes and untracked files
are carried in, so the run starts from what you see; anything it does
afterwards — including `rm -rf` — lands in the clone.

```
Clone:    ~/.dev-envs/clones/my-project
          uncommitted changes carried in
          1 untracked file(s) copied in
Bring back: git -C . fetch ~/.dev-envs/clones/my-project
```

The work is not thrown away, it is moved: review it with
`git -C <clone> status`, and fetch what you want back. A second run reuses
the same clone rather than discarding the first one's work, and says how
far it has drifted from the project.

Two things it does not carry: ignored files, so dependencies are installed
inside the sandbox rather than copied, and git objects are copied rather
than hardlinked — a hardlinked object store is still your repository's own
data, which is the thing being protected here.

For a large repository, copy only the history the run needs:

```sh
dev2 agent run claude --clone-depth 1
```

`--clone-depth` implies `--clone`. It changes the transport git uses, not
the guarantee: a shallow clone is fetched over `file://`, which copies
objects rather than sharing them, so the project's own object store is
still untouched. The cost is that `git log` is short and anything deriving
a version from `git describe` will see less than the project does.

Needs a git repository, and the repository root.

---

## Developing a patch in a clone, and landing it

The clone is a real repository, so the whole change can be made, reviewed
and merged without the sandbox ever touching your working tree. This is
the workflow worth learning.

**1. Do the work in the clone.**

```sh
dev2 agent run claude --clone-depth 1 -- "fix the retry logic in worker.py"
```

Or by hand, which is the same thing:

```sh
dev2 shell --clone
# inside: edit, run tests, commit
$ git checkout -b fix-retries
$ git commit -am 'fix retry backoff'
```

Commit inside the clone. An uncommitted change is still recoverable, but a
commit is what makes the next two steps one command each.

**2. Look at what came out, from outside.**

```sh
CLONE=~/.dev-envs/clones/my-project

git -C $CLONE log --oneline -5
git -C $CLONE status
git -C $CLONE diff HEAD~1        # or against whatever it started from
```

You are reading this with your own tools, on the host, with the sandbox
gone. Nothing you are about to merge has run on your machine.

**3. Bring it back.**

```sh
git fetch $CLONE fix-retries          # the branch, by name
git log --oneline FETCH_HEAD -3       # confirm it is what you read
git cherry-pick FETCH_HEAD            # one commit
```

For a series of commits, merge or rebase the fetched branch instead:

```sh
git fetch $CLONE fix-retries:fix-retries   # create the branch locally
git rebase fix-retries                     # or: git merge fix-retries
```

If the agent worked on `HEAD` without a branch, `git fetch $CLONE HEAD`
and cherry-pick `FETCH_HEAD` — the commits are reachable either way.

**This works from a shallow clone too.** The new commits sit on top of a
commit your repository already has, so fetching them needs nothing that
was left behind. Verified by a test that shallow-clones, commits, fetches
back and cherry-picks.

**4. If it went badly, throw it away.**

```sh
rm -rf $CLONE
```

Nothing outside that directory changed. The next `--clone` run starts
fresh — which is the point of running it there in the first place.

### When you would not use a clone

For your own interactive work, the plain bind mount is better: edits
appear in your editor immediately, and there is no step 3. Reach for
`--clone` when the run is unattended, when the thing running is not
trustworthy, or when you want a change to arrive as a diff you approve
rather than as edits already made.

---

## Reviewing what a project reached

Every filtered run is recorded, so the summary that scrolls past is still
there afterwards:

```sh
dev2 history              # recent runs: what each reached, what was blocked
dev2 history --denied     # only the runs that hit the allowlist
dev2 history hosts        # every destination, most recent first
```

This is the question worth asking after running something you did not
write: not "was it blocked" during the run, but "what did it try to talk
to" once you have time to look.

Grants are then reviewable against it:

```sh
dev2 agent grants         # each grant, and when it was last actually used
dev2 agent grants prune   # the ones nothing has reached; --apply to remove
```

An allowlist that only ever grows stops meaning anything. `prune` refuses
to act on a thin history — "never used" across three runs is not a
finding — and never touches hosts you accepted from `.devenv.yaml`, since
withdrawing consent is a different act from tidying your own grants.

Records live under `~/.dev-envs`, mode 0600, never in the repository: what
your machine reached is not the repository's business.

---

## Teammates who use an IDE instead of dev2

A dev container gives them isolation without this tool:

```sh
dev2 devcontainer          # writes .devcontainer/, then commit it
```

The file describes the same image dev2 builds — same base, same digests
if pinned, same tools, same unprivileged uid 1000 — so files written in
either belong to the same user. Verified by building the generated file
with plain `docker`: it produced the same image ID dev2 had built.

It exports the **environment**, not the **sandbox**. Egress filtering
lives in a proxy sidecar dev2 starts, and an editor will not start one, so
an exported container has ordinary network access. The generated file says
so in a comment; a teammate who assumes otherwise is worse off than one
who never had the file.

This is the opposite direction from the read side: dev2 already builds
from a `devcontainer.json` when a project has one. After exporting, both
tools build the same file — regenerate rather than edit it, and dev2 keeps
applying pins, upgrades and tools on top.

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

## What persists and what does not

A reasonable expectation is that installing something inside the container
keeps it. It does not, for two independent reasons:

```sh
dev2 run -c 'apk add jq'      # ERROR: Permission denied
```

The container runs as an unprivileged uid the tool sets, so a package
manager cannot write to the image in the first place. And containers run
with `--rm`, so even a change made as root would be discarded when the
command exits.

| written to | survives? |
|---|---|
| `/workspace` | **yes** — it is your project directory, on your disk |
| anywhere else | no — the container is discarded when it exits |

That is why `dev2 tools add` exists: it records the tool and rebuilds the
image from that record, which keeps the environment reproducible instead
of turning it into a pet container that only works on one machine.

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
it is yours until you choose to share it:

```sh
dev2 tools add --shared ripgrep
```

That writes it into the project's `.devenv.yaml` instead, where it becomes
a request the team shares. Commit it, and a teammate sees:

```
.devenv.yaml requests settings you have not accepted:

  tools: ripgrep
      install these packages into the project image: ripgrep
      (installed during a build, which runs unfiltered)
```

One `dev2 accept --all` and they have the same environment. Your own
acceptance is recorded when you share it — you are the one asking, and
making you accept your own edit would teach you to click through the
prompt that protects everyone else.

---

## Pinning what a build fetches

Egress control governs a *running* container. A build is different: it
runs on the daemon, outside the sandbox, and fetches whatever the tag
points at today. `golang:1.26-alpine` is a moving target — the same
command a month from now can produce a different image with no edit to
anything.

```sh
dev2 pin
```

```
  + golang:1.26-alpine
      golang@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2

Recorded in .devenv.yaml
```

Builds then use the digest:

```
Step 1/9 : FROM golang@sha256:0178a641…
```

Commit it and a teammate builds the *same image*, not merely the same
tag. The original tag is kept as a comment above each pinned line, since a
bare digest tells a reader nothing about what it was meant to be.

Re-resolve when you mean to:

```sh
dev2 pin --update
```

`dev2 build` reports anything still unpinned, so the gap is visible rather
than assumed.

Pinning needs no `dev2 accept`: it narrows what a build fetches rather
than widening it, and the project already chooses its own base images. A
prompt that always deserves yes is how prompts stop being read.

---

## Scanning what you are about to run

```sh
dev2 scan
```

Runs whichever of trivy and grype are installed against the image this
project actually runs — including any tools added to it, whose packages
are as real as the base image's.

```
Image:     dev-img-go
Scanners:  trivy, grype
Threshold: high and above

Clean at high and above.
```

It **exits non-zero** when something is found at or above the threshold,
so CI can gate on it:

```sh
dev2 scan --severity critical || exit 1
```

Lower the threshold to see more (`--severity medium`), or use
`--report-only` to print findings without failing. A scanner that cannot
run is reported as a failure rather than a pass — "no findings" and "no
scan" are different answers.

**Only findings with an available fix are reported.** A real image here
produced 303 findings of which 13 had a fix; the rest were CVEs Debian has
acknowledged and not patched (`will_not_fix`, `fix_deferred`, or simply no
release). A gate that fails on those cannot be satisfied by anything the
user does, so it gets switched off — and takes the 13 that mattered with
it. `--include-unfixed` shows the whole picture when you want it, which is
usually when choosing a base image rather than when gating a build.

If a scan still shows findings after `dev2 update`, look at where they
live. OS packages with no fix are the distribution's backlog and nothing
local will move them; findings in a *language* package (an npm or pip
dependency baked into the base image) are fixed by updating that, which
`dev2 update` cannot do for you.

To check an image before adopting it:

```sh
dev2 scan --image debian:11 --severity critical
```

That pulls it if you do not have it. Scanning something before running it
is a reasonable thing to want.

---

## Keeping a pinned project patched

A pin fixes what a build fetches. That is what makes a build reproducible,
and it is also what stops security updates arriving — the same property,
seen from either end. `dev2 update` is the command that moves the pin on
purpose and says what moved:

```sh
dev2 update
```

```
Base images
  moved golang:1.26-alpine
    from golang@sha256:1111111…
    to   golang@sha256:0178a641…

  Recorded in .devenv.yaml — commit it.

Rebuilding without cache
```

Three things happen.

The base images re-resolve to what their tags point at now. The image
rebuilds **without cache**, because a cached install layer reinstalls
exactly what it installed the first time — the version being updated away
from. And the packages the base image *shipped with* are upgraded.

That third one is where most of a scanner's findings live, and neither of
the first two touches it: a new base digest only helps once upstream
rebuilds, and a cacheless rebuild only re-runs what your Dockerfile asks
for. Without it an update can look successful and change nothing.

It is recorded as `upgrade_packages: true` rather than applied once, so a
later plain `dev2 build` does not quietly reintroduce what was just fixed.
Any tools you added rebuild too.

```sh
dev2 update --scan        # and see whether it helped
dev2 update --keep-pins   # packages only; the base image stays put
```

That report is the point. A pinned project is only safe to leave pinned if
someone can see when it last advanced — otherwise pinning quietly turns
into neglect.

Package versions inside the image are not pinned, only the base image is.
An update fetches current packages at build time, so two builds a week
apart can differ even at the same pin. Pinning every package is possible
in principle and miserable in practice; the base digest is where the
leverage is.

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

## A project that already has a devcontainer

If the repository has a `.devcontainer/devcontainer.json`, dev2 reads it:
the image or Dockerfile it names, and its `forwardPorts`. Nothing needs
converting, and the team does not have to agree to a second config file.

```
Dockerfile: .devcontainer/devcontainer.json (image node:22-bookworm-slim)
```

Precedence is most-specific-first: a `Dockerfile` at the project root, then
the devcontainer, then the language template.

What it does **not** honor is said out loud rather than dropped quietly:

```
⚠  devcontainer.json: containerEnv (AWS_PROFILE): environment passthrough
   is a grant here, not a setting
⚠  devcontainer.json: remoteUser/containerUser: the uid is set by dev2, so
   an image cannot choose to run as root
⚠  devcontainer.json: postCreateCommand: not run
```

Those are not oversights. `containerEnv` and `mounts` hand a container
access to your machine, which goes through `dev2 accept` rather than
through a file the repository ships. `remoteUser` cannot decide who the
container runs as, or a project could choose root. A config half-honored
*silently* is worse than one not read at all, because you would believe
the file describes what is running.

---

## Closing the unsafe paths for a team

`~/.dev-envs/policy.yaml` constrains everyone using the machine, including
the person running the command. It is for handing teammates a tool with
the risky options already shut.

```yaml
network_modes: [allowlist, none]        # `open` is not available
forbid: [mount_docker_socket, pass_env_vars]
deny_hosts: [pastebin.com, "*.evil.example.com"]
min_scan_severity: high                 # the loosest threshold permitted
allowed_registries: [ghcr.io, docker.io]
require:
  memory: 4g
```

```sh
dev2 policy      # what is in force, and where it came from
```

It holds against every route in, not just the polite ones:

```
$ dev2 run --network open
policy forbids network mode open: this machine permits only allowlist, none

$ dev2 agent allow pastebin.com
policy forbids egress to pastebin.com: matches the denied destination pastebin.com

$ dev2 scan --severity critical
Policy raises the threshold from critical to high.
```

A project requesting something forbidden is refused rather than offered
for acceptance — including something accepted before the rule existed,
since a rule that only applied to new decisions would leave the machines
that most need it untouched.

**What it is not.** The file is on the user's disk and they can edit it.
It closes the unsafe paths for people who are not attacking their own
laptop, and it makes an override deliberate rather than accidental. An
organization that needs more than that needs device management, not a YAML
file.

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
