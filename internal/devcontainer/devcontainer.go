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
	"github.com/mwing/isolated-dev/internal/container"
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
		out = append(out, "remoteUser/containerUser: the uid is set by dev, "+
			"so an image cannot choose to run as root")
	}
	if len(c.ForwardPorts) > 0 {
		out = append(out, fmt.Sprintf("forwardPorts (%d): publishing opens a socket "+
			"on your machine, so it is a setting in your own config rather than "+
			"something a repository asks for; detected ports are published as usual",
			len(c.ForwardPorts)))
	}
	if c.PostCreate != nil {
		out = append(out, "postCreateCommand: not run")
	}
	if c.WorkspaceFolder != "" && c.WorkspaceFolder != "/workspace" {
		out = append(out, "workspaceFolder "+c.WorkspaceFolder+": the workspace is /workspace")
	}
	return out
}

// Generated is what to write for a project so an IDE reproduces the same
// environment dev builds.
type Generated struct {
	// Files maps a path relative to the project to its contents.
	Files map[string]string
	// Notes describe what a devcontainer cannot reproduce, so nobody
	// assumes the two are equivalent.
	Notes []string
}

// Options describe the project being exported.
type Options struct {
	Name string
	// Image is used directly when the project has one, e.g. a devcontainer
	// or a base image with nothing added.
	Image string
	// Dockerfile is the rendered Dockerfile to ship alongside, used when
	// there is no ready-made image. Empty when Image is set.
	Dockerfile string
	// DockerfilePath is an existing Dockerfile in the project to point at
	// rather than copying.
	DockerfilePath string
	Ports          []int
	// EgressFiltered reports whether dev runs this project behind the
	// egress proxy, which an IDE will not do.
	EgressFiltered bool
	// Tools were added with `dev tools add`; they are baked into the
	// Dockerfile this writes, but worth naming so the difference from a
	// plain base image is visible.
	Tools []string
}

// Generate produces the devcontainer files for a project.
func Generate(o Options) Generated {
	g := Generated{Files: map[string]string{}}

	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("  // Generated by `dev devcontainer`. Edit or regenerate.\n")
	b.WriteString("  //\n")
	b.WriteString("  // This describes the same image dev builds, so an editor's\n")
	b.WriteString("  // dev container gives the same tools and the same non-root user.\n")
	b.WriteString("  // It does NOT reproduce dev's egress filtering: that lives in a\n")
	b.WriteString("  // proxy sidecar dev starts, and an editor will not start it.\n")
	fmt.Fprintf(&b, "  \"name\": %q,\n", o.Name)

	switch {
	case o.Image != "":
		fmt.Fprintf(&b, "  \"image\": %q,\n", o.Image)
	case o.DockerfilePath != "":
		fmt.Fprintf(&b, "  \"build\": { \"dockerfile\": %q, \"context\": \"..\" },\n",
			o.DockerfilePath)
	default:
		g.Files[filepath.Join(".devcontainer", "Dockerfile")] = o.Dockerfile
		b.WriteString("  \"build\": { \"dockerfile\": \"Dockerfile\", \"context\": \"..\" },\n")
	}

	b.WriteString("  \"workspaceFolder\": \"/workspace\",\n")
	b.WriteString("  \"workspaceMount\": " +
		"\"source=${localWorkspaceFolder},target=/workspace,type=bind\",\n")
	// The same unprivileged uid dev runs as, so files written in the
	// editor belong to the same user as files written by dev — which is the
	// uid of the person at the keyboard, not a fixed 1000. On Linux a bind
	// mount carries host ownership through unchanged, so anything else
	// produces a workspace the editor cannot write.
	fmt.Fprintf(&b, "  \"remoteUser\": \"%d\",\n", container.HostUID())

	if len(o.Ports) > 0 {
		parts := make([]string, 0, len(o.Ports))
		for _, p := range o.Ports {
			parts = append(parts, fmt.Sprintf("%d", p))
		}
		fmt.Fprintf(&b, "  \"forwardPorts\": [%s],\n", strings.Join(parts, ", "))
	}
	// runArgs mirror the hardening dev applies. An editor that ignores
	// them still works; one that honors them matches.
	b.WriteString("  \"runArgs\": [\"--cap-drop=ALL\", \"--security-opt=no-new-privileges:true\"]\n")
	b.WriteString("}\n")

	g.Files[filepath.Join(".devcontainer", "devcontainer.json")] = b.String()

	if o.EgressFiltered {
		g.Notes = append(g.Notes,
			"egress filtering is not reproduced: an editor runs the container with "+
				"normal network access, while dev routes it through an allowlisting proxy")
	}
	if len(o.Tools) > 0 {
		g.Notes = append(g.Notes,
			"tools added with `dev tools add` are baked into the generated Dockerfile: "+
				strings.Join(o.Tools, ", "))
	}
	g.Notes = append(g.Notes,
		"trust decisions are dev's own: an editor will not ask before honoring "+
			"what a project requests")
	return g
}
