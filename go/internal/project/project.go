// Package project resolves a directory into the things a run needs: an
// image name, a Dockerfile, ports, and the network policy that applies.
package project

import (
	"fmt"
	"os"
	"path/filepath"
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

// Registries returns the egress destinations a build or run of this
// project legitimately needs, from the language plugin's own data.
func (p *Project) Registries() []string {
	if !p.Detected.Found() {
		return nil
	}
	return append([]string(nil), p.Detected.Language.Registries...)
}
