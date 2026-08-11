# Work queue: closing the gap between the docs and the binary

Source: an external review of commit `e792f77`, kept at `review.txt` in the
repository root. Every finding below marked **verified** was re-checked
against the source before it was written down; the line numbers were
accurate at that commit and may have drifted by a few lines since.

**The theme, and the reason the order matters:** the documentation
describes a stricter tool than the binary is. Several security promises
made by the lower layers are not wired up in the CLI glue. Nothing here is
a redesign — most of it is small wiring — but until it is done, sentences
in the README are false, which costs more than a missing feature.

---

## How to work in this repository

```sh
go build ./...                       # must stay clean
gofmt -l cmd internal                # must print nothing
go vet ./...
go test ./... -race -count=1
golangci-lint run                    # must report 0 issues
```

`gofmt` is scoped to `cmd/` and `internal/` deliberately: `languages/` holds
scaffolding templates full of `{{PROJECT_NAME}}` placeholders, and the
golang plugin is its own nested module.

`make` may not be available in every shell; the go commands above are the
real gate. `make proxy-image` builds the sidecar into the OrbStack VM and
needs a working `orb`.

### Ground rules

1. **Write the failing test first.** Every P0 below survived a green
   338-test suite. A fix without a test that fails before it is not a fix,
   it is a coincidence.
2. **Verify the change landed.** If you edit with a script or a patch,
   re-read the file afterwards. Silent no-op patches — where an anchor
   string had shifted — have caused real bugs in this repository twice,
   and both times the code compiled and the tests passed.
3. **A security sentence in the docs must be true in the code, or the
   sentence changes.** Both are acceptable outcomes; leaving them
   disagreeing is not.
4. **Comments say why, not what.** Match the surrounding style: the
   existing comments explain the decision and the failure it prevents.
5. Commit messages here are prose that explains the reasoning and states
   what was verified. Look at `git log` for the register.
6. Use `git -c commit.gpgsign=false commit`.

### Where things are

```
cmd/dev/            the CLI entry point
cmd/dev-proxy/      the egress sidecar binary
internal/cli/       cobra commands — the glue layer, where the P0 bugs are
internal/netpolicy/ proxy, DNS resolver, allowlist, sidecar lifecycle
internal/agent/     agent definitions and run spec
internal/trust/     the store: grants, acceptances, tools
internal/policy/    the machine policy that binds the user
internal/project/   detection, image naming, RunSpec assembly
docs/               guides and design
```

---

# P0 — the security claim is not currently true

## T1. `--safe` never reaches the agent  **[verified] [DONE]**

`internal/cli/agent.go:154` declares `safe`, `:249` binds the flag, and
nothing ever assigns it into `agent.Options.Safe`. So
`agent/run.go` always appends the agent's auto-approve arguments, and
`dev agent run claude --safe` runs with `--dangerously-skip-permissions`.

Why it is first: this is the flag a cautious person reaches for, and it
silently does the opposite of what it says. `TestSafeModeDropsAutoApproveArgs`
passes because it calls `Spec()` directly and never goes through the CLI —
a green test that actively misleads.

**Do:** assign `Safe: safe` where the other flags are assembled into
`agent.Options` (search for `opts := agent.Options{`).

**Acceptance:** a CLI-level test — build the command tree with the test
harness in `internal/cli/cli_test.go`, run
`agent run claude --safe --dry-run`, and assert the rendered docker
invocation does **not** contain `--dangerously-skip-permissions`. Assert
the inverse without `--safe`. The existing unit test stays.

## T2. Accepted egress hosts do not apply to plain runs  **[verified] [DONE]**

`store.AcceptedRequest(...)` is included when building the allowlist in
`internal/cli/agent.go:353` and `internal/cli/console.go:369`, and is
absent from `internal/cli/workspace.go` (around line 362), which is what
`dev run` and `dev shell` use. The allowlist there is
`p.Registries() + store.Resolve("default").AllowHosts + o.ExtraHosts`.

So a user runs `dev agent accept`, accepts the project's requested hosts,
and then `dev run` is still blocked by them. Two commands that should agree
silently disagree.

**Do:** include the accepted hosts in the workspace allowlist, keyed to the
`"default"` agent, matching what `console.go` does.

**Acceptance:** a test that seeds a trust store with an accepted host and
asserts it appears in the allowlist handed to the sidecar for `dev run`.
Check `console.go` and `workspace.go` produce the same set for the same
inputs — the divergence is the actual bug.

## T3. Machine policy is not enforced on most routes in  **[verified] [DONE]**

Done in both directions: the routes were closed, and the sentence was made
specific rather than left to be re-read generously. `runAgent` now loads the
policy before anything is built or started and applies the checks the
workspace path applies — the mode an agent run actually uses (`allowlist`,
which is not optional there), `forbid` against the project's own asks,
`require.*` applied after the project and the stored file so nothing lower
relaxes them, and the registry rule on the base the overlay is built on.

Every mutation goes through `CheckHost` — `--allow-host` on `run`, `shell`,
`console`, `agent run` and `agent policy`, `dev agent accept`, the blocking
prompt and the console's dialog. Two routes turned up that the finding did
not name: `dev accept`, which recorded consent to a forbidden setting and
left the refusal to the next run, and `dev tools add`, which writes the
request and the acceptance in one step.

The assembled allowlist is then filtered once more on the way to the
sidecar (`permittedHosts`), because two things cannot be refused at the
door: a grant recorded before the rule existed, and `dev agent config edit`,
which hands the user an editor. A destination dropped there is named on
stderr. `edit` also warns at save time, since by then the file is written.

ROADMAP M4 now lists the routes concretely and says which of them refuse
and which drop, and USE-CASES no longer claims a project's forbidden
request is refused rather than offered while `dev accept` was offering it.

`internal/policy` documents itself as enforced at "every route in", and
ROADMAP M4 repeats it. In fact `loadPolicy` is called from `allow.go`,
`consent.go`, `scan.go`, `policy.go` and `workspace.go` — and **not once**
in `agent.go`. So `network_modes`, `forbid` and `require.*` do not apply to
agent runs, which are the runs with the most reason to be constrained.

`policy.CheckHost` is likewise only called from `dev agent allow`. It is
not applied to `--allow-host` on any command, to the interactive
"allow this project from now on" prompt (`internal/cli/prompt.go`, which
also persists the grant), or to `dev agent accept`. A `deny_hosts` entry
can be walked around by any of those.

**Do:** load the policy in `runAgent` and apply the same checks the
workspace path applies. Route every allowlist mutation through
`CheckHost` — the per-run `--allow-host` flags, the prompt's grant, and the
acceptance path.

**Acceptance:** a test per route: with a policy denying `evil.example`,
assert each of `--allow-host evil.example`, the interactive grant, and
`dev agent accept` refuses. Then either the sentence "every route in" is
true, or it gets edited.

## T4. Consent that grants nothing  **[verified] [DONE]**

Resolved per key rather than in one direction: `mount_git_config`,
`mount_docker_socket` and `pass_env_vars` are implemented behind the
acceptance; `mount_ssh_keys` is retired, because ROADMAP 4.1 promises "socket
only, never key files" and implementing the mount would have meant weakening
a security sentence to satisfy a key nobody had honored. ROADMAP 4.1 now
states what the code does about trust levels, which is that there are none.


`mount_ssh_keys`, `mount_git_config`, `mount_docker_socket` and
`pass_env_vars` are parsed in `internal/config/config.go`, described to the
user in the consent prompt, gated behind `dev accept`, and reported by
`doctor` and `migrate`. They have **no consumer** in `internal/project` or
`internal/container` — grep confirms it. The container never receives them.

A prompt that authorizes nothing is worse than no prompt: it teaches people
to click through the one that matters. ROADMAP 4.1's trust-level table
(untrusted / trusted / privileged) likewise has no representation in code.

**Do:** pick one. Either implement them in `project.RunSpec` behind the
acceptance the user already gives, or remove them from the config schema,
the consent prompt, `doctor` and `migrate`. Implementing is the better
outcome — "we can expose anything the user needs" is the stated intent —
but a half-honored grant is the one thing that must not remain.

**Acceptance:** if implemented, a RunSpec test showing the mount appears
only when accepted, and does not appear otherwise. If removed, a test that
the config parser rejects or ignores the key with a clear note, and no doc
mentions it.

## T5. An existing network is trusted without checking it  **[verified] [DONE]**

`internal/container/engine.go:167` (`NetworkCreate`) treats
`already exists` as success without verifying the existing network's
`Internal` flag. The entire egress model rests on that one flag: a leftover
network created without `--internal` — by a crashed run, an older version,
or a name collision — gives the workload a default route and fully open
egress, while the CLI cheerfully reports `network: allowlist`.

This is the worst outcome the tool can produce: no error, no warning, and a
security property silently absent.

**Do:** on the already-exists path, inspect the network
(`docker network inspect -f '{{.Internal}}' <name>`) and fail loudly if an
internal network was requested and the existing one is not. Name the fix in
the error (`dev clean --all`).

**Acceptance:** a test with the fake runner where inspect reports
`false` and creation is requested internal — assert the run refuses.

## T6. SNI is claimed and not implemented  **[verified] [DONE]**

Implemented rather than documented away: the gap was real, and the sentence
was worth keeping true. `internal/netpolicy/clienthello.go` parses the
opening TLS record and refuses a session whose SNI is not the CONNECT
authority, with a fatal `access_denied` alert so the block reaches the
developer at the failed request rather than only the log.

The record is inspected as it streams past rather than held: buffering until
a complete ClientHello had arrived would deadlock any protocol whose server
speaks first, and would put the check in the latency path of every
connection. The verdict therefore lands a moment after the bytes it is
about, which costs the far end one closed socket and reveals nothing the
CONNECT had not. No interception: nothing is decrypted or rewritten, and a
test asserts the client still sees the upstream's own certificate.

Three cases pass deliberately and are documented in ROADMAP 4.3: a
connection that is not TLS (ssh on 22, which `--allow-push` grants), a
ClientHello with no SNI (it reaches the dialled host's default, already
approved), and a matching name. A handshake record that cannot be read is
refused, since fragmenting the ClientHello is how such a check is evaded.

`docs/ROADMAP.md:236` says the proxy "allowlists by CONNECT target / SNI
hostname"; `:369` says "SNI-allowlisting". No ClientHello or ServerName
parsing exists outside test fixtures. Only the CONNECT authority is
checked, and then bytes are relayed untouched.

The gap is real: for a CDN-heavy allowlist, CONNECT to an allowed front and
send a different SNI inside the session. SNI checking is exactly what would
close it.

**Do:** either parse the ClientHello at CONNECT time and require the SNI to
match the authorized host, or correct both roadmap sentences and any
sidecar comment that repeats them. If you implement it: refusing on
mismatch must not break the no-interception property — you are reading the
first record, not terminating the session.

**Acceptance:** if implemented, a test driving a real handshake where the
SNI differs from the CONNECT host and asserting refusal, alongside the
existing test proving no interception. If documented instead, a grep for
"SNI" that returns only accurate statements.

---

# P1 — it breaks normal use, or a clean install

## T7. `IdleTimeout` is a hard 10-minute lifetime  **[verified] [DONE]**

A real idle timer (`internal/netpolicy/idle.go`): both copies feed it, so
the deadline moves whenever bytes move in either direction. Activity on
one side counts for both — a download is silent on the way up and a long
poll is silent on the way down, and treating either as idleness would cut
off the connection it describes. Refreshed at most every tenth of the
window, since a syscall per packet is a cost nobody asked for.

`internal/netpolicy/proxy.go`: `touch()` sets a deadline on both
connections once, before the two `io.Copy` goroutines start, and is never
called again. `io.Copy` does not refresh it. So every relayed connection
dies after 10 minutes regardless of activity — long agent sessions, large
`git clone`s, `ssh` through the ProxyCommand. The field comment says it
"bounds a relayed connection's inactivity", which is not what it does.

**Do:** make it a real idle timer — wrap the copy so the deadline is
extended on every successful read/write — or rename the field to
`MaxLifetime`, raise the default, and say so. A real idle timer is the
right answer; the comment already describes it.

**Acceptance:** a test where data flows past the timeout in small
increments and the connection survives, plus one where silence past the
timeout closes it.

## T8. The sidecar cannot be built from a release binary  **[verified]**

`ensureProxyImage` (`internal/cli/agent.go:569`) errors with "build it with
`make proxy-image` from the repository root". The Makefile target tars the
repository and pipes it to `docker build` inside the VM. A user who
installs a released binary has no repository, so the mandatory component
for every allowlist run cannot be produced. The flagship feature does not
work from a clean install.

It also accepts any locally-tagged `dev-proxy:latest` with no verification,
so a stale sidecar is used silently.

**Do:** embed `Dockerfile.proxy` (and what it needs) in the binary with
`go:embed` and build from that, or publish the image and pull it. Record
and check a digest so a stale or foreign image is noticed. Keep the
existing error as the fallback when the build is impossible.

**Acceptance:** building the sidecar succeeds from a directory that is not
the repository.

## T9. Exit codes are lost twice  **[verified] [DONE]**

Both halves fixed, and a third found on the way. The two `Wait` switches in
`internal/runner` are now one `exitStatusOf`: a failure that is not an
`*exec.ExitError` carries no status at all, so reporting zero for it read as
success — that was the PTY bug, and the non-PTY path had the same shape.
The third: `exitErr.ExitCode()` is -1 for a signalled process, which is
neither a status to propagate nor a number anyone recognizes, so it becomes
128+N the way a shell reports it.

The CLI carries the number rather than the sentence: `exitStatus` in
`root.go` is an error the root unwraps into the process status, used by
`run`, `shell`, `console` and `agent run`. The message is still printed —
the code is for the machine, the sentence is for the person. Only a
workload's own status propagates; a tool failure stays exit 1, or a script
would read the wrong thing.

- `internal/runner/runner.go` (`runPTY`): the `default` branch of the error
  switch returns `Result{}, nil`, so a PTY workload that failed for any
  reason other than `*exec.ExitError` — killed by a signal, for instance —
  reads as exit 0.
- `internal/cli/root.go:139`: every error becomes exit 1, so
  `dev run -- make test` cannot propagate the test's real status.

**Do:** return a non-zero result (or an error) from the `default` branch,
and give the CLI a typed exit error the root can unwrap and use as the
process status. `streamRunWith` already formats `exited with status %d` —
carry the number rather than the sentence.

**Acceptance:** `dev run -c 'exit 7'` exits 7. A unit test for the runner's
default branch.

## T10. Forward relay teardown can block forever  **[verified] [DONE]**

Close now interrupts the relays rather than waiting for them to end on
their own, and the wait after that is bounded (`closeWait`, 5s). One quiet
forwarded connection used to hold teardown open for as long as it stayed
open, which is indefinitely.

A departure from the brief: no idle deadline on the relayed copies. A
published port is not egress — a hot-reload websocket, an SSE stream, a
debugger session are all legitimately silent for long stretches, and a
blanket deadline would kill them and be blamed on the framework. Teardown
was the actual bug, and closing the tracked connections fixes it without
putting a clock on a connection nobody asked to time.

The comment at the dial was corrected rather than the code: this end of a
forward is raw TCP, so a refused upstream has no vocabulary but an
immediate close. It never told the client anything and could not.

Writing the test found a race the change had introduced and the old code
had latent: `wg.Add` in the accept loop against `wg.Wait` in Close. Both
now happen under one lock with a `closing` flag, so a connection accepted
a moment before teardown is either fully tracked or never started.

`internal/netpolicy/forward.go`: the copy has no deadlines and `Close()`
does `wg.Wait()` (`:131`), so one idle forwarded connection blocks teardown
indefinitely. The comment near `:92` claims a refused connection is
reported to the client; the code drops it silently.

**Do:** deadlines on the relayed copies, and a bounded wait in `Close()`.
Fix the comment or the behavior it describes.

---

# P2 — daily friction

## T11. The interactive grant ignores the port it asked about  **[verified] [DONE]**

Both grants — `[o]` for the run and `[p]` for the project file — now name
the destination the question named. The destination is built with
`net.JoinHostPort` rather than a format string, because it is granted and
recorded now rather than only printed, and `::1:8080` is not something
`netpolicy.Parse` can read back.

The refusal is the one place that still names the bare host, deliberately:
a deny rule matches the name whatever port follows it, so the refusal is as
broad as the rule that caused it — and it is the key `Proxy.Refuse` records
under, so a narrower one would not be found and the next attempt would wait
out its timeout again.

`internal/cli/prompt.go:106` grants `e.Host` — the bare hostname — after
asking about `host:port`. A bare grant opens 80/443 only. So approving
`evil.com:8080` opens two ports you were never asked about, while the
request you approved keeps failing until it times out. Wrong in both
directions, and invisible from the prompt.

## T12. Four of nine `dev agent` subcommands are not about agents  **[DONE]**

`dev allow`, `dev revoke`, `dev grants` and `dev config` are the canonical
spellings; the four under `dev agent` are hidden aliases that still work and
say once, on stderr, which name to use now. Not through cobra's `Deprecated`
field: that prints through the command's out writer, which is stdout here,
and `dev agent config path` exists to be read by a script.

`dev agent accept` stays where it is. The root already has an `accept` for
the settings a project requests, and this one is for the egress it requests
— two different decisions, and merging them would blur the line the trust
model rests on. It looks inconsistent beside the four that moved, and it is
the right split.

Completions needed no change: they are generated from the tree, so the new
root commands appear and the hidden aliases do not.

`allow`, `revoke`, `grants` and `config` apply to plain runs too — a
blocked `dev run` prints `dev agent allow HOST` (`workspace.go`), and plain
runs consume those grants.

## T13. Small correctness  **[all verified] [DONE]**

All four, with one departure. The VM messages: creating a VM is OrbStack's
job (PARITY), so a missing VM now names `orb create`, and a stopped one
names `dev vm start` rather than the bare `orb start` it used to print —
`dev vm start` is the one that checks the result. `dev env up docker-host`
appears nowhere outside `v1/`.

`--egress-notify` parses like `--egress-prompt`. `--dry-run` is checked
before the clone is prepared and says what it would have prepared.

`--allow-push` reads `git remote get-url origin` and allows that host on
the port the remote names. The departure: an `https` origin is refused
rather than translated to port 443. Forwarding an ssh-agent for one grants
a socket the push can never use, and the tool deliberately carries no token
into the container, so there is nothing an https push could authenticate
with. Refusing says that; granting 443 would have looked like it worked.

- `internal/backend/orbstack/orbstack.go:74` tells the user to run
  `dev env up docker-host`. There is no `dev env` command. It should name
  `dev vm start`.
- `--egress-notify` (`agent.go:251`) accepts any string; anything not
  exactly `off` means on, so `--egress-notify of` silently does the wrong
  thing. Validate it like `--egress-prompt` is validated.
- `--dry-run` is checked at `agent.go:421`, after the clone is created at
  `:199`. A dry run should touch nothing on disk.
- `--allow-push` hardcodes `github.com:22` (`agent.go:320`) and warns using
  that name regardless of the actual remote. Derive the host from
  `git remote get-url origin`.

## T14. `languages/README.md` is v1 documentation  **[verified] [DONE]**

Rewritten against what `internal/langs` actually reads, which turned up two
things the old page had been covering for:

- `languages/golang/go.mod` used `{{GO_VERSION}}`, a placeholder only v1's
  bash substituted. `dev new golang` had been writing `go {{GO_VERSION}}`
  into every new project — a go.mod that does not build.
  `TestShippedPluginsUseOnlySubstitutedPlaceholders` now fails on any
  plugin using a placeholder nothing substitutes.
- `TestRealPluginsFromTheRepoLoad` pointed one directory too high, left over
  from when the module lived in a subdirectory. A missing plugin directory
  loads as an empty set rather than an error, so the guard had been skipping
  itself silently — the one outcome a guard must not have.

The page also states what the plugin format does NOT do, since that is where
it was wrong before: a directory never matches as a detection marker, a
version read from a project's own marker file is used whether declared or
not, and `registries` widen egress for every run of a detected project with
no grant and no prompt.

It documented `dev list`, `dev new python-3.13 --init` and `--devcontainer`,
none of which exist in this tool.

---

# P3 — hardening, and the reason these bugs were possible

## T15. Integration tests through the CLI glue  **(do this early, not last)**

**[harness DONE]** — `internal/cli/glue_test.go` drives the real command
tree against the fake runner. The harness in `cli_test.go` gained
`dockerKey` (keys rendered the way the backend spawns them, quoting
included), `dockerArgs`/`dockerRuns` (argument vectors, not rendered
lines), `workloadRun` and `sidecarAllow`. Standard input moved into `Env`
because `isTerminal(os.Stdin)` decided whether prompting blocks and `go
test` supplies /dev/null, a character device that reads as a terminal.
Remaining: the per-fix coverage below, and the real-daemon CI tier.

Every P0 above survived a green suite because the tests stop below the glue
layer. `internal/cli/cli_test.go` has a harness with a fake runner; it is
underused.

**Do:** end-to-end tests through the command tree for `runWorkspace` and
`runAgent`, asserting on the rendered docker invocations and the allowlist
handed to the sidecar. Cover: `--safe`, accepted hosts, policy denial,
network mode, mounts. Then a Linux CI tier against a real docker daemon —
the backend interface exists to make that possible and currently has no
tests at all.

Without this, fixing T1–T5 means the next one hides the same way.

## T19. An agent run does not get the project's registries  **[done — fixed while the queue was paused]**

Found by dogfooding this queue: the agent could not fetch a Go module.
`proxy.golang.org` serves large zips as a signed redirect to
`storage.googleapis.com`, which nothing had granted — because `runAgent`
never resolved the project at all, so `p.Registries()` could not reach it.
`dev run` had them all along.

This was T2 in the opposite direction: two paths that should agree, that
could drift. Fixed the same way — `agentEgress` in `internal/cli/workspace.go`,
used by both `runAgent` and `prepareAgent`, with a CLI-level test that seeds
a language plugin fixture and asserts the registries reach the sidecar's
`--allow`. The test was confirmed to fail without the fix.

Nothing to do; listed so it is not repeated.

## T21. A stale sidecar enforces an older policy, silently  **[found while verifying T6]**

`ensureProxyImage` accepts any locally-tagged `dev-proxy:latest`. The
enforcement lives in that image, not in the `dev` binary, so a rebuilt
binary and a stale image means the tool reports policies it is not applying.

This is not hypothetical. Verifying T6 end to end, a mismatched SNI
completed its handshake — because the sidecar image predated the SNI check
by five commits. Nothing said so: `dev doctor` reported the image present,
the run reported itself filtered, and the bypass the commit had just closed
was wide open. It took a rebuild to notice, and only because the test was
expected to fail.

**Do:** fold into T8. Stamp the same version into both binaries at build
time, have the sidecar report it in the line `waitReady` already parses,
and refuse — not warn — when they disagree. A warning here is a line
scrolling past above a run that then proceeds under the wrong policy.

**Acceptance:** a run against a deliberately older sidecar image fails with
a message naming the skew and the rebuild.

## T22. Nothing reaps orphans in a container  **[done — fixed here]**

The workload ran as pid 1, which inherits every orphaned process and reaps
none of them. With `PidsLimit` at 512 that is not untidiness but a hard
failure: the agent working through this queue accumulated 458 zombie `git`
processes from `internal/clone`'s tests, after which `fork` failed and the
suite reported errors that looked like test failures and were not.

Fixed by adding `--init` to the hardened spec, so `docker-init` is pid 1 and
reaps. Verified in a real run (`/proc/1/comm` is `docker-init`) and held by
a unit test on the rendered argv.

Worth noting for anything else that reads this: it was invisible for months
because it only bites a workload that spawns many short-lived children, and
the symptom names the wrong culprit.

## T20. The gitconfig filter is a denylist wearing an allowlist's label

`internal/cli/hostaccess.go` filters the host gitconfig by dropping known
dangerous keys (`credential`, `insteadof`, `signingkey`, `sshcommand` …).
The file it writes says "Only identity is carried over", and the commit
that added it says the same. That is not what a denylist does: everything
not named is passed, including settings that run programs —
`core.fsmonitor`, `filter.*.clean/smudge`, `diff.*.textconv`,
`protocol.*.allow`, `alias.*`.

The sandbox contains the consequences, so this is not urgent, and the
dangerous-for-the-host cases (token helpers, remote rewriting) are already
denied. But the stated intent and the mechanism disagree, which is the same
class of problem this whole queue is about.

**Do:** invert it — carry `user.*`, `init.defaultBranch`, `core.autocrlf`
and the handful of genuinely identity-shaped keys, drop everything else.
Keep the header comment honest either way.

**Acceptance:** a test feeding a gitconfig containing `core.fsmonitor` and
an alias, asserting neither survives, alongside the existing tests that
identity does.

## T16. Allowlist hardening

- **Public-suffix guard** (`internal/netpolicy/allowlist.go:79`): `*.co.uk`,
  `*.github.io`, `*.s3.amazonaws.com` all parse and match at any depth. A
  grant of `*.github.io` is all of GitHub Pages. Refuse or warn loudly on a
  wildcard at or near a public suffix, and surface it in the grant prompt.
- **DNS exfiltration under wildcards** (`resolver.go:77`): for an allowed
  name the full QNAME is forwarded verbatim, so with `*.example.com`
  granted, `<payload>.example.com` carries data out and is logged as
  `allow`. The A/AAAA-only restriction stops TXT tunnels, not label-encoded
  exfil, which needs no answer. Length and label-count heuristics are the
  cheap mitigation.
- **SSRF filter on the dial target** (`proxy.go:110`): no RFC1918,
  loopback, link-local or `169.254.169.254` filter. An allowlisted name
  whose DNS an attacker influences becomes a route to the docker gateway,
  the host, or the metadata endpoint. Block those unless a rule names the
  address — the literal-IP rule in `allowlist.go` is the precedent.
- **IDNA / punycode normalization** in the grant prompt, which is the one
  place a user widens policy by reading a hostname.

## T17. Bound what an attacker can grow

The sidecar and the host-side watcher key maps by destination. A retrying
client can emit thousands of distinct denials. Cap the maps and the log
volume.

## T18. `http.Transport` per plain-HTTP request  **[DONE]**

One transport, built on first use because `Dial` is injected after
construction, with a bounded idle pool. The test asserts the observable
consequence rather than the identity of the object: three requests to one
host now dial once.

`proxy.go:264` constructs one per request and never closes it, leaking fds
and goroutines under a loop. Reuse one.

---

## Suggested order

1. **T15 first, partially** — enough harness to write CLI-level tests.
2. **T1, T2, T4, T5, T3** — the wiring, cheapest to most invasive. Each
   with a test that fails before the fix.
3. **T6** — decide implement-or-document, and make the docs true.
4. **T9, T7, T8** — the ones that break real use.
5. **T11, T13, T14** — small and user-visible.
6. **T12** — command move, once the rest is stable.
7. **T16–T18** — hardening.

After each group: `go test ./... -race`, `golangci-lint run`, and a commit
that says what was verified rather than what was intended.

## What not to do

- Do not widen a default to make a test pass. The friction is the product;
  the roadmap's "deliberately out of scope" list is load-bearing.
- Do not add a feature while the promise/behavior gaps are open. The docs
  are already ahead of the binary, which is the problem being fixed.
- Do not delete a security sentence from the docs to close a gap without
  saying so in the commit message. Both directions are honest; a quiet edit
  is not.

---

# Success criteria

## For the queue as a whole

Done means all six of these are true at once:

1. **Every security sentence in `README.md`, `docs/CONCEPTS.md` and
   `docs/ROADMAP.md` is true of the binary**, or has been edited with the
   change explained in the commit. Grep for the load-bearing ones: "every
   route in", "SNI", "closed by default", "no route out", "accepted".
2. **`go test ./... -race -count=1` passes, `gofmt -l cmd internal` is
   empty, `go vet ./...` is clean, `golangci-lint run` reports 0 issues.**
3. **Each fixed bug has a test that fails on the commit before the fix.**
   Check this literally: `git stash` the source change, run the new test,
   watch it fail, `git stash pop`. A test written after a fix that never
   saw it fail proves nothing.
4. **The CLI-level suite exists** (T15) and covers `--safe`, accepted
   hosts, policy denial, network mode and mounts through the command tree,
   not through the spec builders.
5. **A clean install works**: from a directory that is not this repository,
   with only the installed binary, `dev doctor` reports ready and
   `dev run -c true` completes in a scratch project.
6. **`dev run -c 'exit 7'` exits 7.**

## Per task

| task | done when |
|---|---|
| T1 | `dev agent run X --safe --dry-run` shows no `--dangerously-skip-permissions`; without `--safe` it does |
| T2 | the allowlist for `dev run` equals the one for `dev console` given identical config and store |
| T3 | done — with a policy denying a host, all of `--allow-host`, the interactive grant, and `dev agent accept` refuse it; agent runs honor `network_modes`, `forbid`, `require.*` |
| T4 | an accepted mount appears in the RunSpec and an unaccepted one does not — or the key is gone from config, consent, doctor, migrate and the docs |
| T5 | a non-internal pre-existing network makes the run fail loudly, naming `dev clean --all` |
| T6 | done — a handshake whose SNI differs from the CONNECT host is refused, and no interception is added to do it |
| T7 | done — a connection transferring past the timeout survives; a silent one is closed |
| T8 | the sidecar image builds with no repository present |
| T9 | done — `dev run -c 'exit 7'` exits 7; a PTY workload killed by a signal does not report success |
| T10 | done — `Close()` returns within a bounded time with an idle forwarded connection open |
| T11 | done — approving `host:8080` grants 8080 and does not grant 80/443 |
| T12 | done — `dev allow` works, `dev agent allow` still works as a hidden alias, and no doc or hint names the old path as primary |
| T13 | done — each of the four is corrected, and `--egress-notify of` is rejected rather than silently accepted |
| T14 | done — `languages/README.md` names no command or flag that does not exist |
| T15 | the CLI suite fails when any of T1–T5 is reverted |
| T16 | `*.co.uk` is refused or warned about; an over-long QNAME under a wildcard grant is refused; a dial to 169.254.169.254 is refused unless a rule names it |
| T17 | a flood of distinct denials does not grow memory without bound |
| T18 | done — one transport is reused across plain-HTTP requests |
| T19 | done — an agent run's `--allow` contains the project's language registries |
| T20 | a gitconfig with `core.fsmonitor` or an alias yields neither in the container |
| T21 | a run against an older sidecar image fails, naming the skew |
| T22 | done — `/proc/1/comm` in a run is an init, not the workload |

---

# Testing instructions

## The three tiers, and which to use

**Unit** — pure logic: `internal/netpolicy` (allowlist matching, resolver
decisions), `internal/policy`, `internal/project`, `internal/wizard`. Fast,
no daemon. Most existing tests live here, and this tier is where the P0
bugs were *invisible*, so a unit test alone does not close any of T1–T5.

**CLI level with a fake runner** — the tier that was missing. Drives the
real cobra command tree and asserts on the exact `docker` invocations that
would have run. Every P0 needs one of these.

**Against a real daemon** — manual for now, and the CI tier T15 proposes.
Needed for anything about actual network behavior: the internal-network
check (T5), SNI (T6), timeouts (T7).

## Writing a CLI-level test

The harness is in `internal/cli/cli_test.go`. It gives a temp home and
project, a `runner.Fake`, and captured output:

```go
h := newHarness(t)
h.readyBackend()                    // fake runner looks like a healthy VM
h.writeProject(t, "network: open")  // optional .devenv.yaml
h.fake.Response[proxyInspect] = runner.Result{Stdout: "[{}]\n"}

if err := h.run(t, "agent", "run", "claude", "--safe", "--dry-run"); err != nil {
    t.Fatal(err)
}

for _, line := range h.fake.Lines() {
    if strings.Contains(line, "--dangerously-skip-permissions") {
        t.Fatalf("--safe did not drop auto-approve:\n%s", line)
    }
}
```

Three things to know about `runner.Fake`:

- `Response` keys match a **prefix of the whole rendered command line**,
  including the backend's wrapper: `orb -m dev-vm-docker-host sudo docker
  volume inspect X`, not `docker volume inspect X`. A key that does not
  match falls through to `Default`, whose zero value is exit 0 — which
  silently means "success" and has misled test-writing here before.
- `Calls` / `Lines()` record everything, in order. Assert on them rather
  than on stdout where you can: stdout is prose that will be reworded,
  the argv is the behavior.
- `Err[key]` makes a matching call fail, for the error paths.

## Manual verification, and what to expect

The commands worth running by hand, in a scratch project
(`dev new python /tmp/scratch && cd /tmp/scratch`):

```sh
# T1
dev agent run claude --safe --dry-run | grep -c dangerously-skip   # want 0

# T2
dev agent accept                    # accept a requested host
dev run --tty off -c 'curl -sS -o /dev/null -w "%{http_code}\n" https://<that host>/'

# T5
orb -m dev-vm-docker-host sudo docker network create dev-scratch-internal
dev run --tty off -c true           # want a loud failure, not a silent open network

# T7
dev run --tty off -c 'curl -sS -o /dev/null https://<large file>'   # >10 minutes

# T9
dev run -c 'exit 7'; echo $?        # want 7

# T11
dev run --egress-prompt ask --tty off -c 'curl -sS https://example.com:8080/'
# approve with [p], then:
dev grants                          # want example.com:8080, not example.com
```

Clean up after network experiments: `dev clean --all`.

## Regression guard for the docs

The gap this queue closes is a documentation gap as much as a code one.
Before finishing, grep the docs for each claim and confirm it:

```sh
grep -rn "every route in\|SNI\|no route out\|closed by default" README.md docs/
```

Each hit should correspond to something a test asserts.

---

# Recommended order, all tasks

Twelve steps. Each is a commit; the groups are natural stopping points.

| # | task | why here |
|---|---|---|
| 1 | **T15 (partial)** | Build just enough CLI-level harness to express "this flag reaches the container". Everything after this becomes testable, and without it the P0 fixes cannot be proven. |
| 2 | **T1** `--safe` | One line plus a test. The most dangerous bug and the cheapest fix — do it while the harness is fresh. |
| 3 | **T2** accepted hosts | Same shape as T1: a missing term in an expression. Fixing it next means the allowlist assembly is correct before policy is layered on it in T3. |
| 4 | **T5** internal network | Independent, self-contained, and the worst failure mode. Does not need T3. |
| 5 | **T4** mounts | Decide implement-or-delete. Bigger than T1–T2 and touches the trust model, so it comes after the mechanical fixes but before policy, since policy may need to check mounts too. |
| 6 | **T3** policy everywhere | Now that the allowlist and mounts are correct, put the machine policy in front of all of them. Largest P0 and the one that touches the most call sites. |
| 7 | **T6** SNI | Decide and act. Doing it after T3 means the sentence you write about enforcement is about a code path that is finally true. |
| 8 | **T9, T13** | Small, mechanical, user-visible. A palate cleanser after the P0 block, and T9 unblocks CI use. |
| 9 | **T7, T10, T18** | The proxy's connection handling, all in `internal/netpolicy`, all needing the same real-daemon verification — one context, one sitting. |
| 10 | **T8 + T21** distribution and version skew | Needs the sidecar to be otherwise finished, since embedding it fixes its build path. Before any release, and before asking anyone else to install this. |
| 11 | **T11, T12, T14** | User-facing surface: the port bug, the command move, the stale plugin docs. T12 last of the three because it renames things the other two mention. |
| 12 | **T20, T15 (rest), T16, T17** | Finish the integration tier, then hardening. Hardening last is deliberate: each item widens or narrows matching behavior, and you want the full test suite underneath before touching allowlist semantics. |

Two ordering rules worth stating, because they are why this is not simply
P0→P3:

- **T15 comes first, not last.** The queue exists because a green suite
  hid five real bugs. Fixing them without closing that hole means the sixth
  hides the same way.
- **Hardening comes after the surface work.** T16 changes what an existing
  grant matches; doing it before T11 and T12 would mean re-testing grant
  behavior twice.
