// Package trust stores per-project agent configuration and the grants a
// user has made.
//
// Everything here lives under ~/.dev-envs, never inside a project tree.
// That placement is the point: configuration recorded in a repository is
// configuration the repository can award itself, so cloning a hostile
// project would widen its own egress before anyone read it (ROADMAP 4.2).
// Keeping the file outside means a project can be edited freely without
// ever changing what the sandbox permits.
//
// Layout:
//
//	~/.dev-envs/agents.yaml                    defaults for every project
//	~/.dev-envs/projects/<slug>-<hash>.yaml    one file per project
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// AgentConfig is what a user can set for an agent, either globally or for
// one project.
//
// Only fields that are safe to set without a confirmation belong here.
// Host mounts and environment passthrough are deliberately absent: those
// are grants that need the trust prompt, not preferences.
type AgentConfig struct {
	// AllowHosts adds egress destinations.
	AllowHosts []string `yaml:"allow_hosts,omitempty"`
	// Base overrides the image the agent overlay is built on, e.g. to give
	// the agent the project's own toolchain.
	Base string `yaml:"base,omitempty"`
	// Args replaces the agent's default arguments.
	Args []string `yaml:"args,omitempty"`
	// Memory and CPUs bound the container.
	Memory string `yaml:"memory,omitempty"`
	CPUs   string `yaml:"cpus,omitempty"`
}

// merge layers other on top of c. Later layers win for scalars; host lists
// accumulate, because a project should be able to add a destination
// without restating the global ones.
func (c *AgentConfig) merge(other AgentConfig) {
	c.AllowHosts = appendUnique(c.AllowHosts, other.AllowHosts...)
	if other.Base != "" {
		c.Base = other.Base
	}
	if len(other.Args) > 0 {
		c.Args = append([]string(nil), other.Args...)
	}
	if other.Memory != "" {
		c.Memory = other.Memory
	}
	if other.CPUs != "" {
		c.CPUs = other.CPUs
	}
}

// File is one configuration file: the global defaults or one project.
type File struct {
	// Project records which directory this file belongs to. It is for
	// humans reading the file; the filename is what actually keys it.
	Project string `yaml:"project,omitempty"`
	// Agents maps an agent name to its configuration. The key "default"
	// applies to every agent.
	Agents map[string]AgentConfig `yaml:"agents,omitempty"`

	path string
}

// Path returns where the file lives on disk.
func (f *File) Path() string { return f.path }

// Store resolves configuration across the global file and one project.
type Store struct {
	Root    string // ~/.dev-envs
	Global  *File
	Project *File
}

// GlobalPath is the file holding defaults for every project.
func GlobalPath(root string) string { return filepath.Join(root, "agents.yaml") }

// ProjectPath is the file for one project directory. The name carries a
// readable slug so the directory can be browsed, and a hash of the
// absolute path so two projects with the same basename never collide.
func ProjectPath(root, projectDir string) string {
	k := key(projectDir)
	sum := sha256.Sum256([]byte(k))
	return filepath.Join(root, "projects",
		fmt.Sprintf("%s-%s.yaml", slug(filepath.Base(k)), hex.EncodeToString(sum[:4])))
}

// Load reads the global file and the one for projectDir. Missing files are
// not an error; a malformed one is, because silently ignoring
// configuration the user wrote is how mistakes hide.
func Load(root, projectDir string) (*Store, error) {
	global, err := readFile(GlobalPath(root))
	if err != nil {
		return nil, err
	}
	project, err := readFile(ProjectPath(root, projectDir))
	if err != nil {
		return nil, err
	}
	if project.Project == "" {
		project.Project = key(projectDir)
	}
	return &Store{Root: root, Global: global, Project: project}, nil
}

func readFile(path string) (*File, error) {
	f := &File{Agents: map[string]AgentConfig{}, path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, fmt.Errorf("trust: reading %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, f); err != nil {
		return nil, fmt.Errorf("trust: parsing %s: %w", path, err)
	}
	if f.Agents == nil {
		f.Agents = map[string]AgentConfig{}
	}
	f.path = path
	return f, nil
}

// Save writes a file, creating its directory. Mode 0600: it records what
// the user authorized, so another account must not be able to rewrite it.
func (f *File) Save() error {
	if f.path == "" {
		return fmt.Errorf("trust: file has no path")
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	out, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("trust: encoding: %w", err)
	}
	return os.WriteFile(f.path, out, 0o600)
}

// Resolve returns the effective configuration for an agent: global
// defaults, global per-agent, project defaults, then project per-agent.
func (s *Store) Resolve(agentName string) AgentConfig {
	var out AgentConfig
	for _, layer := range []struct {
		file *File
		key  string
	}{
		{s.Global, "default"},
		{s.Global, agentName},
		{s.Project, "default"},
		{s.Project, agentName},
	} {
		if layer.file == nil {
			continue
		}
		if cfg, ok := layer.file.Agents[layer.key]; ok {
			out.merge(cfg)
		}
	}
	return out
}

// Grant adds destinations for an agent in the given scope and saves.
// Passing "default" as the agent name applies to every agent. It returns
// the entries that were actually new.
func (s *Store) Grant(scope *File, agentName string, hosts []string) ([]string, error) {
	if agentName == "" {
		agentName = "default"
	}
	cfg := scope.Agents[agentName]

	existing := map[string]bool{}
	for _, h := range cfg.AllowHosts {
		existing[h] = true
	}
	var added []string
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" || existing[h] {
			continue
		}
		existing[h] = true
		cfg.AllowHosts = append(cfg.AllowHosts, h)
		added = append(added, h)
	}
	sort.Strings(cfg.AllowHosts)

	if scope.Agents == nil {
		scope.Agents = map[string]AgentConfig{}
	}
	scope.Agents[agentName] = cfg
	return added, scope.Save()
}

// Revoke removes destinations for an agent in a scope and saves.
func (s *Store) Revoke(scope *File, agentName string, hosts []string) ([]string, error) {
	if agentName == "" {
		agentName = "default"
	}
	cfg, ok := scope.Agents[agentName]
	if !ok {
		return nil, nil
	}

	drop := map[string]bool{}
	for _, h := range hosts {
		drop[strings.TrimSpace(h)] = true
	}
	var kept, removed []string
	for _, h := range cfg.AllowHosts {
		if drop[h] {
			removed = append(removed, h)
			continue
		}
		kept = append(kept, h)
	}
	cfg.AllowHosts = kept
	scope.Agents[agentName] = cfg
	return removed, scope.Save()
}

// Template returns a commented starter file, so `edit` on a project with
// no configuration yet opens something that explains itself rather than an
// empty buffer.
//
// The agent's built-in allowlist is included as comments. Without it the
// user is editing blind: they cannot tell whether a destination is already
// permitted, so they either duplicate an entry or add one never needed.
func Template(projectDir, agentName string, builtinHosts []string) string {
	var defaults strings.Builder
	if len(builtinHosts) == 0 {
		defaults.WriteString("#   (none)\n")
	}
	for _, h := range builtinHosts {
		fmt.Fprintf(&defaults, "#   %s\n", h)
	}

	return fmt.Sprintf(`# Agent configuration for %s
#
# This file lives outside the project on purpose. Configuration inside a
# repository would be configuration the repository can grant itself, so a
# cloned project could widen its own egress before anyone read it.
#
# The "default" key applies to every agent; a name like "%s" applies to
# that one. Project values layer on top of ~/.dev-envs/agents.yaml.
#
# %s already permits these destinations, so there is no need to repeat them:
%s#
# Anything below is ADDED to that list.

project: %s

agents:
  default:
    allow_hosts: []

  %s:
    # Extra egress destinations. Ports 80 and 443 unless you write host:port.
    #   - internal.example.com
    #   - "*.corp.example.com"
    #   - db.example.com:5432
    allow_hosts: []

    # Build the agent on the project's own image instead of its default base.
    # base: my-project:latest

    # Resource limits.
    # memory: 4g
    # cpus: "2"

    # Replace the agent's default arguments.
    # args: []
`, projectDir, agentName, agentName, defaults.String(), projectDir, agentName)
}

// ProjectFiles lists every per-project configuration file under root.
func ProjectFiles(root string) ([]string, error) {
	dir := filepath.Join(root, "projects")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// ReadProjectFile loads one project file by path, for listing.
func ReadProjectFile(path string) (*File, error) { return readFile(path) }

// key normalizes a project path so the same directory always resolves to
// the same file regardless of symlinks or a trailing slash.
func key(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "project"
	}
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

func appendUnique(dst []string, add ...string) []string {
	seen := map[string]bool{}
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range add {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			dst = append(dst, v)
		}
	}
	return dst
}
