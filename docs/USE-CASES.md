# Use cases

Worked examples for the jobs people actually reach for this tool to do.
Each one starts from a real problem rather than a feature.

New to it? [CONCEPTS.md](CONCEPTS.md) explains how it works in six ideas,
and [GETTING-STARTED.md](GETTING-STARTED.md) covers adopting it in a
repository you already have.

**Everyday**

- [Starting a project](#starting-a-project)
- [Adding tools that stick](#adding-tools-that-stick)
- [What persists and what does not](#what-persists-and-what-does-not)
- [Sharing configuration with a team](#sharing-configuration-with-a-team)
- [A project that already has a devcontainer](#a-project-that-already-has-a-devcontainer)
- [Teammates who use an IDE instead of dev](#teammates-who-use-an-ide-instead-of-dev)

**Agents, and keeping them off your working tree**

- [Sandboxing a coding agent](#sandboxing-a-coding-agent)
- [Letting an agent run without risking your working tree](#letting-an-agent-run-without-risking-your-working-tree)
- [Developing a patch in a clone, and landing it](#developing-a-patch-in-a-clone-and-landing-it)

**Code you do not trust**

- [Running software you do not trust](#running-software-you-do-not-trust)
- [A server with no filesystem and no network](#a-server-with-no-filesystem-and-no-network)
- [Reaching a server you do want to use](#reaching-a-server-you-do-want-to-use)

**Supply chain**

- [Updating dependencies without trusting them](#updating-dependencies-without-trusting-them)
- [Pinning what a build fetches](#pinning-what-a-build-fetches)
- [Scanning what you are about to run](#scanning-what-you-are-about-to-run)
- [Keeping a pinned project patched](#keeping-a-pinned-project-patched)

**Review and operations**

- [Reviewing what a project reached](#reviewing-what-a-project-reached)
- [Tests that need docker, and what that costs](#tests-that-need-docker-and-what-that-costs)
- [Closing the unsafe paths for a team](#closing-the-unsafe-paths-for-a-team)
- [The console](#the-console)
- [When something looks stuck](#when-something-looks-stuck)

---

## Starting a project

```sh
dev new python my-app
cd my-app
dev run -c 'python main.py'
```

The files come from the language plugin, so adding a language means adding
a directory rather than changing this tool. `dev new` with an unknown
language lists what is available, and with an unknown version says which
ones the plugin declares — a version that does not exist would otherwise
fail much later, inside a build.

It does **not** write a Dockerfile. The language template is rendered at
build time and stays current, whereas a copy in the project is a copy that
goes stale. Write one when you want to change it: `dev build` prefers a
project Dockerfile over the template.

Scaffolding never overwrites. If anything would collide, nothing is
written at all — a half-scaffolded directory is worse than a refusal, and
`--force` is there when you mean it.

---

## Letting an agent run without risking your working tree

```sh
dev agent run claude            # clones by default
dev run --clone -c './migrate.sh'   # opt in for a plain run
```

An agent gets a private clone rather than the directory you are editing.
Your uncommitted changes and untracked files are carried in, so the run
starts from what you see; anything it does afterwards — including
`rm -rf` — lands in the clone.

```
Clone:     ~/.dev-envs/clones/my-project
           uncommitted changes carried in
           1 untracked file(s) copied in
Review:    dev clone diff
Bring back: dev clone apply
```

The work is not thrown away, it is moved. A second run reuses the same
clone rather than discarding the first one's work, and says how far it has
drifted from the project.

`--in-place` runs the agent in your working tree instead, when you want
that. `dev run` and `dev shell` already work there — a human editing their
own files is what the plain mount is for.

Two things it does not carry: ignored files, so dependencies are installed
inside the sandbox rather than copied, and git objects are copied rather
than hardlinked — a hardlinked object store is still your repository's own
data, which is the thing being protected here.

For a large repository, copy only the history the run needs:

```sh
dev agent run claude --clone-depth 1
```

On `dev run` and `dev shell`, `--clone-depth` implies `--clone`; an agent
is already cloning. It changes the transport git uses, not the guarantee: a
shallow clone is fetched over `file://`, which copies objects rather than
sharing them, so the project's own object store is still untouched. The
cost is that `git log` is short and anything deriving a version from
`git describe` sees less than the project does.

A clone needs a git repository, and the repository root. `dev run --clone`
refuses without one, since you asked for it explicitly. An agent run
continues in your files instead and says so loudly — it clones by default,
so failing over a flag you never typed would be blaming you for the tool's
own choice.

---

## Developing a patch in a clone, and landing it

The clone is a real repository, so the whole change can be made, reviewed
and merged without the sandbox ever touching your working tree. This is
the workflow worth learning.

**1. Do the work in the clone.**

```sh
dev agent run claude -- "fix the retry logic in worker.py"
```

Or by hand, which is the same thing:

```sh
dev shell --clone
# inside: edit, run tests, commit
$ git checkout -b fix-retries
$ git commit -am 'fix retry backoff'
```

Commit inside the clone. An uncommitted change is still recoverable, but a
commit is what makes the next two steps one command each.

**2. Look at what came out, from outside.**

```sh
dev clone diff           # commits the project lacks, then uncommitted changes
dev clone diff --stat    # by file, when the patch is long
```

You are reading this with your own tools, on the host, with the sandbox
gone. Nothing you are about to merge has run on your machine.

**3. Bring it back.**

```sh
dev clone apply
```

Most of that has already happened. The run fetched the clone's commits into
your project when it ended — and the next run does it again at the start,
so a container that crashed before you applied anything has not cost you
the work. `apply` brings them onto your branch and releases the refs that
were holding them.

Where your branch has moved too, it stops and hands you the decision:

```
Your branch is unchanged. The clone's 2 commit(s) are here, on clone-work.

Both histories have moved, so combining them is a judgement about code
this command has not read. Yours to make:
  git log --oneline HEAD..clone-work   # what it did
  git cherry-pick clone-work           # take it, if it is one commit
  git merge clone-work                 # keeps both histories
  git rebase clone-work                # replay yours on top
```

It will not merge for you. An automatic merge is a judgement about code
this tool has not read.

Uncommitted changes stay in the clone: `apply` moves commits, and
committing is the act that says "this is work". It says so rather than
quietly bringing back less than you asked for.

**This works from a shallow clone too.** The new commits sit on top of a
commit your repository already has, so fetching them needs nothing that
was left behind. Verified by a test that shallow-clones, commits, fetches
back and cherry-picks.

**4. If it went badly, throw it away.**

```sh
dev clone rm            # this project's clone
dev clone rm --force    # even if it still holds work
```

`rm` refuses while the clone holds commits the project does not have, or
uncommitted changes — that is the one irreversible thing this tool can do
to work that exists nowhere else. Once you have fetched the commits back,
it stops objecting: it asks the project whether it contains them, rather
than asking the clone whether it pushed them.

Nothing outside that directory changed. The next agent run starts fresh —
which is the point of running it there in the first place.

### Keeping the disk honest

Every agent run makes one of these, and a clone is a full copy of a
repository. That adds up quietly, which is the worst way for a disk
problem to arrive:

```sh
dev clone list                      # every clone, its size, and what is in it
dev clone prune                     # remove the ones holding nothing
dev clone prune --dry-run           # the number first
dev clone prune --older-than 30d    # a wider window than the default week
dev clone prune --force             # take the ones holding work too
dev clone path                      # this project's, for scripting
```

`prune` keeps three kinds of clone and says which: those holding commits
the project does not have, those with uncommitted changes, and those
touched within the window — a run that finished this morning is one
somebody may still be reading.

Past 5 GiB the total is printed when a clone is created, so the number
arrives while you are there rather than when the disk fills.

```
  acme-platform            1.2G     fix-retries            2 commit(s) only here
  scratch-repo             144K     main                   clean

2 clone(s), 1.3G on disk, in ~/.dev-envs/clones
1 hold work that is not in their project.
```

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
dev history              # recent runs: what each reached, what was blocked
dev history --denied     # only the runs that hit the allowlist
dev history hosts        # every destination, most recent first
```

This is the question worth asking after running something you did not
write: not "was it blocked" during the run, but "what did it try to talk
to" once you have time to look.

Grants are then reviewable against it:

```sh
dev grants               # each grant, and when it was last actually used
dev grants prune         # the ones nothing has reached; --apply to remove
```

An allowlist that only ever grows stops meaning anything. `prune` refuses
to act on a thin history — "never used" across three runs is not a
finding — and never touches hosts you accepted from `.devenv.yaml`, since
withdrawing consent is a different act from tidying your own grants.

Records live under `~/.dev-envs`, mode 0600, never in the repository: what
your machine reached is not the repository's business.

---

## Teammates who use an IDE instead of dev

A dev container gives them isolation without this tool:

```sh
dev devcontainer          # writes .devcontainer/, then commit it
```

The file describes the same image dev builds — same base, same digests
if pinned, same tools, same unprivileged uid 1000 — so files written in
either belong to the same user. Verified by building the generated file
with plain `docker`: it produced the same image ID dev had built.

It exports the **environment**, not the **sandbox**. Egress filtering
lives in a proxy sidecar dev starts, and an editor will not start one, so
an exported container has ordinary network access. The generated file says
so in a comment; a teammate who assumes otherwise is worse off than one
who never had the file.

This is the opposite direction from the read side: dev already builds
from a `devcontainer.json` when a project has one. After exporting, both
tools build the same file — regenerate rather than edit it, and dev keeps
applying pins, upgrades and tools on top.

---

## Updating dependencies without trusting them

A dependency update runs arbitrary code from strangers: install scripts,
build hooks, `postinstall`. That code runs with whatever your shell has —
which is everything.

```sh
cd ~/code/my-project
dev shell
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
dev run --allow-host proxy.corp.example.com -c 'go mod download'   # once
dev allow proxy.corp.example.com                                   # from now on
```

**Caveat worth knowing:** image *builds* are not filtered, and neither is
`dev tools search`, which reads a package index over the same path. `docker build`
runs on the daemon, outside the sandbox network, so a `Dockerfile` or a
`dev add` install step downloads over an unfiltered path. The allowlist
is a runtime control, not a supply chain one.

---

## Running software you do not trust

A binary from a forum, a script from a gist, someone's proof of concept.

```sh
mkdir -p ~/inspect && cd ~/inspect
cp ~/Downloads/suspicious ./
dev run --image debian:bookworm-slim --offline -c './suspicious'
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
dev run --network allowlist --egress-prompt ask -c './suspicious'
```

Now every destination it reaches for stops and asks you, holding the
connection while you decide. Answer `n` and it stays blocked; the program
gets a failed connection, which is usually more informative than a
timeout.

To see the policy before running anything:

```sh
dev status
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
dev agent list                # what is available and what each may reach
dev agent policy claude       # exactly what a run would permit
dev agent run claude          # go
```

What the agent gets: the project directory, its own tools, its own
configuration directory (a named volume, so a login survives across runs),
and its API endpoint. What it does not get: your keys, your environment,
any host path but the project.

The rest of its home is not kept. Only the configuration directory
persists, and it persists per agent rather than per project — so one login
serves every repository, and nothing else an agent writes in one project is
there to be read in the next.

**MCP connectors are off unless you ask.** A claude.ai account's connectors
— Gmail, Linear, Notion — reach accounts *outside* the sandbox with live
tokens the config volume carries, which is exactly the reach the sandbox
exists to withhold from code it does not trust. So the host they route
through is not in the allowlist by default, and the agent is told to ignore
its inherited MCP config (which also stops an MCP server a hostile
repository shipped in a `.mcp.json`). Turn them on for a run you trust with
them:

```sh
dev agent run claude --allow-mcp
```

Even then, defence in depth holds: a connector is reachable only because
`--allow-mcp` adds its host to the allowlist, so it is still a named,
reviewable destination in `dev history` like any other.

**The other direction: an agent on your host, reaching for this.** The
above puts the agent inside the sandbox. The reverse arrangement — Claude
Code running normally on your machine, choosing to run untrusted commands
through `dev` — is a skill in `skills/sandboxed-execution/`, copied into
`~/.claude/skills/`.

Be clear about the difference. Inside, the agent cannot forget: it is not
the one deciding. With the skill it can, and an agent that forgets is one
running on your machine while you believe otherwise. The skill is a habit
worth having and not a boundary; where you want the guarantee, use
`dev agent run`.

**Committing and pushing.** The agent can commit — its identity comes from
your `user.name` and `user.email`, nothing else from your gitconfig — but
it cannot push. That is the review boundary: you read the diff and push
yourself. If you want it to push:

```sh
dev agent run claude --allow-push
```

That forwards your **ssh-agent socket**, never a key file. The key stays
on the host; with a hardware or biometric agent (1Password, a YubiKey)
every signature still needs your approval. Understand that this is the one
grant that lets the agent write to a remote as you.

The host it opens is your project's own `origin`, on the port that remote
names — not a fixed `github.com:22`. An `https` origin is refused rather
than translated: an ssh-agent cannot push over it, and this tool carries no
token into the container to push with.

**Running the agent with live prompts:**

```sh
dev console --agent claude
```

Same sandbox, but a blocked destination becomes a question while the
agent waits, instead of an error it has to work around.

**Auth without a browser** (CI, or an org that blocks device-code login):

```sh
dev agent run codex --auth env --auth-env OPENAI_API_KEY
```

Only the variable you name is passed. Nothing is taken implicitly.

---

## A server with no filesystem and no network

Running a service to poke at it, without letting it touch anything.

```sh
cd ~/code/service
dev run --offline -c './server --port 8080'
```

No network at all: it cannot call home, cannot fetch, cannot resolve. It
still sees `/workspace`.

To also deny it anything of yours — a truly empty box — run from an empty
directory:

```sh
mkdir -p ~/empty && cd ~/empty
dev run --image debian:bookworm-slim --offline -c 'sh'
```

To let it serve but not reach out, see the next section: published ports
are inbound, and inbound is not governed by the egress allowlist.

---

## Reaching a server you do want to use

Ports are published automatically from the language's defaults, or from
`forward_ports` in **your own** `~/.dev-envs/config.yaml`:

```sh
dev run -c 'python3 -m http.server 8080'
#   ↦ http://127.0.0.1:8080 → container :8080
```

Ports bind to `127.0.0.1`, not `0.0.0.0` — a development service should
not appear on the local network because you ran a command.

A project file cannot name ports to publish, and says so if it tries —
neither `forward_ports` in `.devenv.yaml` nor `forwardPorts` in a
`devcontainer.json`. Publishing opens a socket on your machine that
anything else on it can reach, which makes it a setting of yours rather
than something a repository asks for. Detected ports are a different
thing: those are this tool's guess about the language, not the
repository's request.

A project file *can* set `forward_ports` to empty, which publishes
nothing. Asking for less needs no permission from anyone.

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
dev run -c 'apk add jq'      # ERROR: Permission denied
```

The container runs as an unprivileged uid the tool sets, so a package
manager cannot write to the image in the first place. And containers run
with `--rm`, so even a change made as root would be discarded when the
command exits.

| written to | survives? |
|---|---|
| `/workspace` | **yes** — it is your project directory, on your disk |
| anywhere else | no — the container is discarded when it exits |

That is why `dev tools add` exists: it records the tool and rebuilds the
image from that record, which keeps the environment reproducible instead
of turning it into a pet container that only works on one machine.

---

## Adding tools that stick

You are in a shell, and `jq` is not there. Look it up rather than
guessing at the name:

```sh
dev tools search jq
```

That asks the package manager inside this project's image what it has, so
a wrong name is caught before a build fails on it. Then:

```sh
dev tools add jq
```

It is recorded and the image rebuilt; it is there on every later run.
Nothing was configured in advance and nothing is a pet container — the
record is a declaration and the image is rebuilt from it, so the
environment stays reproducible.

```sh
dev tools               # what this project has
dev tools remove jq     # rebuilds on the next run
```

If a name turns out not to exist, the record is backed out rather than
left behind for every later build to fail on, and the error points at
`dev tools search`.

The record lives in `~/.dev-envs/projects/…`, outside the repository, so
it is yours until you choose to share it:

```sh
dev tools add --shared ripgrep
```

That writes it into the project's `.devenv.yaml` instead, where it becomes
a request the team shares. Commit it, and a teammate sees:

```
.devenv.yaml requests settings you have not accepted:

  tools: ripgrep
      install these packages into the project image: ripgrep
      (installed during a build, which runs unfiltered)
```

One `dev accept --all` and they have the same environment. Your own
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
dev pin
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
dev pin --update
```

`dev build` reports anything still unpinned, so the gap is visible rather
than assumed.

Pinning needs no `dev accept`: it narrows what a build fetches rather
than widening it, and the project already chooses its own base images. A
prompt that always deserves yes is how prompts stop being read.

---

## Scanning what you are about to run

```sh
dev scan
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
dev scan --severity critical || exit 1
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

If a scan still shows findings after `dev update`, look at where they
live. OS packages with no fix are the distribution's backlog and nothing
local will move them; findings in a *language* package (an npm or pip
dependency baked into the base image) are fixed by updating that, which
`dev update` cannot do for you.

To check an image before adopting it:

```sh
dev scan --image debian:11 --severity critical
```

That pulls it if you do not have it. Scanning something before running it
is a reasonable thing to want.

---

## Keeping a pinned project patched

A pin fixes what a build fetches. That is what makes a build reproducible,
and it is also what stops security updates arriving — the same property,
seen from either end. `dev update` is the command that moves the pin on
purpose and says what moved:

```sh
dev update
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
later plain `dev build` does not quietly reintroduce what was just fixed.
Any tools you added rebuild too.

```sh
dev update --scan        # and see whether it helped
dev update --keep-pins   # packages only; the base image stays put
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

Review with:  dev accept
```

One `dev accept --all` and they are working — rather than
rediscovering the allowlist by hitting blocks. Their acceptance is
recorded on their machine, never in the repository.

Acceptance is per value, not per key: if the file later asks for something
new, it asks again. Consent is not a blank cheque for a future edit.

Settings that weaken the sandbox — `network: open`, mounts, environment
passthrough — go through the same gate:

```sh
dev accept          # review what the project asks for
dev accept --all
```

---

## A project that already has a devcontainer

If the repository has a `.devcontainer/devcontainer.json`, dev reads it:
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
⚠  devcontainer.json: remoteUser/containerUser: the uid is set by dev, so
   an image cannot choose to run as root
⚠  devcontainer.json: postCreateCommand: not run
```

Those are not oversights. `containerEnv` and `mounts` hand a container
access to your machine, which goes through `dev accept` rather than
through a file the repository ships. `remoteUser` cannot decide who the
container runs as, or a project could choose root. A config half-honored
*silently* is worse than one not read at all, because you would believe
the file describes what is running.

---

## Tests that need docker, and what that costs

Integration tests that start containers — testcontainers, a compose stack,
a build that produces an image — need a docker daemon. The sandbox does not
have one. The socket can be handed in, and this is the one grant the tool
treats differently from every other.

The project asks in `.devenv.yaml`:

```yaml
mount_docker_socket: true
```

That is a request, so a run stops until you answer it. Unlike every other
setting, "accept it" is not the answer offered first:

```
.devenv.yaml requests settings you have not accepted:

  mount_docker_socket: true
      mount the docker socket, which is root on the docker host

Review and accept:  dev accept
For this run only:   dev run --allow-docker-socket
Or ignore the file:  --network allowlist
```

The container also needs a client, which the language images do not carry:

```sh
dev tools add docker-cli
dev run --allow-docker-socket -c 'pytest tests/integration'
```

Use `docker-cli`, not `docker.io`. The tools layer installs with
`--no-install-recommends`, and on Debian `docker.io` only *recommends* the
command-line client — you get the daemon's helpers and no `docker`. The
name `docker-cli` is right on both Debian and Alpine.

What arrives is the socket and the group that can use it:

```
$ id
uid=1000(appuser) gid=1000(appuser) groups=1000(appuser),0(root)
$ docker ps --format '{{.Image}}'
dev-img-myproject-tools:37f8bf5b
dev-proxy:latest
```

Read the second line again. **The container can see, and control, the
sidecar enforcing its own egress policy.** That is not a flaw in how the
socket is mounted; it is what the socket is. The docker API can start a
container with any mount, as any user, on any network — so a workload
holding it can stop the proxy, or simply run something outside the
sandbox entirely.

Two consequences worth stating plainly:

| | |
|---|---|
| the allowlist | still governs the sandbox's own connections, and governs nothing the *daemon* does on its behalf — an image pulled through the socket is pulled by the host daemon, past the proxy |
| the isolation | ends here. Everything else in this document assumes the workload cannot reach the host; with the socket it can |

So it is granted one run at a time, and nothing is written down:

```sh
dev run --allow-docker-socket -c 'pytest tests/integration'   # this run only
```

Making it permanent takes a second sentence, because an acceptance is keyed
by the project's *path*:

```sh
dev accept mount_docker_socket --remember
```

`dev accept --all` will not do it for you. A remembered grant is inherited
by whatever occupies that path later — clone a different repository over
it and the new one asks for exactly the value you already approved, which
looks identical to the tool. For most settings that trade is fine. For
root on the host it is not, which is why the default is "ask again".

**Agents never receive it**, at any level of acceptance, with or without
the flag. An agent runs on instructions from a model; the whole reason it
gets a private clone is that it should not be able to edit what your host
runs later, and the docker socket is that with no upper bound.

If the tests only need a service rather than a daemon — a database, a
queue — the socket is the wrong tool. Run it on the host and grant the
destination, remembering that a raw TCP client needs the proxy taught to
it (see [CONCEPTS.md](CONCEPTS.md)).

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
dev policy      # what is in force, and where it came from
```

It holds against every route in, not just the polite ones:

```
$ dev run --network open
policy forbids network mode open: this machine permits only allowlist, none

$ dev allow pastebin.com
policy forbids egress to pastebin.com: matches the denied destination pastebin.com

$ dev run --allow-host pastebin.com
policy forbids egress to pastebin.com: matches the denied destination pastebin.com

$ dev accept --all                 # the project asked for it in .devenv.yaml
policy forbids egress to pastebin.com: matches the denied destination pastebin.com

$ dev scan --severity critical
Policy raises the threshold from critical to high.
```

The same holds for `dev agent run`, which is an allowlist run and so needs
a machine that permits one, and for the prompt that offers to allow a
blocked destination mid-run: a denied host is refused outright rather than
put to the user as a question with one permitted answer.

A project requesting something forbidden is refused rather than offered
for acceptance. So is something accepted or granted before the rule
existed: a setting stops applying, and a destination is dropped from the
run's allowlist with a line saying so. A rule that bound only new
decisions would leave the machines that most need it untouched.

**What it is not.** The file is on the user's disk and they can edit it.
It closes the unsafe paths for people who are not attacking their own
laptop, and it makes an override deliberate rather than accidental. An
organization that needs more than that needs device management, not a YAML
file.

---

## The console

```sh
dev console                    # a shell, with a live egress pane
dev console --agent claude     # an agent, same
dev console -c 'make test'     # one command
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
`dev clean` in that project removes its containers.

**Then look at what it was allowed to do:**

```sh
dev status
```

**If a blocked host is the cause**, the run tells you the three ways
forward:

```
Allow once:       --allow-host HOST
Allow from now:   dev allow HOST
Unrestricted:     --network open
```

**If a bind mount fails with "bind source path does not exist"**, the
directory is somewhere the VM does not share. Move it under your home
directory; `/tmp` in particular is not shared.

**If the screen itself misbehaves**, record it. A screenshot shows the
result of rendering; it cannot show which size the program believed it
had, which is usually the question:

```sh
dev console --agent claude --record /tmp/rec.jsonl
# reproduce, then ctrl+q
dev console --replay /tmp/rec.jsonl | head -20
```

That prints the sizes the workload was given alongside the screen it
produced.

**If the sidecar looks wrong after an upgrade**, it will not be: the image
is stamped with the source it was built from, and a run whose binary
carries a different one rebuilds it and says so. The image is where egress
is actually enforced, so a stale one enforces a stale policy — which is
worth a rebuild you did not ask for.
