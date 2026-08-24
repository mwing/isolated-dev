// Package agent defines coding agents that run inside the sandbox and the
// registry that loads them.
//
// An agent is data, not code: the same on-disk shape as v1's language
// plugins, so a user can add one without touching the tool.
package agent

import (
	"fmt"
	"github.com/mwing/isolated-dev/internal/container"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Agent describes one coding agent.
type Agent struct {
	// Name is the identifier used on the command line.
	Name string `yaml:"name"`
	// Description is shown by `dev agent list`.
	Description string `yaml:"description"`
	// Version pins what gets installed. "latest" is accepted but warned
	// about: an unpinned agent silently changes what runs in the sandbox
	// between two invocations.
	Version string `yaml:"version"`
	// Install is the shell fragment that installs the agent into the
	// overlay image.
	Install string `yaml:"install"`
	// Binary is the executable to launch.
	Binary string `yaml:"binary"`
	// Args are default arguments, e.g. an auto-approve flag that is only
	// safe because the sandbox is the boundary.
	Args []string `yaml:"args"`
	// AllowHosts is the agent's default egress allowlist.
	AllowHosts []string `yaml:"allow_hosts"`
	// ConfigDir is the path inside the container holding credentials and
	// settings. It is backed by a named volume so an OAuth login survives
	// across runs — and it is the only part of the home directory that
	// does, so it is also the whole of what one project can leave behind
	// for another.
	ConfigDir string `yaml:"config_dir"`
	// ConfigEnv is the variable this agent reads to find ConfigDir, when it
	// has one. Setting it keeps state the agent would otherwise scatter
	// beside its config directory — a `~/.claude.json`, an onboarding
	// marker — inside the one directory that persists, rather than in a
	// home that does not.
	ConfigEnv string `yaml:"config_env"`
	// Env are non-secret environment defaults for the agent, e.g. turning
	// off telemetry. They are applied before the sandbox's own variables,
	// so an agent definition cannot override the proxy settings that make
	// egress control work.
	Env []string `yaml:"env"`
	// AuthEnv names environment variables that carry an API key in `env`
	// auth mode. Values are never taken implicitly: the user must ask.
	AuthEnv []string `yaml:"auth_env"`
	// Base is the image to build the overlay on when no project image
	// exists.
	Base string `yaml:"base"`
	// Runtime is the language runtime the agent needs, installed into the
	// overlay rather than assumed present. Without this the agent only
	// works on bases that already carry it, which rules out running the
	// agent on the project's own image — and an agent that cannot run the
	// project's tests cannot check its own work.
	//
	// "node" is currently the only value; "" installs nothing.
	Runtime string `yaml:"runtime"`
	// RuntimeImage is where the runtime is copied from.
	//
	// It names a version, which is not the same as naming an image:
	// `node:22-bookworm-slim` is a mutable tag, and the overlay is only as
	// reproducible as what that tag points at today. The tool asks every
	// project to pin its bases for exactly this reason, so its own images
	// go through the same mechanism — `dev pin` resolves these too, and a
	// recorded digest is what makes two builds the same build.
	RuntimeImage string `yaml:"runtime_image"`

	// source records where the definition came from, for `agent list`.
	source string
}

// Validate reports whether the definition is usable.
func (a *Agent) Validate() error {
	if a.Name == "" {
		return fmt.Errorf("agent: definition has no name")
	}
	if strings.ContainsAny(a.Name, "/ \t") {
		return fmt.Errorf("agent %q: name must not contain spaces or slashes", a.Name)
	}
	if a.Binary == "" {
		return fmt.Errorf("agent %q: no binary", a.Name)
	}
	if a.ConfigDir == "" {
		return fmt.Errorf("agent %q: no config_dir; credentials would not persist", a.Name)
	}
	if !strings.HasPrefix(a.ConfigDir, "/") {
		return fmt.Errorf("agent %q: config_dir must be absolute", a.Name)
	}
	// Inside the home directory, and not the home directory itself.
	//
	// It used to be a path that was merely read; now it is where a named
	// volume is mounted, so `config_dir: /` or `/usr` would mount a volume
	// over the container's own filesystem. The migration out of the old
	// home volume also assumes this prefix. An agent definition is the
	// user's own file rather than a hostile one, which makes this a
	// mistake-catcher rather than a defence — but the mistake it catches is
	// an unbootable container.
	if !strings.HasPrefix(a.ConfigDir, HomePath+"/") || strings.Contains(a.ConfigDir, "..") {
		return fmt.Errorf("agent %q: config_dir must be a directory under %s, not %q",
			a.Name, HomePath, a.ConfigDir)
	}
	if len(a.AllowHosts) == 0 {
		// An agent with no allowlist cannot reach its own API and would
		// fail in a way that looks like a bug rather than a policy.
		return fmt.Errorf("agent %q: no allow_hosts; it could not reach its own API", a.Name)
	}
	return nil
}

// Pinned reports whether the version is fixed rather than floating.
func (a *Agent) Pinned() bool {
	v := strings.TrimSpace(a.Version)
	return v != "" && v != "latest"
}

// InstallCommand renders the install line with the version substituted.
//
// A definition writes `npm install -g pkg@{{VERSION}}` and the pin decides
// what that fetches. Without the placeholder the command is used as
// written, so a definition that pins by other means still works — but it
// is then responsible for its own reproducibility, and `dev agent list`
// has no way to know.
func (a *Agent) InstallCommand() string {
	v := strings.TrimSpace(a.Version)
	if v == "" {
		v = "latest"
	}
	return strings.ReplaceAll(a.Install, "{{VERSION}}", v)
}

// Package is the npm package a definition installs, when it can be read
// from the install command.
//
// Used to ask an image what it actually installed, which is how `dev agent
// update` learns the version to pin rather than guessing one.
func (a *Agent) Package() string {
	fields := strings.Fields(a.Install)
	for _, f := range fields {
		if strings.HasPrefix(f, "-") || f == "npm" || f == "install" {
			continue
		}
		// Strip whatever version spec is attached; the name is what an
		// image can be queried about.
		if i := strings.LastIndex(f, "@"); i > 0 {
			f = f[:i]
		}
		return f
	}
	return ""
}

// Source returns where this definition was loaded from.
func (a *Agent) Source() string {
	if a.source == "" {
		return "built-in"
	}
	return a.source
}

// VolumeName is the named volume holding the agent's configuration and
// credentials. It is scoped per agent, not per project, so one login serves
// every project.
//
// It covers the config directory and nothing else. It used to be the whole
// home directory, which made it a channel between projects: an agent
// working in project A could write a shell profile, a git config or an MCP
// setting that an agent in project B then read, with no route between them
// on the network and none intended here either. Everything outside the
// config directory now lives and dies with the container, as it already
// does for `dev run` and `dev shell`.
//
// What remains shared is the config directory itself, and that is inherent:
// one login means one place the credential lives, and the agents that keep
// a credential keep their settings beside it. See ConfigEnv.
//
// Named precisely, because "settings" undersells it. For the claude
// built-in that directory holds settings.json, which can declare hooks —
// commands the next run executes — as well as env and permissions. So an
// agent working in project A can still arrange for a command to run in
// project B's container. What this change removed is everything else: the
// shell profile, the git config, the caches, the MCP files outside the
// config directory. The remaining channel is the price of one login, and
// closing it means a config directory per project, which is a login per
// project. See BACKLOG B27.
func (a *Agent) VolumeName() string { return "dev-agent-" + a.Name + "-config" }

// homeVolumeName is what the volume was called while it held the whole home
// directory. Its config directory is carried into the new volume once.
func homeVolumeName(a *Agent) string { return "dev-agent-" + a.Name }

// Images are the upstream images this agent's overlay builds FROM, so the
// same pinning the tool asks of a project can be applied to its own
// images. The project image, when there is one, is not among them: it is
// built here and pinned by its own bases.
func (a *Agent) Images() []string {
	var out []string
	if a.Base != "" {
		out = append(out, a.Base)
	}
	if a.Runtime != "" {
		out = append(out, a.runtimeImage())
	}
	return out
}

// ImageTag is the overlay image tag for this agent on top of base.
func (a *Agent) ImageTag(projectImage string) string {
	base := strings.NewReplacer("/", "-", ":", "-").Replace(projectImage)
	v := a.Version
	if v == "" {
		v = "latest"
	}
	// The uid is in the tag for the same reason it is in the project
	// image's: the overlay creates an account with it, so an image built by
	// one person must not be reused by another. When the base is a project
	// image it already carries the uid, and repeating it is harmless.
	return fmt.Sprintf("dev-agent-%s-%s%s:%s", a.Name, base, uidSuffix(), v)
}

// Registry holds the known agents.
type Registry struct {
	agents map[string]*Agent
}

// NewRegistry returns a registry seeded with the built-in definitions.
func NewRegistry() *Registry {
	r := &Registry{agents: map[string]*Agent{}}
	for _, a := range builtins() {
		cp := a
		r.agents[a.Name] = &cp
	}
	return r
}

// LoadDir adds definitions from <dir>/<name>/agent.yaml, overriding
// built-ins of the same name so a user can pin or adjust one without
// forking the tool.
func (r *Registry) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("agent: reading %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "agent.yaml")
		raw, err := os.ReadFile(path)
		if err != nil {
			if errorIsNotExist(err) {
				continue
			}
			return fmt.Errorf("agent: reading %s: %w", path, err)
		}
		var a Agent
		if err := yaml.Unmarshal(raw, &a); err != nil {
			return fmt.Errorf("agent: parsing %s: %w", path, err)
		}
		if a.Name == "" {
			a.Name = e.Name()
		}
		if err := a.Validate(); err != nil {
			return fmt.Errorf("agent: %s: %w", path, err)
		}
		a.source = path
		r.agents[a.Name] = &a
	}
	return nil
}

func errorIsNotExist(err error) bool {
	return os.IsNotExist(err) || strings.Contains(err.Error(), string(fs.ErrNotExist.Error()))
}

// Get returns an agent by name.
func (r *Registry) Get(name string) (*Agent, error) {
	a, ok := r.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent: unknown agent %q (try `dev agent list`)", name)
	}
	return a, nil
}

// List returns every agent, sorted by name.
func (r *Registry) List() []*Agent {
	out := make([]*Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// uidSuffix distinguishes agent images built for different host uids, and
// is empty for the historical default so the common case reads unchanged.
func uidSuffix() string {
	uid := container.HostUID()
	if uid == container.FallbackUID {
		return ""
	}
	return fmt.Sprintf("-u%d", uid)
}
