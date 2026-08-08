package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// Paths locates the v1-compatible on-disk layout. v2 reads the same files
// so a user can run both binaries during the transition.
type Paths struct {
	Home       string // ~/.dev-envs
	Global     string // ~/.dev-envs/config.yaml
	Languages  string // ~/.dev-envs/languages
	Templates  string // ~/.dev-envs/templates
	Cache      string // ~/.dev-envs/cache
	Trust      string // ~/.dev-envs/trust.yaml (v2 only)
	ProjectDir string
	Project    string // <project>/.devenv.yaml
}

// DefaultPaths resolves the layout for a home directory and project dir.
func DefaultPaths(home, projectDir string) Paths {
	root := filepath.Join(home, ".dev-envs")
	return Paths{
		Home:       root,
		Global:     filepath.Join(root, "config.yaml"),
		Languages:  filepath.Join(root, "languages"),
		Templates:  filepath.Join(root, "templates"),
		Cache:      filepath.Join(root, "cache"),
		Trust:      filepath.Join(root, "trust.yaml"),
		ProjectDir: projectDir,
		Project:    filepath.Join(projectDir, ".devenv.yaml"),
	}
}

// Discover resolves paths from the environment: $HOME and the working
// directory.
func Discover() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("config: locating home directory: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return Paths{}, fmt.Errorf("config: locating working directory: %w", err)
	}
	return DefaultPaths(home, wd), nil
}

// Load layers defaults, the global file, the project file and DEV_*
// environment variables. Missing files are not an error; malformed ones
// are, because silently ignoring a config the user wrote is how v1's
// parsers hid mistakes.
func Load(p Paths, env []string) (Config, error) {
	cfg := Defaults()

	for _, layer := range []struct {
		path   string
		origin Origin
	}{
		{p.Global, OriginGlobal},
		{p.Project, OriginProject},
	} {
		if layer.path == "" {
			continue
		}
		file, notes, err := readFile(layer.path)
		if err != nil {
			return cfg, err
		}
		cfg.Notes = append(cfg.Notes, notes...)
		cfg.merge(file, layer.origin)
	}

	envFile, envNotes := fromEnv(env)
	cfg.Notes = append(cfg.Notes, envNotes...)
	cfg.merge(envFile, OriginEnv)

	return cfg, nil
}

// readFile parses one YAML file. It returns a zero File if the path does
// not exist.
func readFile(path string) (File, []Note, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return File{}, nil, nil
		}
		return File{}, nil, fmt.Errorf("config: reading %s: %w", path, err)
	}

	// Two passes: the map pass reports dead and unknown keys, the struct
	// pass produces typed values.
	var top map[string]any
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return File{}, nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	notes := classify(path, keys)

	var f File
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return File{}, notes, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	return f, notes, nil
}

// fromEnv builds a File layer from DEV_* variables.
func fromEnv(env []string) (File, []Note) {
	var f File
	var notes []Note

	get := func(name string) (string, bool) {
		prefix := name + "="
		for i := len(env) - 1; i >= 0; i-- {
			if strings.HasPrefix(env[i], prefix) {
				return env[i][len(prefix):], true
			}
		}
		return "", false
	}

	str := func(name string) *string {
		v, ok := get(name)
		if !ok {
			return nil
		}
		return &v
	}
	boolean := func(name string) *bool {
		v, ok := get(name)
		if !ok {
			return nil
		}
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			notes = append(notes, Note{File: "environment", Key: name,
				Text: fmt.Sprintf("not a boolean (%q), ignored", v)})
			return nil
		}
		return &b
	}
	number := func(name string) *int {
		v, ok := get(name)
		if !ok {
			return nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			notes = append(notes, Note{File: "environment", Key: name,
				Text: fmt.Sprintf("not a number (%q), ignored", v)})
			return nil
		}
		return &n
	}

	f.VMName = str("DEV_VM_NAME")
	f.DefaultTemplate = str("DEV_DEFAULT_TEMPLATE")
	f.ContainerPrefix = str("DEV_CONTAINER_PREFIX")
	f.AutoStartVM = boolean("DEV_AUTO_START_VM")
	f.MemoryLimit = str("DEV_MEMORY_LIMIT")
	f.CPULimit = str("DEV_CPU_LIMIT")
	f.CacheTTL = number("DEV_CACHE_TTL")
	f.CacheMaxSize = number("DEV_CACHE_MAX_SIZE")
	f.MinDiskSpace = number("DEV_MIN_DISK_SPACE")
	f.MountSSHKeys = boolean("DEV_MOUNT_SSH_KEYS")
	f.MountGitConfig = boolean("DEV_MOUNT_GIT_CONFIG")
	f.MountDockerSocket = boolean("DEV_MOUNT_DOCKER_SOCKET")
	f.ForwardPorts = str("DEV_FORWARD_PORTS")
	f.Network = str("DEV_NETWORK")

	for name := range deadEnvKeys {
		if _, ok := get(name); ok {
			notes = append(notes, Note{File: "environment", Key: name,
				Text: "ignored: " + deadKeys[deadEnvKeys[name]]})
		}
	}
	return f, notes
}

var deadEnvKeys = map[string]string{
	"DEV_NETWORK_MODE":             "network_mode",
	"DEV_AUTO_HOST_NETWORKING":     "auto_host_networking",
	"DEV_PORT_RANGE":               "port_range",
	"DEV_ENABLE_PORT_HEALTH_CHECK": "enable_port_health_check",
	"DEV_PORT_HEALTH_TIMEOUT":      "port_health_timeout",
}
