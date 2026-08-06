# isolated-dev v2 (Go)

Work in progress. This tree is the Go rewrite described in
[docs/ROADMAP.md](../docs/ROADMAP.md). The bash v1 in `scripts/` remains the
working tool; nothing here replaces it yet.

Status: **M0 (skeleton)**.

## What exists

| | |
|---|---|
| `cmd/dev2` | entry point |
| `internal/runner` | the single choke point for external processes, with a fake for tests |
| `internal/config` | v1-compatible config loading and layering, grant extraction |
| `internal/backend` | engine abstraction; `orbstack` driver probes, `docker` driver lands in M3 |
| `internal/cli` | cobra command tree: `version`, `doctor` |

## Build and test

```sh
make check      # gofmt, vet, race tests
make build      # bin/dev2
./bin/dev2 doctor
```

Requires Go 1.26+.

## Design notes that matter early

**Everything external goes through `internal/runner`.** No package calls
`os/exec` directly. That is what makes commands mockable, printable under
`--verbose`, and free of the quoting bugs that dominated v1: a `Command`
carries an argument vector, never a string a shell will re-split.

**`doctor` diagnoses, it never repairs.** It will not start a VM. A
non-zero exit status from a probe is data, not an error, which is why
`runner` reports exit codes in `Result` and reserves `error` for "the
process could not be run at all".

**Config layering is explicit and traceable.** Defaults, global file,
project file, `DEV_*` environment. Pointer fields in `config.File`
distinguish "absent" from "set to false", so a project saying
`mount_ssh_keys: false` overrides a global `true`. Every resolved key
remembers its origin, which `doctor` prints.

**Grants are extracted, not inferred.** `Config.Asks()` returns only the
security-relevant subset (mounts, env passthrough, socket, ports). The
trust store will hash *that*, not the raw files, so routine edits do not
re-prompt — see ROADMAP 4.2.

**v1's never-implemented keys load but are reported.** `network_mode`,
`port_range` and friends produce a note explaining they do nothing, rather
than being silently dropped or silently honored.

## Not done yet

`run`, `shell`, `build`, agent mode, the egress proxy, the trust store.
Milestones and sequencing are in the roadmap.
