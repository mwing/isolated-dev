// Package devcontainer reads a project's devcontainer.json.
//
// Many repositories already describe their environment there. Reading it
// means such a project works without being converted first — and, more
// usefully, without its team having to agree to a second config file.
//
// Only the parts that map onto what this tool does are used. The rest is
// reported rather than silently dropped: a config that is half-honored and
// silently so is worse than one that is not read at all, because the user
// believes the file describes what is running.
package devcontainer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is the subset of devcontainer.json this tool can act on.
type Config struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	// DockerFile is the older spelling; Build.Dockerfile is current.
	DockerFile string `json:"dockerFile"`
	Build      struct {
		Dockerfile string `json:"dockerfile"`
		Context    string `json:"context"`
	} `json:"build"`
	ForwardPorts    []int             `json:"forwardPorts"`
	ContainerEnv    map[string]string `json:"containerEnv"`
	RemoteUser      string            `json:"remoteUser"`
	ContainerUser   string            `json:"containerUser"`
	Mounts          []any             `json:"mounts"`
	PostCreate      any               `json:"postCreateCommand"`
	WorkspaceFolder string            `json:"workspaceFolder"`

	// Dir is the directory the file was found in.
	Dir string `json:"-"`
	// Path is the file itself.
	Path string `json:"-"`
}

// Locations are the places the spec allows the file to live.
func Locations(projectDir string) []string {
	return []string{
		filepath.Join(projectDir, ".devcontainer", "devcontainer.json"),
		filepath.Join(projectDir, ".devcontainer.json"),
	}
}

// Find returns the project's devcontainer.json, if it has one.
func Find(projectDir string) (string, bool) {
	for _, p := range Locations(projectDir) {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// Load reads and parses the file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("devcontainer: reading %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(stripComments(raw), &c); err != nil {
		return nil, fmt.Errorf("devcontainer: parsing %s: %w", path, err)
	}
	c.Path = path
	c.Dir = filepath.Dir(path)
	return &c, nil
}

// stripComments removes the comments and trailing commas devcontainer.json
// is allowed to contain. It is JSON with C comments, which encoding/json
// rejects outright, so a real file fails to parse without this.
func stripComments(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inString, inLine, inBlock := false, false, false

	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				out = append(out, c)
			}
		case inBlock:
			if c == '*' && i+1 < len(in) && in[i+1] == '/' {
				inBlock = false
				i++
			}
		case inString:
			out = append(out, c)
			if c == '\\' && i+1 < len(in) {
				i++
				out = append(out, in[i])
				continue
			}
			if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			inLine = true
			i++
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			inBlock = true
			i++
		default:
			out = append(out, c)
		}
	}
	return removeTrailingCommas(out)
}

// removeTrailingCommas drops a comma before a closing brace or bracket,
// which the devcontainer format tolerates and JSON does not.
func removeTrailingCommas(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inString := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(in) {
				i++
				out = append(out, in[i])
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(in) && (in[j] == ' ' || in[j] == '\t' || in[j] == '\n' || in[j] == '\r') {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// DockerfilePath returns the Dockerfile the config points at, resolved
// relative to the file's own directory as the spec requires.
func (c *Config) DockerfilePath() string {
	name := c.Build.Dockerfile
	if name == "" {
		name = c.DockerFile
	}
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Clean(filepath.Join(c.Dir, name))
}

// Ignored lists the parts of the file this tool does not act on, so a
// half-honored config says so rather than being mistaken for the whole.
func (c *Config) Ignored() []string {
	var out []string
	if len(c.ContainerEnv) > 0 {
		names := make([]string, 0, len(c.ContainerEnv))
		for k := range c.ContainerEnv {
			names = append(names, k)
		}
		sort.Strings(names)
		out = append(out, "containerEnv ("+strings.Join(names, ", ")+
			"): environment passthrough is a grant here, not a setting")
	}
	if len(c.Mounts) > 0 {
		out = append(out, fmt.Sprintf("mounts (%d): a mount is a grant here, not a setting",
			len(c.Mounts)))
	}
	if c.RemoteUser != "" || c.ContainerUser != "" {
		out = append(out, "remoteUser/containerUser: the uid is set by dev2, "+
			"so an image cannot choose to run as root")
	}
	if c.PostCreate != nil {
		out = append(out, "postCreateCommand: not run")
	}
	if c.WorkspaceFolder != "" && c.WorkspaceFolder != "/workspace" {
		out = append(out, "workspaceFolder "+c.WorkspaceFolder+": the workspace is /workspace")
	}
	return out
}
