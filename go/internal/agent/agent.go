// Package agent defines coding agents that run inside the sandbox and the
// registry that loads them.
//
// An agent is data, not code: the same on-disk shape as v1's language
// plugins, so a user can add one without touching the tool.
package agent

import (
	"fmt"
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
	// Description is shown by `dev2 agent list`.
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
	// across runs.
	ConfigDir string `yaml:"config_dir"`
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
	// RuntimeImage is where the runtime is copied from. Pinned rather than
	// floating so the overlay is reproducible.
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

// Source returns where this definition was loaded from.
func (a *Agent) Source() string {
	if a.source == "" {
		return "built-in"
	}
	return a.source
}

// VolumeName is the named volume holding the agent's home directory. It is
// scoped per agent, not per project, so one login serves every project.
func (a *Agent) VolumeName() string { return "dev2-agent-" + a.Name }

// ImageTag is the overlay image tag for this agent on top of base.
func (a *Agent) ImageTag(projectImage string) string {
	base := strings.NewReplacer("/", "-", ":", "-").Replace(projectImage)
	v := a.Version
	if v == "" {
		v = "latest"
	}
	return fmt.Sprintf("dev2-agent-%s-%s:%s", a.Name, base, v)
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
		return nil, fmt.Errorf("agent: unknown agent %q (try `dev2 agent list`)", name)
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
