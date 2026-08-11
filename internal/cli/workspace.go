package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/history"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/trust"
)

// resolveProject loads config, language plugins and the project itself.
func resolveProject(env *Env) (config.Config, *project.Project, error) {
	cfg, err := config.Load(env.Paths, env.Env)
	if err != nil {
		return cfg, nil, err
	}
	set, err := loadLanguages(env)
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
	return buildImageWith(ctx, env, cfg, p, platform, false)
}

func buildImageWith(ctx context.Context, env *Env, cfg config.Config, p *project.Project,
	platform string, noCache bool) error {
	dockerfile, err := p.RenderedDockerfile()
	if err != nil {
		return err
	}
	// A pinned digest is what makes two builds of the same project produce
	// the same image, here and on a teammate's machine.
	pinned := project.ApplyPins(dockerfile, cfg.Pins)
	unpinned := project.BaseImages(pinned)
	if cfg.UpgradePackages {
		pinned = project.WithPackageUpgrade(pinned)
	}

	pol, err := loadPolicy(env)
	if err != nil {
		return err
	}
	for _, image := range project.BaseImages(dockerfile) {
		if verr := pol.CheckRegistry(image); verr != nil {
			return verr
		}
	}

	source := p.Dockerfile
	switch {
	case p.FromTemplate:
		source = p.Detected.Language.DockerfileTemplate() + " (language template)"
	case p.DevcontainerImage != "":
		source = p.Devcontainer.Path + " (image " + p.DevcontainerImage + ")"
	case p.Devcontainer != nil && p.Dockerfile != "":
		source = p.Dockerfile
	}
	fmt.Fprintf(env.Stdout, "Project:    %s\n", p.Name)
	fmt.Fprintf(env.Stdout, "Detected:   %s\n", p.Detected.Explain())
	fmt.Fprintf(env.Stdout, "Dockerfile: %s\n", source)
	fmt.Fprintf(env.Stdout, "Image:      %s\n", p.Image)
	if len(cfg.Pins) > 0 {
		fmt.Fprintf(env.Stdout, "Pinned:     %d base image(s)\n", len(cfg.Pins))
	}
	if cfg.UpgradePackages {
		fmt.Fprintf(env.Stdout, "Upgrades:   base image packages upgraded\n")
	}
	// A config half-honored silently is worse than one not read at all.
	if p.Devcontainer != nil {
		for _, note := range p.Devcontainer.Ignored() {
			fmt.Fprintf(env.Stderr, "⚠  devcontainer.json: %s\n", note)
		}
	}
	if len(unpinned) > 0 {
		fmt.Fprintf(env.Stdout, "Unpinned:   %s  (dev pin)\n", strings.Join(unpinned, " "))
	}
	fmt.Fprintln(env.Stdout)

	eng := container.New(env.driver(cfg.VMName))
	// The build context is the project directory, but the Dockerfile comes
	// from stdin so a rendered template needs no temporary file in the
	// user's tree.
	return eng.BuildWithDockerfile(ctx, container.BuildSpec{
		Tag:      p.Image,
		Context:  p.Dir,
		Platform: platform,
		NoCache:  noCache,
	}, pinned, env.Stdout)
}

// buildImageNoCache rebuilds every layer, which is what an update needs:
// a cached install layer reinstalls exactly what it installed before.
func buildImageNoCache(ctx context.Context, env *Env, cfg config.Config, p *project.Project) error {
	return buildImageWith(ctx, env, cfg, p, "", true)
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
		useCloneDir  bool
		cloneDepth   int
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
				Clone:        useCloneDir,
				CloneDepth:   cloneDepth,
			})
		},
	}
	addWorkspaceFlags(cmd, &command, &tty, &rebuild, &offline, &network, &extraHosts, &image)
	addEgressPromptFlag(cmd, &egressPrompt)
	addCloneFlag(cmd, &useCloneDir, &cloneDepth)
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
		useCloneDir  bool
		cloneDepth   int
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
				Clone:        useCloneDir,
				CloneDepth:   cloneDepth,
			})
		},
	}
	addWorkspaceFlags(cmd, &command, &tty, &rebuild, &offline, &network, &extraHosts, &image)
	addEgressPromptFlag(cmd, &egressPrompt)
	addCloneFlag(cmd, &useCloneDir, &cloneDepth)
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

// addCloneFlag is shared by every command that mounts a workspace.
//
// --clone-depth implies --clone. Asking for a shallow clone and then being
// told nothing was cloned would be a pedantic reading of an unambiguous
// request.
func addCloneFlag(cmd *cobra.Command, clone *bool, depth *int) {
	cmd.Flags().BoolVar(clone, "clone", false,
		"work in a private clone of the repository, not the working tree")
	cmd.Flags().IntVar(depth, "clone-depth", 0,
		"copy only this many commits of history into the clone (0: all)")
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
	// Clone mounts a private copy of the repository instead of the
	// working tree, so an unattended run cannot damage what is being
	// edited outside it.
	Clone bool
	// CloneDepth limits the history that copy carries. Zero copies it all.
	CloneDepth int
}

// workspaceAllowlist is the egress policy a plain run enforces: the
// project's own language registries, the destinations this user granted, and
// the ones the project requested and this user accepted.
//
// The registries are permitted because a build that cannot reach its own
// package index is not a policy, it is a broken tool.
//
// `dev run`, `dev shell` and `dev console` resolve it here rather than each
// assembling their own. That was the bug: `dev agent accept` recorded a
// decision, and the plain runs — which consume the same grants and print
// the same remedy when blocked — silently left the accepted half out. Two
// commands that should agree must not be able to drift.
// agentEgress is what an agent run may reach: the project's own language
// registries, then the destinations this user granted or accepted, then
// anything added for this run.
//
// The registries are the fix for a real failure, found by running an agent
// on this repository: it could not fetch a Go module, because
// proxy.golang.org serves large zips as a signed redirect to
// storage.googleapis.com, and an agent run inherited none of the project's
// registries. A plain `dev run` had them all along. That is the same drift
// between two paths that workspaceAllowlist exists to prevent, in the
// opposite direction, so it is fixed the same way: in one place both
// callers use.
//
// An agent is asked to work on the project — build it, test it, install its
// dependencies. Withholding the package index its language needs does not
// make the agent safer, it makes it useless and teaches the user to widen
// the policy by hand.
func agentEgress(p *project.Project, hosts ...[]string) []string {
	out := p.Registries()
	for _, set := range hosts {
		out = append(out, set...)
	}
	return out
}

func workspaceAllowlist(cfg config.Config, p *project.Project, store *trust.Store) []string {
	allowed := append(p.Registries(), store.Resolve("default").AllowHosts...)
	return append(allowed,
		store.AcceptedRequest("default", projectRequest(cfg, "default"))...)
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

	pol, err := loadPolicy(env)
	if err != nil {
		return err
	}
	if o.Network != "" {
		mode, err := project.ParseNetworkMode(o.Network)
		if err != nil {
			return err
		}
		// A flag is still a request. Policy applies to the person running
		// the command, or it applies to nobody.
		if err := pol.CheckNetwork(string(mode)); err != nil {
			return err
		}
		p.Network = mode
	}
	// So is --allow-host: it widens what this run may reach, which is
	// exactly what deny_hosts is about.
	if err := pol.CheckHosts(o.ExtraHosts); err != nil {
		return err
	}
	// Limits the policy requires are applied last, so nothing lower can
	// relax them.
	if pol.Require.Memory != "" {
		cfg.MemoryLimit = pol.Require.Memory
	}
	if pol.Require.CPUs != "" {
		cfg.CPULimit = pol.Require.CPUs
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

	// Host access the user authorized. Resolved after the image is known
	// because reading the group that owns a mounted socket needs a container
	// to read it from.
	grants, err := resolveGrants(ctx, env, eng, cfg, store, image)
	if err != nil {
		return err
	}
	for _, line := range describeGrants(grants) {
		fmt.Fprintf(env.Stderr, "  granted  %s\n", line)
	}

	spec := p.RunSpec(cfg, grants, o.Command, wantTTY(o.TTY, env.Stdin))
	spec.Image = image

	if o.Clone || o.CloneDepth > 0 {
		if err := useClone(ctx, env, p, &spec, o.CloneDepth); err != nil {
			return err
		}
	}

	switch p.Network {
	case project.NetworkOpen, project.NetworkNone:
		return streamRun(ctx, env, eng, spec, o.Fallback)
	}

	// Allowlist mode: the same enforcement agents get.
	allowed := permittedHosts(env, pol,
		append(workspaceAllowlist(cfg, p, store), o.ExtraHosts...))

	if len(allowed) == 0 {
		fmt.Fprintf(env.Stderr,
			"⚠  network: allowlist, but nothing is allowed for this project.\n"+
				"   Use `--network open`, or grant destinations with `dev allow`.\n")
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
	resolved := mode.Resolve(env.stdinIsTerminal() && !workloadOwnsStdin)
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
	side, topo, err := startSidecarWithPorts(ctx, env, eng, p, allowed, resolved, p.Ports)
	if err != nil {
		return explainPortConflict(ctx, eng, err, p.Ports)
	}
	defer reportEgress(ctx, env, side, runRecord{
		Path:    history.Path(store.Project.Path()),
		Start:   time.Now(),
		Command: o.Command,
		Image:   image,
		Network: string(p.Network),
	})

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
		ask = newPrompter(env, side, store, p.Dir, pol)
		// Only one reader can own stdin. The prompt needs it to take an
		// answer, so the workload does without: in ask mode the command is
		// one that does not read input anyway, which is the same condition
		// that made asking possible.
		spec.Interactive = false
	}
	// The log follower outlives nothing: it is stopped before the deferred
	// teardown reads the same log, so the two do not race for it, and a run
	// does not leave a goroutine attached to a container that is gone.
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	watchEgress(watchCtx, env, eng, topo, ask)

	stdin := env.stdin()
	if ask != nil {
		stdin = nil
	}
	return streamRunWith(ctx, env, eng, spec, o.Fallback, stdin)
}

func streamRun(ctx context.Context, env *Env, eng *container.Engine,
	spec container.RunSpec, fallback []string) error {
	return streamRunWith(ctx, env, eng, spec, fallback, env.stdin())
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
		return &exitStatus{Code: res.ExitCode}
	}
	return nil
}

func missingCommand(stderr string) bool {
	return strings.Contains(stderr, "executable file not found") ||
		strings.Contains(stderr, "no such file or directory")
}

// startSidecar brings up the egress proxy for a workspace run.
func startSidecar(ctx context.Context, env *Env, eng *container.Engine, p *project.Project,
	allowed []string, mode EgressMode) (*netpolicy.Sidecar, netpolicy.Topology, error) {
	return startSidecarWithPorts(ctx, env, eng, p, allowed, mode, nil)
}

func startSidecarWithPorts(ctx context.Context, env *Env, eng *container.Engine, p *project.Project,
	allowed []string, mode EgressMode, ports []int) (*netpolicy.Sidecar, netpolicy.Topology, error) {
	// The image every filtered run depends on, built or refreshed here.
	// Only the agent path used to do this, so a plain run against a missing
	// sidecar failed as "exited immediately with no output" — the daemon
	// had tried to pull an image that is not published anywhere.
	image, err := ensureProxyImage(ctx, eng, env)
	if err != nil {
		return nil, netpolicy.Topology{}, err
	}

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
		Image:      image,
		Allow:      allowed,
		AskTimeout: ask,
		Forwards:   forwards,
		Ports:      ports,
		Topology: netpolicy.Topology{
			InternalNetwork: "dev-" + p.Name + "-internal",
			EgressNetwork:   "dev-" + p.Name + "-egress",
			SidecarName:     "dev-" + p.Name + "-proxy",
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

func reportEgress(ctx context.Context, env *Env, side *netpolicy.Sidecar, rec runRecord) {
	summary, err := finishRun(ctx, env, side, rec)
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
	fmt.Fprintf(env.Stderr, "Allow from now:   dev allow HOST\n")
	fmt.Fprintf(env.Stderr, "Unrestricted:     --network open\n")
	fmt.Fprintf(env.Stderr, "Later:            dev history\n")
}
