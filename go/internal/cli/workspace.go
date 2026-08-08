package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/container"
	"github.com/mwing/isolated-dev/go/internal/langs"
	"github.com/mwing/isolated-dev/go/internal/netpolicy"
	"github.com/mwing/isolated-dev/go/internal/project"
	"github.com/mwing/isolated-dev/go/internal/trust"
)

// resolveProject loads config, language plugins and the project itself.
func resolveProject(env *Env) (config.Config, *project.Project, error) {
	cfg, err := config.Load(env.Paths, env.Env)
	if err != nil {
		return cfg, nil, err
	}
	set, err := langs.Load(env.Paths.Languages)
	if err != nil {
		return cfg, nil, err
	}
	for _, note := range set.Notes {
		fmt.Fprintf(env.Stderr, "⚠  language plugin: %s\n", note)
	}
	p, err := project.Resolve(env.Paths.ProjectDir, cfg, set)
	return cfg, p, err
}

func newBuildCmd(env *Env) *cobra.Command {
	var platform string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build this project's image",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, p, err := resolveProject(env)
			if err != nil {
				return err
			}
			return buildImage(cmd.Context(), env, cfg, p, platform)
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "", "target platform, e.g. linux/amd64")
	return cmd
}

func buildImage(ctx context.Context, env *Env, cfg config.Config, p *project.Project, platform string) error {
	dockerfile, err := p.RenderedDockerfile()
	if err != nil {
		return err
	}

	source := p.Dockerfile
	if p.FromTemplate {
		source = p.Detected.Language.DockerfileTemplate() + " (language template)"
	}
	fmt.Fprintf(env.Stdout, "Project:    %s\n", p.Name)
	fmt.Fprintf(env.Stdout, "Detected:   %s\n", p.Detected.Explain())
	fmt.Fprintf(env.Stdout, "Dockerfile: %s\n", source)
	fmt.Fprintf(env.Stdout, "Image:      %s\n\n", p.Image)

	eng := container.New(env.driver(cfg.VMName))
	// The build context is the project directory, but the Dockerfile comes
	// from stdin so a rendered template needs no temporary file in the
	// user's tree.
	return eng.BuildWithDockerfile(ctx, container.BuildSpec{
		Tag:      p.Image,
		Context:  p.Dir,
		Platform: platform,
	}, dockerfile, env.Stdout)
}

func newRunCmd(env *Env) *cobra.Command {
	var (
		command      string
		tty          string
		rebuild      bool
		offline      bool
		network      string
		extraHosts   []string
		egressPrompt string
		image        string
	)

	cmd := &cobra.Command{
		Use:   "run [-- args...]",
		Short: "Run a command in this project's container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspace(cmd.Context(), env, workspaceOpts{
				Command:      splitCommand(command, args),
				TTY:          tty,
				Rebuild:      rebuild,
				Offline:      offline,
				Network:      network,
				ExtraHosts:   extraHosts,
				EgressPrompt: egressPrompt,
				Image:        image,
			})
		},
	}
	addWorkspaceFlags(cmd, &command, &tty, &rebuild, &offline, &network, &extraHosts, &image)
	addEgressPromptFlag(cmd, &egressPrompt)
	return cmd
}

func newShellCmd(env *Env) *cobra.Command {
	var (
		command      string
		tty          string
		rebuild      bool
		offline      bool
		network      string
		extraHosts   []string
		egressPrompt string
		image        string
	)

	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Open a shell in this project's container",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := splitCommand(command, nil)
			if len(c) == 0 {
				c = []string{"/bin/bash"}
			}
			return runWorkspace(cmd.Context(), env, workspaceOpts{
				Command:      c,
				TTY:          tty,
				Rebuild:      rebuild,
				Offline:      offline,
				Network:      network,
				ExtraHosts:   extraHosts,
				EgressPrompt: egressPrompt,
				Image:        image,
				Fallback:     []string{"/bin/sh"},
			})
		},
	}
	addWorkspaceFlags(cmd, &command, &tty, &rebuild, &offline, &network, &extraHosts, &image)
	addEgressPromptFlag(cmd, &egressPrompt)
	return cmd
}

func addWorkspaceFlags(cmd *cobra.Command, command, tty *string, rebuild, offline *bool,
	network *string, extraHosts *[]string, image *string) {
	cmd.Flags().StringVar(image, "image", "",
		"run this image instead of building one for the project")
	cmd.Flags().StringVarP(command, "command", "c", "", "run this command instead of the default")
	cmd.Flags().StringVar(tty, "tty", "auto", "allocate a terminal: auto, on, or off")
	cmd.Flags().BoolVar(rebuild, "rebuild", false, "rebuild the image first")
	cmd.Flags().BoolVar(offline, "offline", false, "no network at all")
	cmd.Flags().StringVar(network, "network", "", "override network mode: allowlist, open or none")
	cmd.Flags().StringArrayVar(extraHosts, "allow-host", nil, "add a destination for this run")
}

// egressPromptFlag is shared by run and shell.
func addEgressPromptFlag(cmd *cobra.Command, mode *string) {
	cmd.Flags().StringVar(mode, "egress-prompt", "auto",
		"on a blocked destination: ask (hold the request), report (fail now), or auto")
}

// splitCommand turns -c and trailing args into an argv. -c takes a shell
// string because that is what a user expects from -c; trailing args after
// -- are already a vector and are not re-split.
func splitCommand(c string, args []string) []string {
	if c != "" {
		return []string{"/bin/sh", "-c", c}
	}
	return args
}

type workspaceOpts struct {
	Command      []string
	TTY          string
	Rebuild      bool
	Offline      bool
	Network      string
	ExtraHosts   []string
	EgressPrompt string
	// Image runs a given image directly instead of building one for the
	// project. Sandboxing something that is not a project — a downloaded
	// binary, a script from a stranger — otherwise needs a Dockerfile
	// written before the thing can be looked at, which is backwards.
	Image string
	// Fallback is tried when the primary command is missing from the
	// image, so a distroless or alpine base still opens a shell.
	Fallback []string
}

func runWorkspace(ctx context.Context, env *Env, o workspaceOpts) error {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}

	store, err := trust.Load(env.Paths.Home, p.Dir)
	if err != nil {
		return err
	}
	// A project file is a request; running the project is not consent.
	if err := enforceConsent(env, cfg, store); err != nil {
		return err
	}

	if o.Network != "" {
		mode, err := project.ParseNetworkMode(o.Network)
		if err != nil {
			return err
		}
		p.Network = mode
	}
	if o.Offline {
		p.Network = project.NetworkNone
	}

	eng := container.New(env.driver(cfg.VMName))
	if o.Image != "" {
		// An image named explicitly is used as it is: building it would
		// mean overwriting what the user asked for.
		p.Image = o.Image
	} else {
		exists, err := eng.ImageExists(ctx, p.Image)
		if err != nil {
			return err
		}
		if o.Rebuild || !exists {
			if err := buildImage(ctx, env, cfg, p, ""); err != nil {
				return err
			}
		}
	}

	image, err := ensureTools(ctx, env, eng, p, store, cfg)
	if err != nil {
		return err
	}
	spec := p.RunSpec(cfg, o.Command, wantTTY(o.TTY, os.Stdin))
	spec.Image = image

	switch p.Network {
	case project.NetworkOpen, project.NetworkNone:
		return streamRun(ctx, env, eng, spec, o.Fallback)
	}

	// Allowlist mode: the same enforcement agents get. The project's
	// language registries are permitted because a build that cannot reach
	// its own package index is not a policy, it is a broken tool.
	allowed := append(p.Registries(), store.Resolve("default").AllowHosts...)
	allowed = append(allowed, o.ExtraHosts...)

	if len(allowed) == 0 {
		fmt.Fprintf(env.Stderr,
			"⚠  network: allowlist, but nothing is allowed for this project.\n"+
				"   Use `--network open`, or grant destinations with `dev2 agent allow`.\n")
	}

	// Asking needs a terminal to ask on AND stdin free to answer with. An
	// interactive shell already owns stdin, so a prompt would fight the
	// workload for the user's keystrokes. Until the console owns the
	// screen (ROADMAP M5) that combination falls back to reporting, said
	// out loud rather than silently.
	mode, err := ParseEgressMode(o.EgressPrompt)
	if err != nil {
		return err
	}
	workloadOwnsStdin := len(o.Command) == 0 || spec.TTY
	resolved := mode.Resolve(isTerminal(os.Stdin) && !workloadOwnsStdin)
	if mode == EgressAsk && workloadOwnsStdin {
		fmt.Fprintf(env.Stderr,
			"⚠  --egress-prompt ask needs stdin, which this session gives to the\n"+
				"   workload. Reporting instead; a blocked host will fail rather than wait.\n")
		resolved = EgressReport
	}

	// A workload on an internal network cannot publish ports itself, so
	// the sidecar publishes them and relays to the workload by container
	// name. The container therefore needs a stable name.
	spec.Name = p.Container
	side, topo, err := startSidecarWithPorts(ctx, eng, p, allowed, resolved, p.Ports)
	if err != nil {
		return err
	}
	defer reportEgress(ctx, env, side)

	spec.Network = topo.InternalNetwork
	spec.DNS = []string{topo.SidecarIP}
	spec.Env = append(spec.Env, topo.Env()...)
	if len(spec.Ports) > 0 {
		for _, port := range spec.Ports {
			fmt.Fprintf(env.Stderr, "  ↦ http://127.0.0.1:%d → container :%d\n",
				port.Host, port.Container)
		}
		// Published by the sidecar, not by this container.
		spec.Ports = nil
	}

	var ask *prompter
	if resolved == EgressAsk {
		ask = newPrompter(env, side, store, p.Dir)
		// Only one reader can own stdin. The prompt needs it to take an
		// answer, so the workload does without: in ask mode the command is
		// one that does not read input anyway, which is the same condition
		// that made asking possible.
		spec.Interactive = false
	}
	watchEgress(ctx, env, eng, topo, ask)

	stdin := io.Reader(os.Stdin)
	if ask != nil {
		stdin = nil
	}
	return streamRunWith(ctx, env, eng, spec, o.Fallback, stdin)
}

func streamRun(ctx context.Context, env *Env, eng *container.Engine,
	spec container.RunSpec, fallback []string) error {
	return streamRunWith(ctx, env, eng, spec, fallback, os.Stdin)
}

func streamRunWith(ctx context.Context, env *Env, eng *container.Engine,
	spec container.RunSpec, fallback []string, stdin io.Reader) error {
	var errBuf strings.Builder
	res, err := eng.Run(ctx, spec, stdin, env.Stdout, io.MultiWriter(env.Stderr, &errBuf))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && len(fallback) > 0 && missingCommand(errBuf.String()) {
		spec.Command = fallback
		res, err = eng.Run(ctx, spec, stdin, env.Stdout, env.Stderr)
		if err != nil {
			return err
		}
	}
	if res.ExitCode != 0 {
		if hint := explainTTYFailure(errBuf.String()); hint != "" {
			return fmt.Errorf("could not start: %s", hint)
		}
		return fmt.Errorf("exited with status %d", res.ExitCode)
	}
	return nil
}

func missingCommand(stderr string) bool {
	return strings.Contains(stderr, "executable file not found") ||
		strings.Contains(stderr, "no such file or directory")
}

// startSidecar brings up the egress proxy for a workspace run.
func startSidecar(ctx context.Context, eng *container.Engine, p *project.Project,
	allowed []string, mode EgressMode) (*netpolicy.Sidecar, netpolicy.Topology, error) {
	return startSidecarWithPorts(ctx, eng, p, allowed, mode, nil)
}

func startSidecarWithPorts(ctx context.Context, eng *container.Engine, p *project.Project,
	allowed []string, mode EgressMode, ports []int) (*netpolicy.Sidecar, netpolicy.Topology, error) {
	var ask time.Duration
	if mode == EgressAsk {
		ask = AskTimeout
	}
	var forwards []string
	for _, port := range ports {
		forwards = append(forwards, fmt.Sprintf("%d:%s:%d", port, p.Container, port))
	}
	side := &netpolicy.Sidecar{
		Engine:     eng,
		Image:      proxyImageTag,
		Allow:      allowed,
		AskTimeout: ask,
		Forwards:   forwards,
		Ports:      ports,
		Topology: netpolicy.Topology{
			InternalNetwork: "dev2-" + p.Name + "-internal",
			EgressNetwork:   "dev2-" + p.Name + "-egress",
			SidecarName:     "dev2-" + p.Name + "-proxy",
			ProxyPort:       3128,
			DNSPort:         53,
		},
	}
	topo, err := side.Start(ctx)
	return side, topo, err
}

// watchEgress streams sidecar events. When ask is non-nil, pending
// decisions are put to the user; otherwise denials are reported as they
// happen.
func watchEgress(ctx context.Context, env *Env, eng *container.Engine,
	topo netpolicy.Topology, ask *prompter) {
	watcher := netpolicy.NewWatcher(func(n netpolicy.Notice) {
		fmt.Fprintf(env.Stderr, "\r\n  ⛔ egress %s\n", n.String())
	})
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		_ = eng.LogsFollow(ctx, topo.SidecarName, pw)
	}()
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var e netpolicy.Event
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				continue
			}
			if ask != nil {
				ask.Handle(ctx, e)
				continue
			}
			watcher.Observe(e)
		}
	}()
}

func reportEgress(ctx context.Context, env *Env, side *netpolicy.Sidecar) {
	summary, err := side.Stop(context.WithoutCancel(ctx))
	if err != nil {
		fmt.Fprintf(env.Stderr, "\nwarning: reading egress log: %v\n", err)
		return
	}
	fmt.Fprint(env.Stderr, "\r\n")
	if len(summary) == 0 {
		return
	}
	fmt.Fprintln(env.Stderr, "Egress: blocked destinations this run:")
	for _, line := range summary {
		fmt.Fprintf(env.Stderr, "  %s\n", line)
	}
	fmt.Fprintf(env.Stderr, "Allow once:       --allow-host HOST\n")
	fmt.Fprintf(env.Stderr, "Allow from now:   dev2 agent allow HOST\n")
	fmt.Fprintf(env.Stderr, "Unrestricted:     --network open\n")
}
