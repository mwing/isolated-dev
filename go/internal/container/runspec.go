// Package container turns typed specifications into docker invocations.
//
// Nothing here builds a command string. A RunSpec is a struct, one function
// renders it to an argument vector, and tests assert on that vector. This
// is the structural reason v1's quoting bugs cannot recur: a value with a
// space is one element of a slice and stays that way.
package container

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Mount is a bind mount or a named volume.
type Mount struct {
	// Source is a host path or a volume name.
	Source string
	// Target is the path inside the container.
	Target string
	// ReadOnly mounts without write access.
	ReadOnly bool
	// Volume marks Source as a named volume rather than a host path.
	Volume bool
}

// Arg renders the mount for --mount, which unlike -v has an unambiguous
// syntax and fails loudly on a typo instead of silently creating a volume.
func (m Mount) Arg() string {
	kind := "bind"
	if m.Volume {
		kind = "volume"
	}
	parts := []string{
		"type=" + kind,
		"source=" + m.Source,
		"target=" + m.Target,
	}
	if m.ReadOnly {
		parts = append(parts, "readonly")
	}
	return strings.Join(parts, ",")
}

// PortMap publishes a container port on the host.
type PortMap struct {
	Host      int
	Container int
	// HostIP defaults to 127.0.0.1: publishing to 0.0.0.0 would expose a
	// development service to the local network, which is never what a
	// developer running `dev` intended.
	HostIP string
}

// Arg renders the mapping for --publish.
func (p PortMap) Arg() string {
	ip := p.HostIP
	if ip == "" {
		ip = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d:%d", ip, p.Host, p.Container)
}

// RunSpec is everything needed to run one container.
type RunSpec struct {
	Image   string
	Name    string
	Command []string

	// Env is passed as -e NAME=VALUE. Values may contain anything,
	// including spaces and newlines; they are never re-split.
	Env []string

	Mounts  []Mount
	Ports   []PortMap
	WorkDir string

	// Network is the network to attach. Empty uses the daemon default.
	Network string
	// DNS overrides the resolvers. On an internal network this points at
	// the egress sidecar, which is what makes the filtering resolver
	// authoritative for the workload.
	DNS []string

	// User is the uid[:gid] the container runs as. Set by the tool rather
	// than inherited from the image, so a project Dockerfile declaring
	// USER root cannot escalate (ROADMAP 4.1).
	User string

	Interactive bool
	TTY         bool
	Remove      bool
	Detach      bool

	CapDrop     []string
	CapAdd      []string
	SecurityOpt []string
	PidsLimit   int
	Memory      string
	CPUs        string
	ReadOnly    bool

	Labels map[string]string
}

// Hardened returns the baseline security posture every workload gets:
// non-root, no capabilities beyond the few a normal build needs, no
// privilege escalation, and a process cap so a fork bomb cannot take the
// VM down with it.
func Hardened() RunSpec {
	return RunSpec{
		User:    "1000:1000",
		CapDrop: []string{"ALL"},
		CapAdd:  []string{"CHOWN", "DAC_OVERRIDE", "SETGID", "SETUID"},
		SecurityOpt: []string{
			"no-new-privileges:true",
		},
		PidsLimit: 512,
		Remove:    true,
	}
}

// Args renders the spec as arguments to `docker run`, excluding the
// leading "run".
func (s RunSpec) Args() []string {
	var a []string
	add := func(v ...string) { a = append(a, v...) }

	if s.Remove {
		add("--rm")
	}
	if s.Detach {
		add("--detach")
	}
	if s.Interactive {
		add("--interactive")
	}
	if s.TTY {
		add("--tty")
	}
	if s.Name != "" {
		add("--name", s.Name)
	}
	if s.User != "" {
		add("--user", s.User)
	}
	if s.WorkDir != "" {
		add("--workdir", s.WorkDir)
	}
	if s.Network != "" {
		add("--network", s.Network)
	}
	for _, d := range s.DNS {
		add("--dns", d)
	}
	for _, c := range s.CapDrop {
		add("--cap-drop", c)
	}
	for _, c := range s.CapAdd {
		add("--cap-add", c)
	}
	for _, o := range s.SecurityOpt {
		add("--security-opt", o)
	}
	if s.PidsLimit > 0 {
		add("--pids-limit", strconv.Itoa(s.PidsLimit))
	}
	if s.Memory != "" {
		add("--memory", s.Memory)
	}
	if s.CPUs != "" {
		add("--cpus", s.CPUs)
	}
	if s.ReadOnly {
		add("--read-only")
	}
	for _, m := range s.Mounts {
		add("--mount", m.Arg())
	}
	for _, p := range s.Ports {
		add("--publish", p.Arg())
	}
	for _, e := range s.Env {
		add("--env", e)
	}
	for _, k := range sortedKeys(s.Labels) {
		add("--label", k+"="+s.Labels[k])
	}

	add(s.Image)
	add(s.Command...)
	return a
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Validate catches specs that would produce a confusing docker error.
func (s RunSpec) Validate() error {
	if s.Image == "" {
		return fmt.Errorf("container: RunSpec has no image")
	}
	for _, e := range s.Env {
		if !strings.Contains(e, "=") {
			// Bare `-e NAME` copies the value from the tool's own
			// environment, which is exactly the silent passthrough the
			// trust model forbids.
			return fmt.Errorf("container: env %q must be NAME=VALUE; "+
				"implicit passthrough is not allowed", e)
		}
	}
	for _, m := range s.Mounts {
		if m.Source == "" || m.Target == "" {
			return fmt.Errorf("container: mount needs both source and target: %+v", m)
		}
		if !strings.HasPrefix(m.Target, "/") {
			return fmt.Errorf("container: mount target must be absolute: %q", m.Target)
		}
	}
	return nil
}

// BuildSpec describes an image build.
type BuildSpec struct {
	Tag        string
	Dockerfile string
	Context    string
	Platform   string
	BuildArgs  map[string]string
	// Stdin supplies the Dockerfile when Dockerfile is "-".
	Quiet bool
}

// Args renders the spec as arguments to `docker build`.
func (b BuildSpec) Args() []string {
	var a []string
	if b.Tag != "" {
		a = append(a, "--tag", b.Tag)
	}
	if b.Dockerfile != "" {
		a = append(a, "--file", b.Dockerfile)
	}
	if b.Platform != "" {
		a = append(a, "--platform", b.Platform)
	}
	if b.Quiet {
		a = append(a, "--quiet")
	}
	for _, k := range sortedKeys(b.BuildArgs) {
		a = append(a, "--build-arg", k+"="+b.BuildArgs[k])
	}
	ctx := b.Context
	if ctx == "" {
		ctx = "."
	}
	a = append(a, ctx)
	return a
}
