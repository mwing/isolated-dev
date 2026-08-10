package container

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mwing/isolated-dev/internal/backend"
	"github.com/mwing/isolated-dev/internal/runner"
)

// Engine performs container operations against a backend. Every driver
// gets these for free: they are expressed purely in terms of docker CLI
// calls, which is the one thing a backend must provide.
type Engine struct {
	Backend backend.Backend
}

// New returns an engine over b.
func New(b backend.Backend) *Engine { return &Engine{Backend: b} }

func (e *Engine) docker(ctx context.Context, args ...string) (runner.Result, error) {
	return e.Backend.Docker(ctx, backend.Call{Args: args})
}

// check turns a non-zero exit into an error for operations where a failure
// is genuinely exceptional, keeping the "exit code is data" rule at the
// runner layer where probes need it.
func check(res runner.Result, err error, what string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		return fmt.Errorf("%s: %s", what, msg)
	}
	return nil
}

// Run starts a container from spec. Streams are attached when provided,
// which is what makes an interactive agent session work.
func (e *Engine) Run(ctx context.Context, spec RunSpec, stdin io.Reader, stdout, stderr io.Writer) (runner.Result, error) {
	if err := spec.Validate(); err != nil {
		return runner.Result{}, err
	}
	args := append([]string{"run"}, spec.Args()...)
	return e.Backend.Docker(ctx, backend.Call{
		Args: args, Stdin: stdin, Stdout: stdout, Stderr: stderr,
	})
}

// RunPTY starts a container attached to a pseudo-terminal.
func (e *Engine) RunPTY(ctx context.Context, spec RunSpec, out io.Writer, tty *runner.PTY) (runner.Result, error) {
	if err := spec.Validate(); err != nil {
		return runner.Result{}, err
	}
	args := append([]string{"run"}, spec.Args()...)
	return e.Backend.Docker(ctx, backend.Call{Args: args, Stdout: out, PTY: tty})
}

// Build builds an image, streaming output to w.
func (e *Engine) Build(ctx context.Context, spec BuildSpec, stdin io.Reader, w io.Writer) error {
	args := append([]string{"build"}, spec.Args()...)
	res, err := e.Backend.Docker(ctx, backend.Call{
		Args: args, Stdin: stdin, Stdout: w, Stderr: w,
	})
	return check(res, err, "building "+spec.Tag)
}

// BuildWithDockerfile builds with the Dockerfile supplied as text and the
// context taken from disk. Docker cannot read both from stdin, so the
// Dockerfile is written into the context directory under a temporary name
// and removed afterwards — a rendered language template must not leave a
// file behind in the user's tree.
func (e *Engine) BuildWithDockerfile(ctx context.Context, spec BuildSpec, dockerfile string, w io.Writer) error {
	if spec.Context == "" || spec.Context == "-" {
		return fmt.Errorf("container: BuildWithDockerfile needs a context directory")
	}
	f, err := os.CreateTemp(spec.Context, ".dev-dockerfile-*")
	if err != nil {
		return fmt.Errorf("container: writing Dockerfile: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.WriteString(dockerfile); err != nil {
		_ = f.Close()
		return fmt.Errorf("container: writing Dockerfile: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}

	spec.Dockerfile = filepath.Base(tmp)
	return e.Build(ctx, spec, nil, w)
}

// BuildWithDockerfileStdin builds from a Dockerfile on stdin with no
// context. Used for derived images that add only layers on top of an
// existing tag and need no files from disk.
func (e *Engine) BuildWithDockerfileStdin(ctx context.Context, spec BuildSpec,
	dockerfile string, w io.Writer) error {
	spec.Context = "-"
	spec.Dockerfile = ""
	args := append([]string{"build"}, spec.Args()...)
	res, err := e.Backend.Docker(ctx, backend.Call{
		Args: args, Stdin: strings.NewReader(dockerfile), Stdout: w, Stderr: w,
	})
	return check(res, err, "building "+spec.Tag)
}

// Pull fetches an image so its digest can be read.
func (e *Engine) Pull(ctx context.Context, image string, w io.Writer) error {
	res, err := e.Backend.Docker(ctx, backend.Call{
		Args: []string{"pull", image}, Stdout: w, Stderr: w,
	})
	return check(res, err, "pulling "+image)
}

// Digest returns the repository digest of a local image: the identity the
// registry gave it, rather than the tag someone pointed at it.
func (e *Engine) Digest(ctx context.Context, image string) (string, error) {
	res, err := e.docker(ctx, "inspect", "--format", "{{join .RepoDigests \"\\n\"}}", image)
	if err != nil {
		return "", err
	}
	if err := check(res, nil, "inspecting "+image); err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if d := strings.TrimSpace(line); d != "" {
			return d, nil
		}
	}
	return "", fmt.Errorf("container: %s has no repository digest; "+
		"it was built locally rather than pulled, so there is nothing to pin to", image)
}

// ImageExists reports whether a tag is present locally.
func (e *Engine) ImageExists(ctx context.Context, tag string) (bool, error) {
	res, err := e.docker(ctx, "image", "inspect", tag)
	if err != nil {
		return false, err
	}
	return res.ExitCode == 0, nil
}

// NetworkCreate creates a network. An internal network has no gateway and
// therefore no route out, which is the foundation of ROADMAP 4.3: the
// workload cannot reach anything the sidecar does not relay.
func (e *Engine) NetworkCreate(ctx context.Context, name string, internal bool) error {
	args := []string{"network", "create"}
	if internal {
		args = append(args, "--internal")
	}
	args = append(args, name)
	res, err := e.docker(ctx, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && strings.Contains(res.Stderr, "already exists") {
		// Reusing a network is only safe when it is the network that was
		// asked for. Everything about egress control rests on --internal:
		// a leftover network created without it — by a crashed run, by an
		// older version of this tool, or by a name collision — gives the
		// workload a default route and fully open egress while the CLI
		// reports `network: allowlist`. No error, no warning, and the one
		// security property the mode exists for silently absent.
		if internal {
			return e.requireInternal(ctx, name)
		}
		return nil
	}
	return check(res, nil, "creating network "+name)
}

// requireInternal fails unless an existing network really has no route out.
//
// Anything other than a definite "true" refuses: a network whose flag
// cannot be read is a network whose isolation cannot be relied on, and
// guessing in the permissive direction is the failure this check exists to
// prevent.
func (e *Engine) requireInternal(ctx context.Context, name string) error {
	res, err := e.docker(ctx, "network", "inspect", "--format", "{{.Internal}}", name)
	if err != nil {
		return err
	}
	if err := check(res, nil, "inspecting network "+name); err != nil {
		return err
	}
	if strings.TrimSpace(res.Stdout) == "true" {
		return nil
	}
	return fmt.Errorf("network %s already exists and is not internal: the workload "+
		"would have a route out that the egress proxy never sees, while this run "+
		"reported itself filtered.\nRemove it and start clean:\n  dev clean --all",
		name)
}

// NetworkRemove deletes a network, ignoring absence.
func (e *Engine) NetworkRemove(ctx context.Context, name string) error {
	res, err := e.docker(ctx, "network", "rm", name)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && (strings.Contains(res.Stderr, "not found") ||
		strings.Contains(res.Stderr, "No such network")) {
		return nil
	}
	return check(res, nil, "removing network "+name)
}

// NetworkConnect attaches a running container to another network. This is
// how the sidecar becomes dual-homed: it starts on the internal network and
// then gains the one interface that reaches the outside.
func (e *Engine) NetworkConnect(ctx context.Context, network, container string) error {
	res, err := e.docker(ctx, "network", "connect", network, container)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && strings.Contains(res.Stderr, "already exists") {
		return nil
	}
	return check(res, nil, fmt.Sprintf("connecting %s to %s", container, network))
}

// ContainerIP returns a container's address on a specific network.
func (e *Engine) ContainerIP(ctx context.Context, name, network string) (string, error) {
	format := fmt.Sprintf("{{ (index .NetworkSettings.Networks %q).IPAddress }}", network)
	res, err := e.docker(ctx, "inspect", "--format", format, name)
	if err != nil {
		return "", err
	}
	if err := check(res, nil, "inspecting "+name); err != nil {
		return "", err
	}
	ip := strings.TrimSpace(res.Stdout)
	if ip == "" || ip == "<no value>" {
		return "", fmt.Errorf("container %s has no address on network %s", name, network)
	}
	return ip, nil
}

// Stop stops a container, ignoring absence.
func (e *Engine) Stop(ctx context.Context, name string) error {
	res, err := e.docker(ctx, "stop", "--time", "5", name)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && strings.Contains(res.Stderr, "No such container") {
		return nil
	}
	return check(res, nil, "stopping "+name)
}

// Remove deletes a container, ignoring absence.
func (e *Engine) Remove(ctx context.Context, name string) error {
	res, err := e.docker(ctx, "rm", "--force", name)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && strings.Contains(res.Stderr, "No such container") {
		return nil
	}
	return check(res, nil, "removing "+name)
}

// Exec runs a command inside a running container.
func (e *Engine) Exec(ctx context.Context, name string, cmd []string) (runner.Result, error) {
	args := append([]string{"exec", name}, cmd...)
	return e.Backend.Docker(ctx, backend.Call{Args: args})
}

// Running reports whether a container exists and is running.
func (e *Engine) Running(ctx context.Context, name string) (exists, running bool, err error) {
	res, err := e.docker(ctx, "inspect", "--format", "{{.State.Running}}", name)
	if err != nil {
		return false, false, err
	}
	if res.ExitCode != 0 {
		return false, false, nil
	}
	return true, strings.TrimSpace(res.Stdout) == "true", nil
}

// Logs returns a container's output.
func (e *Engine) Logs(ctx context.Context, name string) (string, error) {
	res, err := e.docker(ctx, "logs", name)
	if err != nil {
		return "", err
	}
	// Sidecar events go to stdout, its own diagnostics to stderr; the
	// caller wants both.
	return res.Stdout + res.Stderr, nil
}

// VolumeCreate creates a named volume if it does not exist.
func (e *Engine) VolumeCreate(ctx context.Context, name string) error {
	res, err := e.docker(ctx, "volume", "create", name)
	return check(res, err, "creating volume "+name)
}

// VolumeRemove deletes a named volume, ignoring absence.
func (e *Engine) VolumeRemove(ctx context.Context, name string) error {
	res, err := e.docker(ctx, "volume", "rm", name)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && strings.Contains(res.Stderr, "no such volume") {
		return nil
	}
	return check(res, nil, "removing volume "+name)
}

// VolumeExists reports whether a named volume is present.
func (e *Engine) VolumeExists(ctx context.Context, name string) (bool, error) {
	res, err := e.docker(ctx, "volume", "inspect", name)
	if err != nil {
		return false, err
	}
	return res.ExitCode == 0, nil
}

// StatGroup reports the group owning a path as a container sees it.
//
// Host file sharing remaps ownership, so the value cannot be derived from a
// stat on the host: a socket owned by root:docker outside may appear owned by
// something else inside, and a supplementary group added from the wrong
// number is a mount that is present and unusable.
func (e *Engine) StatGroup(ctx context.Context, image, source, target string) (string, error) {
	spec := RunSpec{
		Image:   image,
		Remove:  true,
		Network: "none",
		Mounts:  []Mount{{Source: source, Target: target}},
		Command: []string{"stat", "-c", "%g", target},
		// Labelled so it is distinguishable from the user's own container by
		// anything reading the daemon — `dev status`, a cleanup, or a test
		// asserting on what the workload was given.
		Labels: map[string]string{"dev.role": "probe"},
	}
	var out strings.Builder
	res, err := e.Run(ctx, spec, nil, &out, io.Discard)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("could not read the group owning %s inside the container", source)
	}
	return strings.TrimSpace(out.String()), nil
}

// CopyIn copies a host file into a container path.
func (e *Engine) CopyIn(ctx context.Context, hostPath, container, target string) error {
	res, err := e.docker(ctx, "cp", hostPath, container+":"+target)
	return check(res, err, "copying "+hostPath)
}

// Inspect returns the raw inspect JSON for a container, decoded into v.
func (e *Engine) Inspect(ctx context.Context, name string, v any) error {
	res, err := e.docker(ctx, "inspect", name)
	if err != nil {
		return err
	}
	if err := check(res, nil, "inspecting "+name); err != nil {
		return err
	}
	return json.Unmarshal([]byte(res.Stdout), v)
}

// LogsFollow streams a container's output to w until the context is
// cancelled or the container exits. Used to surface egress denials while
// the workload is still running, rather than only at exit.
func (e *Engine) LogsFollow(ctx context.Context, name string, w io.Writer) error {
	_, err := e.Backend.Docker(ctx, backend.Call{
		Args:   []string{"logs", "--follow", "--since", "0m", name},
		Stdout: w,
		Stderr: w,
	})
	return err
}

// Info is one container as the daemon reports it.
type Info struct {
	ID     string            `json:"ID"`
	Names  string            `json:"Names"`
	Image  string            `json:"Image"`
	Status string            `json:"Status"`
	State  string            `json:"State"`
	Labels string            `json:"Labels"`
	labels map[string]string `json:"-"`
}

// Label returns a label value, parsing the daemon's comma-separated form
// on first use.
func (i *Info) Label(key string) string {
	if i.labels == nil {
		i.labels = map[string]string{}
		for _, pair := range strings.Split(i.Labels, ",") {
			if k, v, ok := strings.Cut(pair, "="); ok {
				i.labels[k] = v
			}
		}
	}
	return i.labels[key]
}

// List returns containers carrying a label, running or not.
func (e *Engine) List(ctx context.Context, label string) ([]Info, error) {
	res, err := e.docker(ctx, "ps", "--all", "--filter", "label="+label, "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	if err := check(res, nil, "listing containers"); err != nil {
		return nil, err
	}
	var out []Info
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var info Info
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

// DiskUsage reports what the daemon is holding, as display lines.
//
// v1 had a `dev disk` command for this. It is folded into doctor because
// nobody runs a disk command on a good day, and on a bad day doctor is
// what they run.
func (e *Engine) DiskUsage(ctx context.Context) ([]string, error) {
	res, err := e.docker(ctx, "system", "df", "--format",
		"{{.Type}}\t{{.TotalCount}}\t{{.Size}}\t{{.Reclaimable}}")
	if err != nil {
		return nil, err
	}
	if err := check(res, nil, "reading disk usage"); err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 4 {
			continue
		}
		out = append(out, fmt.Sprintf("%-15s %4s items  %10s  reclaimable %s",
			fields[0]+":", fields[1], fields[2], fields[3]))
	}
	return out, nil
}

// PublishedBy returns the containers publishing a host port.
//
// Used to explain a bind failure. The daemon's own message names the port
// and nothing else, which leaves the user to work out that a container
// from an unrelated project is holding it.
func (e *Engine) PublishedBy(ctx context.Context, port int) ([]Info, error) {
	res, err := e.docker(ctx, "ps", "--filter", fmt.Sprintf("publish=%d", port),
		"--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	if err := check(res, nil, "listing containers"); err != nil {
		return nil, err
	}
	var out []Info
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var info Info
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

// Networks returns network names matching a prefix.
func (e *Engine) Networks(ctx context.Context, prefix string) ([]string, error) {
	res, err := e.docker(ctx, "network", "ls", "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	if err := check(res, nil, "listing networks"); err != nil {
		return nil, err
	}
	var out []string
	for _, name := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if name = strings.TrimSpace(name); name != "" && strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out, nil
}

// RemoveImage deletes an image, ignoring absence.
func (e *Engine) RemoveImage(ctx context.Context, tag string) error {
	res, err := e.docker(ctx, "image", "rm", tag)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && (strings.Contains(res.Stderr, "No such image") ||
		strings.Contains(res.Stderr, "not found")) {
		return nil
	}
	return check(res, nil, "removing image "+tag)
}
