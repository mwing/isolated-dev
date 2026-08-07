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

	"github.com/mwing/isolated-dev/go/internal/trust"
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
// names. It carries over to v2 but is gated behind trust level.
type PassEnv struct {
	Patterns []string `yaml:"patterns"`
	Explicit []string `yaml:"explicit"`
}

// Empty reports whether the block requests nothing.
func (p PassEnv) Empty() bool { return len(p.Patterns) == 0 && len(p.Explicit) == 0 }

// File mirrors one YAML file. Pointer fields distinguish "absent" from
// "set to the zero value" so that a project file saying `mount_ssh_keys:
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
	MountSSHKeys      *bool    `yaml:"mount_ssh_keys"`
	MountGitConfig    *bool    `yaml:"mount_git_config"`
	MountDockerSocket *bool    `yaml:"mount_docker_socket"`
	ForwardPorts      *string  `yaml:"forward_ports"`
	PassEnvVars       *PassEnv `yaml:"pass_env_vars"`
	// Network is how a run reaches the outside: allowlist, open or none.
	Network *string `yaml:"network"`

	// Agents is the project's agent request (ROADMAP 4.2.1). It states
	// what the project needs; it grants nothing on its own.
	Agents map[string]trust.AgentConfig `yaml:"agents"`
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
	MountSSHKeys      bool
	MountGitConfig    bool
	MountDockerSocket bool
	ForwardPorts      string
	PassEnvVars       PassEnv
	Network           string

	// Agents carries the project's agent request, unresolved. It is read
	// only from the project file: a global agent request would have no
	// project to speak for.
	Agents map[string]trust.AgentConfig

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
		MountSSHKeys:      false,
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
	if f.MountSSHKeys != nil {
		c.MountSSHKeys = *f.MountSSHKeys
		c.origins["mount_ssh_keys"] = o
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
}

// deadKeys are v1 keys that were parsed and validated but never affected
// behavior. They are accepted so old files still load, and reported so the
// user learns they do nothing. See ROADMAP section 6.
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
	"mount_ssh_keys":      true,
	"mount_git_config":    true,
	"mount_docker_socket": true,
	"forward_ports":       true,
	"pass_env_vars":       true,
	"agents":              true,
	"network":             true,
}

// classify inspects raw top-level keys and returns notes for anything dead
// or unrecognized.
func classify(path string, keys []string) []Note {
	var notes []Note
	for _, k := range keys {
		switch {
		case knownKeys[k]:
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
	MountSSHKeys      bool
	MountGitConfig    bool
	MountDockerSocket bool
	PassEnvPatterns   []string
	PassEnvExplicit   []string
	ForwardPorts      string
}

// Asks extracts the security-relevant grant set.
func (c *Config) Asks() SecurityAsks {
	return SecurityAsks{
		MountSSHKeys:      c.MountSSHKeys,
		MountGitConfig:    c.MountGitConfig,
		MountDockerSocket: c.MountDockerSocket,
		PassEnvPatterns:   append([]string(nil), c.PassEnvVars.Patterns...),
		PassEnvExplicit:   append([]string(nil), c.PassEnvVars.Explicit...),
		ForwardPorts:      c.ForwardPorts,
	}
}

// Empty reports whether the project asks for nothing beyond the sandbox.
func (a SecurityAsks) Empty() bool {
	return !a.MountSSHKeys && !a.MountGitConfig && !a.MountDockerSocket &&
		len(a.PassEnvPatterns) == 0 && len(a.PassEnvExplicit) == 0 && a.ForwardPorts == ""
}

// Describe renders the asks as human-readable lines for a trust prompt.
func (a SecurityAsks) Describe() []string {
	var out []string
	if a.MountSSHKeys {
		out = append(out, "mount ~/.ssh into the container")
	}
	if a.MountGitConfig {
		out = append(out, "mount ~/.gitconfig into the container")
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
