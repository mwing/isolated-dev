// Package project resolves a directory into the things a run needs: an
// image name, a Dockerfile, ports, and the network policy that applies.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/container"
	"github.com/mwing/isolated-dev/go/internal/detect"
	"github.com/mwing/isolated-dev/go/internal/langs"
)

// NetworkMode is how a normal run reaches the outside.
type NetworkMode string

const (
	// NetworkAllowlist routes egress through the proxy, permitting the
	// detected language's registries plus whatever the user granted. This
	// is the default: the tool's promise is deny by default, and a run
	// that silently had the whole internet would not keep it.
	NetworkAllowlist NetworkMode = "allowlist"
	// NetworkOpen is v1 behavior: no proxy, no restriction.
	NetworkOpen NetworkMode = "open"
	// NetworkNone has no network at all.
	NetworkNone NetworkMode = "none"
)

// ParseNetworkMode validates a configured mode.
func ParseNetworkMode(s string) (NetworkMode, error) {
	switch NetworkMode(strings.TrimSpace(s)) {
	case "", NetworkAllowlist:
		return NetworkAllowlist, nil
	case NetworkOpen:
		return NetworkOpen, nil
	case NetworkNone:
		return NetworkNone, nil
	default:
		return "", fmt.Errorf("project: unknown network mode %q (want allowlist, open or none)", s)
	}
}

// Project is a resolved project directory.
type Project struct {
	Dir  string
	Name string
	// Detected is what the language plugins found.
	Detected detect.Result
	// Dockerfile is the path used to build, empty when the project has
	// none and a plugin template will be rendered instead.
	Dockerfile string
	// FromTemplate reports that the Dockerfile came from a language plugin
	// rather than the project.
	FromTemplate bool

	Image     string
	Container string
	Ports     []int
	Network   NetworkMode
}

// SanitizeName turns a directory name into a valid docker name fragment.
// v1 grew this function after directories with spaces and capitals broke
// builds; the rules are the same.
func SanitizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	// Collapse runs of separators and trim both ends: a docker name must
	// begin and end alphanumeric, so "My Project!" must not become
	// "my-project-", which the daemon rejects.
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-._")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-._")
	}
	if s == "" {
		s = "project"
	}
	return s
}

// Resolve inspects dir and produces everything a build or run needs.
func Resolve(dir string, cfg config.Config, set *langs.Set) (*Project, error) {
	name := SanitizeName(filepath.Base(dir))
	mode, err := ParseNetworkMode(cfg.Network)
	if err != nil {
		return nil, err
	}

	p := &Project{
		Dir:      dir,
		Name:     name,
		Detected: detect.Detect(dir, set),
		Network:  mode,
	}

	prefix := cfg.ContainerPrefix
	if prefix == "" {
		prefix = "dev"
	}
	// Same shape as v1 so images built by either tool are distinguishable
	// at a glance but do not collide.
	p.Image = fmt.Sprintf("%s-img-%s", prefix, name)
	p.Container = fmt.Sprintf("%s-ctn-%s", prefix, name)
	p.Ports = detect.Ports(cfg.ForwardPorts, p.Detected)

	if df := filepath.Join(dir, "Dockerfile"); fileExists(df) {
		p.Dockerfile = df
	} else if p.Detected.Found() {
		p.FromTemplate = true
	}
	return p, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// RenderedDockerfile returns the Dockerfile contents to build with. A
// project's own Dockerfile wins; otherwise the detected language's
// template is rendered.
func (p *Project) RenderedDockerfile() (string, error) {
	if p.Dockerfile != "" {
		raw, err := os.ReadFile(p.Dockerfile)
		if err != nil {
			return "", fmt.Errorf("project: reading %s: %w", p.Dockerfile, err)
		}
		return string(raw), nil
	}
	if !p.Detected.Found() {
		return "", fmt.Errorf("project: no Dockerfile and no language detected in %s", p.Dir)
	}
	tmpl := p.Detected.Language.DockerfileTemplate()
	raw, err := os.ReadFile(tmpl)
	if err != nil {
		return "", fmt.Errorf("project: reading template %s: %w", tmpl, err)
	}
	return langs.RenderDockerfile(string(raw), langs.TemplateVars{
		Version:     p.Detected.Version,
		ProjectName: p.Name,
	}), nil
}

// RunSpec builds the container specification for a run.
//
// The posture is the hardened baseline: the tool sets the uid rather than
// trusting the image's USER, capabilities are dropped, and privilege
// escalation is off. Only the workspace is writable.
func (p *Project) RunSpec(cfg config.Config, command []string, tty bool) container.RunSpec {
	spec := container.Hardened()
	spec.Image = p.Image
	spec.WorkDir = "/workspace"
	spec.Interactive = true
	spec.TTY = tty
	spec.Memory = cfg.MemoryLimit
	spec.CPUs = cfg.CPULimit
	spec.Command = command
	spec.Mounts = []container.Mount{{Source: p.Dir, Target: "/workspace"}}
	spec.Labels = map[string]string{
		"dev2.role":    "workspace",
		"dev2.project": p.Name,
	}
	for _, port := range p.Ports {
		spec.Ports = append(spec.Ports, container.PortMap{Host: port, Container: port})
	}
	if p.Network == NetworkNone {
		spec.Network = "none"
	}
	return spec
}

// ToolsImage is the tag for this project's image plus a set of extra
// tools. The hash means an unchanged list reuses the built image and any
// change produces a new one, so the tag never lies about its contents.
func (p *Project) ToolsImage(tools []string) string {
	if len(tools) == 0 {
		return p.Image
	}
	sorted := append([]string(nil), tools...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\x00")))
	return fmt.Sprintf("%s-tools:%s", p.Image, hex.EncodeToString(sum[:4]))
}

// ToolsDockerfile layers tools onto the project image.
//
// A declaration rebuilt into an image, never a mutated container: `docker
// commit` would give an image nobody can reproduce and an environment that
// exists only on the machine where the command was typed.
func ToolsDockerfile(base string, tools []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", base)
	// Installing needs root; the runtime uid is set by the tool on every
	// run, so the image's final USER does not decide who the workload is.
	b.WriteString("USER root\n")
	quoted := make([]string, 0, len(tools))
	for _, t := range tools {
		quoted = append(quoted, shellSafe(t))
	}
	list := strings.Join(quoted, " ")
	fmt.Fprintf(&b, "RUN (command -v apt-get >/dev/null && apt-get update && "+
		"DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends %s "+
		"&& rm -rf /var/lib/apt/lists/*) || "+
		"(command -v apk >/dev/null && apk add --no-cache %s) || "+
		"(command -v dnf >/dev/null && dnf install -y %s) || "+
		"(echo 'no supported package manager in this image' >&2; exit 1)\n",
		list, list, list)
	return b.String()
}

// shellSafe rejects anything that is not a plain package name. The list is
// interpolated into a RUN line, so a name containing shell metacharacters
// would execute during the build.
func shellSafe(name string) string {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '+':
		default:
			return ""
		}
	}
	return name
}

// ValidToolName reports whether a name is safe to install.
func ValidToolName(name string) bool {
	return name != "" && shellSafe(name) == name
}

// Registries returns the egress destinations a build or run of this
// project legitimately needs, from the language plugin's own data.
func (p *Project) Registries() []string {
	if !p.Detected.Found() {
		return nil
	}
	return append([]string(nil), p.Detected.Language.Registries...)
}
