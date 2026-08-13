# Command reference

Every command takes `--verbose`, which prints the exact `docker`
invocation before running it. A security tool should never make you guess
what it ran.

`dev <command> --help` is authoritative; this page is the map.

---

## Starting out

| | |
|---|---|
| `dev` | in a project: guided — what state it is in, and what to do next |
| `dev interactive` | the same guided view, under its own name |
| `dev new LANG [dir]` | scaffold a new project from a language plugin |
| `dev doctor` | backend, sidecar image, disk, and this project's build context |
| `dev migrate` | what changes coming from the bash tool (`dev1`) |
| `dev completion install` | install completions for your shell |

`dev new python my-app` writes the plugin's files, not a Dockerfile: the
language template is rendered at build time and stays current, whereas a
copy in the project goes stale. Write one when you want to change it —
`dev build` prefers a project Dockerfile.

---

## Running things

| | |
|---|---|
| `dev run -c 'CMD'` | run one command in the sandbox |
| `dev run -- ARGS` | same, with an argv rather than a shell string |
| `dev run --image IMG` | run a stock image instead of building one — for things that are not projects |
| `dev shell` | interactive shell |
| `dev console` | full-screen: output, egress decisions, and blocking prompts |
| `dev build` | build the project image |
| `dev clean` | remove this project's containers, networks and sidecar |
| `dev clean --all` | the same for every project — frees ports a killed run left held |

Useful flags on `run` and `shell`:

| | |
|---|---|
| `--offline` | no network at all |
| `--network open` | no filtering |
| `--allow-host HOST` | add a destination for this run only |
| `--egress-prompt ask` | hold blocked requests while you decide |
| `--clone` | work in a private copy of the repository |
| `--clone-depth N` | copy only N commits of history — for large repositories |
| `--rebuild` | rebuild the image first |
| `--tty auto\|on\|off` | allocate a terminal |

`--allow-docker-socket` is on `run`, `shell` and `console`. It mounts the
docker socket for that run only and records nothing: it is root on the
docker host, and `dev accept` will not remember it without `--remember`.

`console` takes `--allow-host`, `--clone`, `--clone-depth`, `--in-place`,
`--allow-docker-socket`, `--rebuild` and `--agent`. It has no `--offline`,
`--network` or
`--egress-prompt`: it requires `network: allowlist` — with nothing filtered
there is nothing to decide — and holding blocked requests is what it does
rather than something to ask for. `--shell` takes the place of `--tty`.

---

## Agents

| | |
|---|---|
| `dev agent list` | available agents |
| `dev agent run NAME` | run one against this project |
| `dev agent run NAME --in-place` | …in your working tree instead of a private copy |
| `dev agent run NAME --clone` | …in a clone, when config turned that off |
| `dev agent run NAME -- "prompt"` | pass a prompt or flags through to the agent |
| `dev agent policy` | the egress policy a run would enforce, without running it |
| `dev agent logout NAME` | discard the stored login |
| `dev agent update NAME` | move the agent to the current published version, and say what moved |

Agents run at the untrusted level regardless of the project's trust: they
act on instructions from a model, so they do not inherit your decision to
trust this repository. They also work in a **private clone by default** —
an agent cannot reach your SSH keys, but it can edit git hooks, npm
scripts and Makefiles, which are things your host runs later.

---

## Egress and trust

| | |
|---|---|
| `dev accept` | review everything this project requests: settings and destinations |
| `dev allow HOST` | grant a destination for this project (`HOST:PORT` for a port other than 80/443) |
| `dev revoke HOST` | remove one |
| `dev grants` | every grant, and whether anything still uses it |
| `dev grants prune` | offer back the ones no recorded run reached |
| `dev config edit` | open this project's recorded configuration in `$EDITOR` |
| `dev config show` | the configuration a run would resolve |
| `dev history` | what past runs reached, and what was blocked |
| `dev history hosts` | every destination, most recent first |
| `dev policy` | the rules this machine enforces on everyone |
| `dev status` | what is running, under which policy (`--all` for other projects) |

Grants belong to the project, not to agents: a plain `dev run` consumes the
same ones. `allow`, `revoke`, `grants`, `config` and `accept` were once
`dev agent` subcommands and still answer there, hidden, so existing scripts
keep working.

Granting a non-HTTP port permits the destination; reaching it means going
through the sidecar, which offers HTTP CONNECT (`HTTP_PROXY`) and SOCKS5
(`ALL_PROXY`). A client reading either gets there on any granted port; one
that opens a socket itself still gets `network is unreachable`. `dev allow`
says which case you are in; [CONCEPTS.md](CONCEPTS.md) has the table.

`dev accept` shows settings and destinations together because the project
asked for both in one file. They remain separate decisions — accept one and
the other stays pending, and each is checked against its own policy rules —
but which of the two something is was never the reader's problem.

---

## The environment

| | |
|---|---|
| `dev tools search TERM` | what the image's package index has |
| `dev tools add TOOL` | add it for you |
| `dev tools add --shared TOOL` | add it to `.devenv.yaml` for the team |
| `dev tools list` / `remove` | list and remove |
| `dev pin` | pin base images to digests |
| `dev pin --update` | re-resolve them deliberately |
| `dev update` | move base image and packages to current, and report what moved |
| `dev scan` | vulnerabilities in the image you actually run |
| `dev scan --severity medium` | lower the bar it fails on (default: high) |
| `dev scan --include-unfixed` | also report findings with no fix available |
| `dev scan --report-only` | print findings but exit zero |
| `dev devcontainer` | export this environment as `.devcontainer/` for IDE users |

---

## Private clones

| | |
|---|---|
| `dev clone list` | every clone, its size, branch, and what work is still in it |
| `dev clone path` | this project's, for scripting |
| `dev clone diff` | what the clone has that the project does not |
| `dev clone apply` | bring those commits back, fast-forwarding when that needs no decision |
| `dev clone prune` | remove clones holding nothing (`--older-than`, `--force`, `--dry-run`) |
| `dev clone rm` | delete one — refuses while it holds work the project does not have |

---

## This machine

| | |
|---|---|
| `dev vm status` | VM and daemon state |
| `dev vm start` | start it |
| `dev version` | version, commit and build time |

---

## Network modes

| mode | meaning |
|---|---|
| `allowlist` (default) | language registries plus what you granted; everything else denied |
| `open` | no filtering |
| `none` | no network at all |

Set per project in `.devenv.yaml` (`network: open`) or per run
(`--network open`). A project asking for `open` needs your acceptance,
because it switches the filtering off.

---

## Exit codes

`dev run` and `dev shell` return the workload's own exit code, so they
compose in scripts. `dev scan` exits non-zero at or above its threshold so
CI can gate on it — and treats a scanner that could not run as a failure,
because "no findings" and "no scan" are different answers.
