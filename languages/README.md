# Language plugins

A plugin tells `dev` how to recognize a kind of project, what image to
build for it, and which hosts that project's toolchain needs. It is data,
not code: adding a language means adding a directory here, and nothing in
`internal/` has to learn its name.

That is the property worth protecting. In the bash tool this format was
largely decorative — `detection.files` was declared in every
`language.yaml` and then ignored, because the real markers lived in a
`case` statement — so dropping in a plugin did nothing until someone edited
a script. Everything below is read.

## Where a plugin lives

```
languages/<name>/                in this repository
~/.dev-envs/languages/<name>/    where dev reads it from
```

`dev` only ever reads the installed copy, so editing a plugin here has no
effect until it is copied across. A plugin is machine-level configuration,
like `~/.dev-envs/policy.yaml`: it comes from you, never from the
repository being sandboxed, which is why it is allowed to widen egress (see
[`registries`](#on-registries)) without asking.

## What ships

`language.yaml` is authoritative; this table is a map.

| Plugin | markers | versions | ports |
|---|---|---|---|
| `golang` | `go.mod`, `main.go` | 1.21, 1.22, 1.25 | 8080, 8000, 3000 |
| `java` | `pom.xml`, `build.gradle`, `*.java` | 17, 21 | 8080 |
| `kotlin` | `build.gradle.kts`, `settings.gradle.kts` | 1.9, 2.0 | 8080, 8000 |
| `node` | `package.json` | 20, 22, 25 | 3000, 8080, 5000, 4000 |
| `php` | `composer.json`, `index.php`, `*.php` | 8.2, 8.3 | 8080 |
| `python` | `requirements.txt`, `pyproject.toml`, `setup.py`, `Pipfile` | 3.11, 3.12, 3.13, 3.14 | 8000, 5000 |
| `rust` | `Cargo.toml`, `src/main.rs` | 1.75, 1.82, 1.91 | 8000 |
| `ubuntu` | `*.sh`, `Makefile` | 20.04, 22.04, 24.04 | 8080 |

## How the tool uses one

1. **Detection.** Every plugin's `detection.files` are matched against the
   project directory. The plugin with the most matching markers wins, ties
   broken by name so the answer never depends on filesystem order.
   `dev build` prints what it concluded and from which markers.
2. **Version.** `detection.version_files` are tried in order against the
   project; the first that yields something wins. With no marker the
   plugin's newest declared version is used.
3. **Image.** The plugin's Dockerfile template is rendered with that
   version and built. A `Dockerfile` in the project wins over the template,
   and a `devcontainer.json` wins over the template but not over a
   Dockerfile.
4. **Egress.** The plugin's `registries` are permitted in `allowlist` mode
   for every run of a detected project, without a grant. `dev status` lists
   them.
5. **Scaffolding.** `dev new <name> [dir]` copies the plugin's declared
   scaffolding files into a directory.

## `language.yaml`

Every field the loader reads, and nothing it does not:

```yaml
name: mylang                  # optional; defaults to the directory name
display_name: My Language     # shown in `dev new` completions
versions: ["1.0", "2.0"]      # ORDER MATTERS: the last one is the default

detection:
  files: [mylang.toml, "*.mylang"]   # any one present identifies the language
  version_files:                     # optional, tried in order
    - file: .mylang-version          # no `extract`: the first line is the version
    - file: mylang.toml
      extract: 'version\s*=\s*"([0-9]+\.[0-9]+)"'   # capture group 1

registries:                   # egress this language's toolchain needs
  - packages.example.com

ports: [8080]                 # forwarded unless the project sets forward_ports

files:
  dockerfile: Dockerfile.template     # optional; this is the default
  scaffolding: [mylang.toml, src/main.mylang, .gitignore]
```

The details that decide whether a plugin works:

- **`detection.files`** entries containing `*`, `?` or `[` are globbed;
  everything else is an exact name. A marker must be a **file** — naming a
  directory, such as `src/main/kotlin`, never matches.
- **`detection.version_files`** with an `extract` use Go's regexp syntax
  (RE2: no backreferences, no lookahead) and take capture group 1, or the
  whole match if the pattern has no group. Without `extract` the file's
  first line is taken. A leading `v` and any of `^~>=<` are stripped, so
  `>=3.11` and `v20.1.0` become `3.11` and `20.1.0`. Single-quote patterns
  containing backslashes, since YAML has escaping of its own.
- **`versions`** are the tags the Dockerfile template can render.
  `dev new --version` refuses one that is not declared, rather than
  producing an image tag that does not exist and a failure much later. A
  version read out of a project's own marker file is used as it stands,
  declared or not: the project is the authority on what it needs, and this
  list is not a whitelist.
- **`ports`** are a convenience: they are forwarded unless the project
  overrides them. In `allowlist` mode the sidecar publishes them, because a
  workload on an internal network cannot publish its own, and the run says
  which it published.
- **`files.scaffolding`** may name nested paths. A file declared here and
  not shipped is reported as missing rather than invented: content this tool
  made up would be a surprise attributed to the plugin.
- A plugin with **no `detection.files`**, or with an invalid `extract`
  regexp, is skipped with a warning on stderr — `⚠  language plugin: …` —
  rather than failing every command that touches languages. A plugin that
  can never be detected is a plugin that silently does nothing, so it is
  reported as a load failure rather than accepted.

### Template placeholders

Two, in the Dockerfile template and in scaffolding files alike:

| placeholder | value |
|---|---|
| `{{VERSION}}` | the resolved language version, e.g. `3.13` |
| `{{PROJECT_NAME}}` | the project directory name, sanitized |

Nothing else is substituted. The bash tool also had `{{GO_VERSION}}`; it is
gone, and a template still using it ships that literal text into someone's
new project. `TestShippedPluginsUseOnlySubstitutedPlaceholders` in
`internal/scaffold` fails on any plugin here that does.

## Adding a language

```sh
mkdir -p languages/mylang
# write language.yaml, Dockerfile.template and the scaffolding files
cp -r languages/mylang ~/.dev-envs/languages/     # dev reads the installed copy

mkdir /tmp/try && cd /tmp/try
touch mylang.toml
dev status                     # does it detect, at the version you expect?
dev build                      # does the template build?
dev run -c '<something>'       # does the toolchain reach what it needs?
```

`dev new mylang /tmp/fresh` exercises the scaffolding path on an empty
directory. It writes the plugin's declared files and **not** a Dockerfile:
the template is rendered at build time and stays current, whereas a copy in
the project goes stale. `dev devcontainer` is the separate command for IDE
users.

### On `registries`

This is the one field with a security consequence. Its hosts are reachable
from every run of a project detected as this language, with no grant and no
prompt, and any host that accepts writes is a place data can go. Keep the
list to what a dependency install genuinely needs, and prefer exact names
to wildcards. A destination one project needs belongs in that project's
grants (`dev allow HOST`), not here.

## Writing the Dockerfile template

The shipped templates are the reference. The conventions the tool relies on:

- Create a user with **UID 1000** and switch to it. Runs pass
  `--user 1000:1000`, so an image whose files belong to root leaves the
  workload unable to write its own workspace.
- `WORKDIR /workspace`, which is where the project is mounted.
- Assume no network at run time beyond the allowlist. **Builds are not
  filtered** — `docker build` runs on the daemon, outside the sandbox
  network — so anything a template downloads bypasses the allowlist
  entirely. That is a reason to keep templates thin, not a licence to move
  work into them.
- End with a shell (`CMD ["bash"]`). `dev run -c` and `dev shell` both go
  through it.

## License

Language plugins follow the same MIT license as the rest of the project.
