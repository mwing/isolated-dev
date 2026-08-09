# v1 feature disposition

v1 is a prototype. v2 is a redesign with stricter defaults and a layer v1
never had, so nothing carries over merely because it existed. Every v1
capability gets a decision: **keep**, **redesign**, **drop**, or **defer**.

This replaces the earlier framing of a parity checklist. Parity was the
wrong exit criterion: it would have forced v2 to reproduce behavior that
was never good, and made "we removed something that did not earn its
place" look like a regression.

The real exit criterion for M2 is that **v2 is the tool you reach for**,
with every v1 capability either present, deliberately redesigned, or
dropped with a reason written down.

## Core loop

| v1 | decision | state |
|---|---|---|
| `dev` (bare run) | keep | done — `dev2 run` |
| `dev shell` | keep | done |
| `dev build` | keep | done |
| `dev run -c '<cmd>'` | keep | done |
| `dev clean` | keep | done |
| `dev list` | redesign | done — `dev2 status` |
| language detection | redesign | done — reads plugin data, deterministic |
| language plugins | keep (format unchanged) | done — all 8 load |

## Security layer (new in v2, no v1 equivalent)

| capability | state |
|---|---|
| egress allowlist + proxy | done, agents and normal runs |
| filtering resolver | done |
| trust store, per-project grants | done |
| project request / user acceptance | done |
| agent sandbox | done |
| `--allow-push` via ssh-agent | done |

## Migration

`dev2 migrate` reports what changes for an existing v1 setup. v2 reads
v1's files, so nothing needs converting; what changes is what some
settings MEAN, and which of them never did anything. With `--write` it
strips the never-implemented keys, taking their comment headings with them
and keeping a timestamped backup.

## Redesigned

**`dev interactive` → `dev2 console`.** v1's interactive mode is a menu
that wraps commands. It is useful, and it is also the wrong shape for a
tool whose interesting events happen *while* something runs. Replaced by a
full-screen console: workload output in one pane, egress decisions in
another, and a blocked destination asked as a question while the request
waits. It is a separate command that delegates to the same code `run`
uses, so nothing becomes console-only.

Not yet: an interactive shell inside the console, which needs a pty per
pane. `dev2 shell` covers that case today.

**`dev list` → `dev2 status`.** v1 lists images. What a user needs is what
is *running*, in which project, under which policy, and what it has been
blocked from reaching. Done: it reports the resolved network mode with its
origin, the destinations allowed and why (language registries vs. granted),
running containers with their role, and — while a sidecar is up — what has
been blocked so far in that run.

**`dev env` → `dev2 vm`.** VM lifecycle, narrowed to what is actually
needed: `start` and `status`. v1 called `orb start` and assumed it worked;
v2 checks. Creating and destroying VMs is OrbStack's job, not this tool's.

**devcontainer.json is read.** A repository that already describes its
environment works unmodified: the image or Dockerfile it names and its
forwardPorts are used. What is not honored — containerEnv, mounts,
remoteUser, postCreateCommand — is reported on every build rather than
dropped silently, since those are grants in this model rather than
settings.

**`dev debug` → `dev2 doctor`.** Done. Diagnosis only; never repairs.

**`dev arch` → folded into `dev2 version`.** A whole command for one line
of output did not earn its place.

## Dropped

| v1 | why |
|---|---|
| `network_mode`, `auto_host_networking`, `port_range`, port health checks | never implemented in v1; v2 has real network modes instead |
| `bridge` / `host` network values | v2 rejects them rather than accepting a value it will not honor |
| unfiltered egress by default | replaced by allowlist-by-default; `network: open` is the opt-out |
| image `USER` deciding the runtime uid | the tool sets `--user`; a project Dockerfile must not be able to choose root |
| blanket `apparmor:unconfined` | applied to every v1 run for no stated reason |

## Deferred

| v1 | when |
|---|---|
| `dev new` / scaffolding | M2 late — no containers, so delegable to v1 in the meantime |
| `dev templates *` | M2 late — same |
| `dev devcontainer` (write) | M2 |

| `dev security scan` | M3, with real exit codes as a CI gate |
| `dev security check` | M3 |
| `dev disk`, `dev troubleshoot` | M3 — candidates for folding into `doctor` |

## Behavior changes worth knowing

**Detection reads the plugin data.** v1 declared `detection.files` in every
`language.yaml` and ignored it, keeping the markers in a bash case
statement along with version extraction and ports. A new plugin directory
did nothing until someone edited the script.

**Ambiguity resolves deterministically.** v1 took whichever language its
directory listing reached first. v2 takes the most matching markers, ties
broken by name.

**A broken plugin is reported**, not silently skipped.

**Ports do not publish in allowlist mode.** An internal network has no
gateway to publish through. The run says so and names `--network open`.
