# Quickstart: adopting dev2 in a repository you already have

[README](README.md) covers what the tool is. This is the other thing:
you have a repository, possibly a large one, and you want it running in
the sandbox without a day of work.

The short version, for a single-language project:

```sh
cd ~/code/my-project
dev2 status      # what it detected, before anything runs
dev2 run -c 'npm test'
```

That is genuinely it. The rest of this page is the interesting case: a
repository that is not one language, not one service, and not small. The
worked example is a real monorepo — Node at the root, Python services in
subdirectories, its own production Dockerfiles, a 7 GB working tree.

---

## 1. Look before you run

```sh
dev2 status
```

```
Project:  acme-platform (~/code/acme-platform)
Image:    dev-img-acme-platform
Network:  allowlist (default)
  registries: registry.npmjs.org deb.debian.org security.debian.org
  granted:    api.acme.example
```

Two things are worth reading here. **What was detected** decides which
image gets built, and **what is allowed** decides what the first run can
reach — its language's package registries and nothing else.

`dev2 build` says more, and says where the Dockerfile came from:

```
Detected:   node 24 (from package.json), version from .nvmrc
Dockerfile: ~/.dev-envs/languages/node/Dockerfile.template (language template)
```

Detection reads the plugin data, so it explains itself: which marker file
it found, and where the version came from.

---

## 2. Expect the first run to be blocked, and read the block

The sandbox is closed by default, so a real project usually fails its
first run on something. That is the tool working:

```
Egress: blocked destinations this run:
  blocked: telemetry.example.com:443
Allow once:       --allow-host HOST
Allow from now:   dev2 agent allow HOST
Unrestricted:     --network open
```

Decide each one deliberately — that decision is the whole point:

```sh
dev2 agent allow api.mycompany.com     # persistent, this project
dev2 run --allow-host api.example.com  # just this run
```

If you want to be asked instead of told afterwards, the request can be
*held* while you decide:

```sh
dev2 run --egress-prompt ask -c 'npm ci'
dev2 console -- npm ci                 # same, in the full-screen view
```

Grants live in `~/.dev-envs`, never in the repository — configuration
inside a repository is configuration the repository can grant itself.

**When you are just exploring a large unfamiliar repo**, `--network open`
first and tighten later is a reasonable order. Run once open, then:

```sh
dev2 history hosts    # everything it actually reached
```

and grant that list rather than guessing. Nothing is recorded for an open
run's traffic, though — the proxy is what records — so do this the other
way around when the answer matters: run filtered, let it fail, read
`dev2 history`.

---

## 3. Put the shareable parts in `.devenv.yaml`

Two files, and the split matters:

| | |
|---|---|
| `<repo>/.devenv.yaml` | committed. What the project **requests**: tools, network mode, ports |
| `~/.dev-envs/projects/<slug>-<hash>.yaml` | private. What you **accepted**, plus your own grants |

A request is not a grant. A teammate who clones the repo is told what it
asks for and accepts once:

```sh
dev2 accept          # settings the project requests
dev2 agent accept    # egress destinations it requests
```

Add tools the whole team needs, rather than each person installing them:

```sh
dev2 tools search ripgrep     # what the image's package index has
dev2 tools add --shared ripgrep jq
```

`--shared` writes to `.devenv.yaml`. Without it the tool is yours alone.
Either way it is a declaration the image is rebuilt from, not a mutation
of a running container — so it survives a rebuild, and a teammate with
the same list gets the same result.

---

## 4. The monorepo question: one container or several?

A repository with `package.json` at the root and Python services beneath
it detects as Node, because that is what the root says. You have two
honest options.

### Option A — run dev2 per subproject (recommended)

`dev2` is directory-scoped. Every subdirectory is its own project with its
own image, its own grants and its own history:

```sh
cd backend  && dev2 status     # python 3.14 (from pyproject.toml)
cd ../frontend && dev2 status  # node
```

```
Project:  backend (~/code/acme-platform/backend)
  registries: pypi.org files.pythonhosted.org deb.debian.org security.debian.org
```

Each gets exactly the registries it needs — the Python service never gets
npm access, the Node app never gets PyPI. That is a real reduction, and
you get it for free by running the command one directory down.

The cost: builds are per subproject, and code that imports across package
boundaries will not see the other side, because only that directory is
mounted.

### Option B — one environment with the other language added

When subprojects are coupled and you need the whole tree at once, stay at
the root and add what is missing:

```sh
dev2 tools add --shared python3 python3-pip
```

Simpler to live with, and a bigger image with a wider allowlist. Prefer A
where the split is real; take B when the repo does not actually split.

---

## 5. Watch out: your Dockerfile is probably a *production* Dockerfile

`dev2 build` prefers, in order: the project's `Dockerfile`, then its
`devcontainer.json`, then the language template. That default is right for
a repo whose Dockerfile describes a dev environment — and wrong for the
common case, where it describes a deployment artifact:

```
Dockerfile: ~/code/acme-platform/backend/Dockerfile
```

If that file is a hardened multi-stage build ending in a slim runtime,
you get a container with your app and no shell tools, no test runner, no
git. Nothing broke; it is simply not a dev environment. Options:

- **Use the language template instead** — rename or move the production
  Dockerfile out of the root, or work from a subdirectory that has none.
- **Write a devcontainer.json** naming a dev image; dev2 reads it.
- **Add a dev Dockerfile** deliberately, with the tools you need.

The tell is a build that succeeds and a shell that has nothing in it.

---

## 6. Add a `.dockerignore` before you build a big repo

Docker sends the build context to the daemon before building anything. In
a real monorepo with history, worktrees and installed dependencies, that
is most of your disk:

```
Sending build context to Docker daemon  7.444GB
```

That is a measured number from a repo with no `.dockerignore`. Adding one
turns every rebuild from minutes into seconds:

```
.git
node_modules
.worktrees
**/node_modules
**/__pycache__
**/.venv
dist
build
```

Nothing in the sandbox needs them: your working tree is bind-mounted at
`/workspace` at run time, so files excluded from the *build* are still
there when you *run*. This is the single biggest speed win when adopting
dev2 in a large repository.

---

## 7. Let an agent loose without risking your tree

For anything unattended, put the run in a private clone:

```sh
dev2 agent run claude --clone
```

Your uncommitted work is carried in; whatever the agent does stays in
`~/.dev-envs/clones/<project>` and comes back through git when you want
it — see [USAGE.md](USAGE.md) for the full patch workflow. On a large
repository add `--clone-depth 1`, which copies one commit of history
instead of all of it. Worth making the habit early — it is the difference between reviewing
a diff and restoring from a stash.

---

## 8. Pin, update, scan

Once it runs, make it reproducible and keep it patched:

```sh
dev2 pin       # resolve base images to digests, recorded per project
dev2 scan      # vulnerabilities in the image you actually run
dev2 update    # move base images and packages forward, and report what moved
```

Pinning and updating are the same trade from opposite ends: a pin stops
the image changing under you, and it also stops security patches arriving
silently, so `update` exists to move it deliberately and tell you what
moved. `scan` defaults to findings that have a fix, because a list you
cannot act on is a list you stop reading.

Commit the pins. A teammate then builds the same image, not merely the
same tag.

---

## 9. Roll it out to the rest of the team

```sh
git add .devenv.yaml .dockerignore && git commit
```

For teammates who use an editor's dev containers rather than this tool:

```sh
dev2 devcontainer     # writes .devcontainer/, describing the same image
```

They get the same base, the same tools, the same unprivileged uid. They do
**not** get the egress filtering — that lives in a sidecar dev2 starts, and
an editor will not start it. The generated file says so.

---

## 10. Look back at what happened

```sh
dev2 history           # what each run reached, and what was blocked
dev2 history hosts     # every destination, most recent first
dev2 agent grants      # each grant, and whether anything still uses it
```

Worth doing a few weeks in. Grants accumulate for reasons that were true
at the time, and `dev2 agent grants prune` offers back the ones nothing
has reached since.

---

## When a port is "already allocated"

A run killed before it could tear itself down — a closed terminal, piped
output, `kill -9` — leaves its sidecar holding the ports it published. The
next project wanting one of those ports fails, and the daemon's message
names neither the culprit nor the fix. dev2 names both:

```
dev2: port 8000 is already published by dev2-other-proxy (other-project).
  Free it:  dev2 clean --all
```

---

## What dev2 will not do for you

Worth knowing before you plan around it:

- **It runs one container, not your stack.** A repo that needs a database
  and a queue alongside it is not what a project run gives you. Start the
  dependencies yourself, or use `network: open` and reach them.
- **Ports do not publish in allowlist mode** unless the sidecar forwards
  them; the run tells you which are published and names `--network open`.
- **Only the project directory is mounted.** No SSH keys, no
  `~/.gitconfig`, no host environment, no docker socket. When a workflow
  genuinely needs one of those, grant it explicitly — that is the design,
  not an obstacle to work around.

---

## Checklist

```sh
dev2 doctor                      # setup is sane
dev2 status                      # detection and allowlist look right
# add .dockerignore for a large repo
dev2 run -c '<your test command>'
dev2 agent allow <hosts it needed>
dev2 tools add --shared <tools the team needs>
dev2 pin && dev2 scan
dev2 devcontainer                # if teammates use an IDE
git add .devenv.yaml .dockerignore
```
