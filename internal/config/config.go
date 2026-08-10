// Package config loads and layers isolated-dev configuration.
//
// Layering, lowest priority first: built-in defaults, the global file
// (~/.dev-envs/config.yaml), the project file (.devenv.yaml), DEV_*
// environment variables, then command-line flags. Every resolved value
// remembers where it came from, because a security tool should be able to
// answer "why is this container getting that" without the user guessing.
package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mwing/isolated-dev/internal/trust"
)

// Origin identifies the layer a value came from.
type Origin int

const (
	OriginDefault Origin = iota
	OriginGlobal
	OriginProject
	OriginEnv
	OriginFlag
)

func (o Origin) String() string {
	switch o {
	case OriginGlobal:
		return "global config"
	case OriginProject:
		return "project config"
	case OriginEnv:
		return "environment"
	case OriginFlag:
		return "flag"
	default:
		return "default"
	}
}

// PassEnv is the v1 pass_env_vars block: shell-glob patterns plus explicit
// names. It carries over to v2 but is a grant, honored only once the user
// has accepted it when a project is the one asking.
type PassEnv struct {
	Patterns []string `yaml:"patterns"`
	Explicit []string `yaml:"explicit"`
}

// Empty reports whether the block requests nothing.
func (p PassEnv) Empty() bool { return len(p.Patterns) == 0 && len(p.Explicit) == 0 }

// Resolve turns the request into NAME=VALUE pairs read from environ.
//
// Only names the request actually matches are read, and an unset name is
// left out rather than passed as an empty value: a variable that exists and
// is empty means something different to a program than one that is absent,
// and inventing the first from the second is a lie the container cannot see
// through.
//
// Order is patterns first in environ order, then explicit names in the order
// asked for, so the result is stable across runs and a diff of two rendered
// invocations means something.
func (p PassEnv) Resolve(environ []string) []string {
	if p.Empty() {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	take := func(name, value string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name+"="+value)
	}

	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		for _, pattern := range p.Patterns {
			// A malformed pattern matches nothing rather than everything.
			if ok, err := filepath.Match(strings.TrimSpace(pattern), name); err == nil && ok {
				take(name, value)
				break
			}
		}
	}

	lookup := map[string]string{}
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok {
			lookup[name] = value
		}
	}
	for _, name := range p.Explicit {
		name = strings.TrimSpace(name)
		if value, ok := lookup[name]; ok && name != "" {
			take(name, value)
		}
	}
	return out
}

// File mirrors one YAML file. Pointer fields distinguish "absent" from
// "set to the zero value" so that a project file saying `mount_git_config:
// false` overrides a global `true` instead of being treated as unset.
type File struct {
	VMName            *string  `yaml:"vm_name"`
	DefaultTemplate   *string  `yaml:"default_template"`
	ContainerPrefix   *string  `yaml:"container_prefix"`
	AutoStartVM       *bool    `yaml:"auto_start_vm"`
	MemoryLimit       *string  `yaml:"memory_limit"`
	CPULimit          *string  `yaml:"cpu_limit"`
	CacheTTL          *int     `yaml:"cache_ttl"`
	CacheMaxSize      *int     `yaml:"cache_max_size"`
	MinDiskSpace      *int     `yaml:"min_disk_space"`
	MountGitConfig    *bool    `yaml:"mount_git_config"`
	MountDockerSocket *bool    `yaml:"mount_docker_socket"`
	ForwardPorts      *string  `yaml:"forward_ports"`
	PassEnvVars       *PassEnv `yaml:"pass_env_vars"`
	// Network is how a run reaches the outside: allowlist, open or none.
	Network *string `yaml:"network"`

	// Agents is the project's agent request (ROADMAP 4.2.1). It states
	// what the project needs; it grants nothing on its own.
	Agents map[string]trust.AgentConfig `yaml:"agents"`
	// Tools the project asks to have installed in its image. A request
	// like any other: packages are installed during a build, which runs
	// unfiltered, so a repository must not be able to add them silently.
	Tools []string `yaml:"tools"`
	// UpgradePackages rebuilds with the base image's own packages upgraded.
	// Recorded rather than applied once, so a later plain build does not
	// silently drop the upgrade and reintroduce what was just fixed.
	UpgradePackages *bool `yaml:"upgrade_packages,omitempty"`
	// Pins maps a base image reference to the digest it resolved to. This
	// narrows what a build fetches rather than widening it, so unlike the
	// other project-supplied values it needs no acceptance: the project
	// already chooses its own base images.
	Pins map[string]string `yaml:"pins"`
}

// Config is the resolved configuration.
type Config struct {
	VMName            string
	DefaultTemplate   string
	ContainerPrefix   string
	AutoStartVM       bool
	MemoryLimit       string
	CPULimit          string
	CacheTTL          int
	CacheMaxSize      int
	MinDiskSpace      int
	MountGitConfig    bool
	MountDockerSocket bool
	ForwardPorts      string
	PassEnvVars       PassEnv
	Network           string

	// Agents carries the project's agent request, unresolved. It is read
	// only from the project file: a global agent request would have no
	// project to speak for.
	Agents map[string]trust.AgentConfig
	// Tools the project requests, unresolved.
	Tools []string
	// Pins maps a base image reference to a digest.
	Pins map[string]string
	// UpgradePackages applies the base image's pending package upgrades.
	UpgradePackages bool

	origins map[string]Origin
	// Notes collects non-fatal observations made while loading: dead keys,
	// unknown keys, unreadable optional files.
	Notes []Note
}

// Note is a non-fatal loading observation.
type Note struct {
	File string
	Key  string
	Text string
}

func (n Note) String() string {
	loc := n.File
	if loc == "" {
		loc = "config"
	}
	if n.Key != "" {
		return fmt.Sprintf("%s: %s: %s", filepath.Base(loc), n.Key, n.Text)
	}
	return fmt.Sprintf("%s: %s", filepath.Base(loc), n.Text)
}

// Defaults returns the built-in configuration, matching v1's constants.sh.
func Defaults() Config {
	return Config{
		VMName:            "dev-vm-docker-host",
		DefaultTemplate:   "",
		ContainerPrefix:   "dev",
		AutoStartVM:       true,
		MemoryLimit:       "",
		CPULimit:          "",
		CacheTTL:          86400,
		CacheMaxSize:      100,
		MinDiskSpace:      5,
		MountGitConfig:    false,
		MountDockerSocket: false,
		ForwardPorts:      "",
		PassEnvVars:       PassEnv{},
		// Deny by default: a run that silently had the whole internet
		// would not keep the tool's promise. `network: open` restores v1
		// behavior per project.
		Network: "allowlist",
		origins: map[string]Origin{},
	}
}

// Origin reports which layer supplied a key. Keys use their YAML names.
func (c *Config) Origin(key string) Origin {
	if c.origins == nil {
		return OriginDefault
	}
	return c.origins[key]
}

// merge applies a file layer on top of c, recording provenance.
func (c *Config) merge(f File, o Origin) {
	if c.origins == nil {
		c.origins = map[string]Origin{}
	}
	if f.VMName != nil {
		c.VMName = *f.VMName
		c.origins["vm_name"] = o
	}
	if f.DefaultTemplate != nil {
		c.DefaultTemplate = *f.DefaultTemplate
		c.origins["default_template"] = o
	}
	if f.ContainerPrefix != nil {
		c.ContainerPrefix = *f.ContainerPrefix
		c.origins["container_prefix"] = o
	}
	if f.AutoStartVM != nil {
		c.AutoStartVM = *f.AutoStartVM
		c.origins["auto_start_vm"] = o
	}
	if f.MemoryLimit != nil {
		c.MemoryLimit = *f.MemoryLimit
		c.origins["memory_limit"] = o
	}
	if f.CPULimit != nil {
		c.CPULimit = *f.CPULimit
		c.origins["cpu_limit"] = o
	}
	if f.CacheTTL != nil {
		c.CacheTTL = *f.CacheTTL
		c.origins["cache_ttl"] = o
	}
	if f.CacheMaxSize != nil {
		c.CacheMaxSize = *f.CacheMaxSize
		c.origins["cache_max_size"] = o
	}
	if f.MinDiskSpace != nil {
		c.MinDiskSpace = *f.MinDiskSpace
		c.origins["min_disk_space"] = o
	}
	if f.MountGitConfig != nil {
		c.MountGitConfig = *f.MountGitConfig
		c.origins["mount_git_config"] = o
	}
	if f.MountDockerSocket != nil {
		c.MountDockerSocket = *f.MountDockerSocket
		c.origins["mount_docker_socket"] = o
	}
	if f.ForwardPorts != nil {
		c.ForwardPorts = *f.ForwardPorts
		c.origins["forward_ports"] = o
	}
	if f.PassEnvVars != nil {
		c.PassEnvVars = *f.PassEnvVars
		c.origins["pass_env_vars"] = o
	}
	if f.Network != nil {
		c.Network = *f.Network
		c.origins["network"] = o
	}
	// Only the project speaks for a project. A request in the global file
	// would be a policy without a subject.
	if len(f.Agents) > 0 && o == OriginProject {
		c.Agents = f.Agents
		c.origins["agents"] = o
	}
	if len(f.Tools) > 0 && o == OriginProject {
		c.Tools = f.Tools
		c.origins["tools"] = o
	}
	if len(f.Pins) > 0 && o == OriginProject {
		c.Pins = f.Pins
		c.origins["pins"] = o
	}
	if f.UpgradePackages != nil {
		c.UpgradePackages = *f.UpgradePackages
		c.origins["upgrade_packages"] = o
	}
}

// deadKeys are v1 keys that were parsed and validated but never affected
// behavior. They are accepted so old files still load, and reported so the
// user learns they do nothing. See ROADMAP section 6.
// retiredKeys are keys v1 honored that v2 deliberately does not, with what
// replaced them. They are distinct from deadKeys: a key that never worked is
// a cleanup, whereas one that worked and was withdrawn is a decision, and the
// user is owed the reasoning rather than a shrug.
var retiredKeys = map[string]string{
	"mount_ssh_keys": "not carried into v2: a private key inside the container " +
		"is an exfiltratable secret, and a container that can read your key can " +
		"copy it. Use ssh-agent forwarding instead (`dev agent run --allow-push`), " +
		"which lets the container sign without ever seeing the key and makes " +
		"revoking it a matter of killing the agent",
}

// RetiredNote returns the explanation for a retired key, empty if it is not
// one. Commands that report configuration use this rather than matching on
// note text.
func RetiredNote(key string) string { return retiredKeys[key] }

var deadKeys = map[string]string{
	"network_mode":             "never implemented in v1; use `network:` in v2 (see ROADMAP 4.3)",
	"auto_host_networking":     "never implemented in v1; no v2 equivalent",
	"port_range":               "never implemented in v1; ports are detected or listed explicitly",
	"enable_port_health_check": "never implemented in v1; no v2 equivalent",
	"port_health_timeout":      "never implemented in v1; no v2 equivalent",
}

// knownKeys are the keys that carry real behavior into v2.
var knownKeys = map[string]bool{
	"vm_name":             true,
	"default_template":    true,
	"container_prefix":    true,
	"auto_start_vm":       true,
	"memory_limit":        true,
	"cpu_limit":           true,
	"cache_ttl":           true,
	"cache_max_size":      true,
	"min_disk_space":      true,
	"mount_git_config":    true,
	"mount_docker_socket": true,
	"forward_ports":       true,
	"pass_env_vars":       true,
	"agents":              true,
	"network":             true,
	"tools":               true,
	"pins":                true,
	"upgrade_packages":    true,
}

// classify inspects raw top-level keys and returns notes for anything dead
// or unrecognized.
func classify(path string, keys []string) []Note {
	var notes []Note
	for _, k := range keys {
		switch {
		case knownKeys[k]:
		case retiredKeys[k] != "":
			notes = append(notes, Note{File: path, Key: k, Text: "ignored: " + retiredKeys[k]})
		case deadKeys[k] != "":
			notes = append(notes, Note{File: path, Key: k, Text: "ignored: " + deadKeys[k]})
		default:
			notes = append(notes, Note{File: path, Key: k, Text: "unknown key, ignored"})
		}
	}
	return notes
}

// SecurityAsks is the subset of configuration that grants a container
// access to something outside itself. The trust store hashes this set, not
// the raw files, so that routine edits never re-prompt (ROADMAP 4.2).
type SecurityAsks struct {
	MountGitConfig    bool
	MountDockerSocket bool
	PassEnvPatterns   []string
	PassEnvExplicit   []string
	ForwardPorts      string
}

// Asks extracts the security-relevant grant set.
func (c *Config) Asks() SecurityAsks {
	return SecurityAsks{
		MountGitConfig:    c.MountGitConfig,
		MountDockerSocket: c.MountDockerSocket,
		PassEnvPatterns:   append([]string(nil), c.PassEnvVars.Patterns...),
		PassEnvExplicit:   append([]string(nil), c.PassEnvVars.Explicit...),
		ForwardPorts:      c.ForwardPorts,
	}
}

// Empty reports whether the project asks for nothing beyond the sandbox.
func (a SecurityAsks) Empty() bool {
	return !a.MountGitConfig && !a.MountDockerSocket &&
		len(a.PassEnvPatterns) == 0 && len(a.PassEnvExplicit) == 0 && a.ForwardPorts == ""
}

// Describe renders the asks as human-readable lines for a trust prompt.
func (a SecurityAsks) Describe() []string {
	var out []string
	if a.MountGitConfig {
		out = append(out, "mount a filtered copy of ~/.gitconfig into the container")
	}
	if a.MountDockerSocket {
		out = append(out, "mount the docker socket (root on the docker host)")
	}
	if len(a.PassEnvPatterns) > 0 {
		out = append(out, "pass host env vars matching: "+strings.Join(a.PassEnvPatterns, ", "))
	}
	if len(a.PassEnvExplicit) > 0 {
		out = append(out, "pass host env vars: "+strings.Join(a.PassEnvExplicit, ", "))
	}
	if a.ForwardPorts != "" {
		out = append(out, "publish ports: "+a.ForwardPorts)
	}
	return out
}
