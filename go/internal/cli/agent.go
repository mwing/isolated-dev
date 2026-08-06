package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/agent"
	"github.com/mwing/isolated-dev/go/internal/backend/orbstack"
	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/container"
	"github.com/mwing/isolated-dev/go/internal/netpolicy"
)

func newAgentCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run a coding agent inside the sandbox",
		Long: "Agents run at the untrusted level regardless of the project's\n" +
			"trust: they act on instructions from a model, so they do not\n" +
			"inherit a human's decision to trust this project.",
	}
	cmd.AddCommand(newAgentListCmd(env))
	cmd.AddCommand(newAgentRunCmd(env))
	cmd.AddCommand(newAgentLogoutCmd(env))
	cmd.AddCommand(newAgentPolicyCmd(env))
	return cmd
}

func registry(env *Env) (*agent.Registry, error) {
	r := agent.NewRegistry()
	if err := r.LoadDir(filepath.Join(env.Paths.Home, "agents")); err != nil {
		return nil, err
	}
	return r, nil
}

func newAgentListCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available agents",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			r, err := registry(env)
			if err != nil {
				return err
			}
			for _, a := range r.List() {
				pin := a.Version
				if !a.Pinned() {
					pin += " (unpinned)"
				}
				fmt.Fprintf(env.Stdout, "%-10s %-28s %s\n", a.Name, a.Description, pin)
				fmt.Fprintf(env.Stdout, "           egress: %s\n", strings.Join(a.AllowHosts, " "))
				fmt.Fprintf(env.Stdout, "           source: %s\n", a.Source())
			}
			return nil
		},
	}
}

func newAgentPolicyCmd(env *Env) *cobra.Command {
	var extra []string
	cmd := &cobra.Command{
		Use:   "policy <agent>",
		Short: "Show the egress policy a run would enforce, without running it",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			r, err := registry(env)
			if err != nil {
				return err
			}
			a, err := r.Get(args[0])
			if err != nil {
				return err
			}
			opts := agent.Options{Agent: a, ExtraHosts: extra}
			allow, err := netpolicy.Parse(opts.Allowlist())
			if err != nil {
				return err
			}

			fmt.Fprintf(env.Stdout, "Egress policy for %s:\n", a.Name)
			for _, rule := range allow.Rules() {
				fmt.Fprintf(env.Stdout, "  allow %s\n", rule.String())
			}
			fmt.Fprintf(env.Stdout, "\nEverything else is denied. The container has no route out\n")
			fmt.Fprintf(env.Stdout, "except the proxy, so a client ignoring HTTP_PROXY fails too.\n")
			fmt.Fprintf(env.Stdout, "\nNote: allowlisted hosts that accept writes (git remotes,\n")
			fmt.Fprintf(env.Stdout, "package registries) remain viable exfiltration channels, and\n")
			fmt.Fprintf(env.Stdout, "%s's own API endpoint is bidirectional by nature.\n", a.Name)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&extra, "allow-host", nil, "additional destination for this run")
	return cmd
}

func newAgentLogoutCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "logout <agent>",
		Short: "Remove an agent's stored credentials",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := registry(env)
			if err != nil {
				return err
			}
			a, err := r.Get(args[0])
			if err != nil {
				return err
			}
			cfg, err := config.Load(env.Paths, env.Env)
			if err != nil {
				return err
			}
			eng := container.New(orbstack.New(cfg.VMName, env.Runner))
			runner := &agent.Runner{Engine: eng, Out: env.Stdout}
			if err := runner.Logout(cmd.Context(), a); err != nil {
				return err
			}
			fmt.Fprintf(env.Stdout, "Removed %s (volume %s).\n", a.Name, a.VolumeName())
			return nil
		},
	}
}

func newAgentRunCmd(env *Env) *cobra.Command {
	var (
		extraHosts []string
		image      string
		authMode   string
		authEnv    []string
		rebuild    bool
		memory     string
		cpus       string
		dryRun     bool
		tty        string
		notify     string
		safe       bool
	)

	cmd := &cobra.Command{
		Use:   "run <agent> [-- args...]",
		Short: "Run an agent against the current project",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := registry(env)
			if err != nil {
				return err
			}
			a, err := r.Get(args[0])
			if err != nil {
				return err
			}

			cfg, err := config.Load(env.Paths, env.Env)
			if err != nil {
				return err
			}

			opts := agent.Options{
				Agent:      a,
				Project:    env.Paths.ProjectDir,
				ExtraHosts: extraHosts,
				AuthMode:   authMode,
				Image:      image,
				// A TTY is only valid when stdin is one AND the backend
				// transport carries it through. Auto-detection covers the
				// common cases; --tty overrides it when it guesses wrong.
				Interactive: wantTTY(tty, os.Stdin),
				Memory:      memory,
				CPUs:        cpus,
				Command:     args[1:],
			}

			if authMode == "env" {
				resolved, err := resolveAuthEnv(a, authEnv, env.Env)
				if err != nil {
					return err
				}
				opts.AuthEnv = resolved
			}

			return runAgent(cmd.Context(), env, cfg, opts, rebuild, dryRun, notify)
		},
	}

	cmd.Flags().StringArrayVar(&extraHosts, "allow-host", nil,
		"add a destination to this run's allowlist")
	cmd.Flags().StringVar(&image, "image", "", "project image to overlay (default: the agent's base)")
	cmd.Flags().StringVar(&authMode, "auth", "volume", "auth mode: volume or env")
	cmd.Flags().StringArrayVar(&authEnv, "auth-env", nil,
		"environment variable name to pass in env auth mode (value taken from your environment)")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "rebuild the agent image")
	cmd.Flags().StringVar(&memory, "memory", "", "memory limit, e.g. 4g")
	cmd.Flags().StringVar(&cpus, "cpus", "", "CPU limit, e.g. 2")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"print the policy and the exact docker invocation, then stop")
	cmd.Flags().StringVar(&tty, "tty", "auto", "allocate a terminal: auto, on, or off")
	cmd.Flags().BoolVar(&safe, "safe", false,
		"keep the agent's own permission prompts instead of auto-approving inside the sandbox")
	cmd.Flags().StringVar(&notify, "egress-notify", "live",
		"report blocked destinations: live (as they happen) or off (summary only)")
	return cmd
}

// resolveAuthEnv turns requested variable names into NAME=VALUE pairs,
// reading only the names the user asked for. Nothing is passed implicitly,
// and an unset variable is an error rather than a silent empty value.
func resolveAuthEnv(a *agent.Agent, requested []string, environ []string) ([]string, error) {
	names := requested
	if len(names) == 0 {
		names = a.AuthEnv
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("--auth env needs --auth-env NAME (agent %s declares none)", a.Name)
	}

	lookup := map[string]string{}
	for _, kv := range environ {
		if i := strings.IndexByte(kv, '='); i > 0 {
			lookup[kv[:i]] = kv[i+1:]
		}
	}

	var out []string
	for _, n := range names {
		v, ok := lookup[n]
		if !ok || v == "" {
			return nil, fmt.Errorf("--auth env: %s is not set in your environment", n)
		}
		out = append(out, n+"="+v)
	}
	return out, nil
}

func runAgent(ctx context.Context, env *Env, cfg config.Config, opts agent.Options, rebuild, dryRun bool, notify string) error {
	a := opts.Agent
	eng := container.New(orbstack.New(cfg.VMName, env.Runner))

	allowEntries := opts.Allowlist()
	allow, err := netpolicy.Parse(allowEntries)
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Stdout, "Agent:     %s", a.Name)
	if !a.Pinned() {
		fmt.Fprintf(env.Stdout, "  ⚠ unpinned version")
	}
	fmt.Fprintln(env.Stdout)
	fmt.Fprintf(env.Stdout, "Project:   %s\n", opts.Project)
	fmt.Fprintf(env.Stdout, "Egress:    %s\n", strings.Join(allowEntries, " "))
	fmt.Fprintf(env.Stdout, "Auth:      %s\n", authDescription(opts))
	fmt.Fprintln(env.Stdout)

	slug := projectSlug(opts.Project)
	topo := netpolicy.Topology{
		InternalNetwork: "dev2-" + slug + "-internal",
		EgressNetwork:   "dev2-" + slug + "-egress",
		SidecarName:     "dev2-" + slug + "-proxy",
		ProxyPort:       3128,
		DNSPort:         53,
	}

	if dryRun {
		spec := agent.Spec(opts, netpolicy.Topology{
			InternalNetwork: topo.InternalNetwork, SidecarIP: "<sidecar>", ProxyPort: 3128,
		})
		fmt.Fprintf(env.Stdout, "Would run:\n  docker run %s\n\n", strings.Join(spec.Args(), " "))
		fmt.Fprintf(env.Stdout, "Allowlist rules:\n")
		for _, rule := range allow.Rules() {
			fmt.Fprintf(env.Stdout, "  %s\n", rule.String())
		}
		return nil
	}

	if _, err := (&agent.Runner{Engine: eng, Out: env.Stdout}).EnsureImage(ctx, opts, rebuild); err != nil {
		return err
	}
	runner := &agent.Runner{Engine: eng, Out: env.Stdout}
	if err := runner.EnsureVolume(ctx, a); err != nil {
		return err
	}

	proxyImage, err := ensureProxyImage(ctx, eng, env)
	if err != nil {
		return err
	}

	side := &netpolicy.Sidecar{
		Engine:   eng,
		Image:    proxyImage,
		Allow:    allowEntries,
		Topology: topo,
	}
	live, err := side.Start(ctx)
	if err != nil {
		return err
	}
	defer func() {
		summary, err := side.Stop(context.WithoutCancel(ctx))
		if err != nil {
			fmt.Fprintf(env.Stderr, "\nwarning: reading egress log: %v\n", err)
			return
		}
		fmt.Fprint(env.Stderr, "\r\n")
		if len(summary) == 0 {
			fmt.Fprintln(env.Stderr, "Egress: nothing blocked.")
			return
		}
		fmt.Fprintln(env.Stderr, "Egress: blocked destinations this run:")
		for _, line := range summary {
			fmt.Fprintf(env.Stderr, "  %s\n", line)
		}
		fmt.Fprintf(env.Stderr, "To permit one of these, re-run with --allow-host HOST\n")
	}()

	// Live egress notices. A denial mid-run is actionable — the user can
	// decide whether to allow it — but only while it is still happening.
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	var watcher *netpolicy.Watcher
	if notify != "off" {
		watcher = netpolicy.NewWatcher(func(n netpolicy.Notice) {
			fmt.Fprintf(env.Stderr, "\r\n  \u26d4 egress %s\n", n.String())
		})
		pr, pw := io.Pipe()
		go func() {
			defer pw.Close()
			_ = eng.LogsFollow(watchCtx, live.SidecarName, pw)
		}()
		go func() { _ = watcher.Run(pr) }()
	}

	spec := agent.Spec(opts, live)
	var errBuf strings.Builder
	res, err := eng.Run(ctx, spec, os.Stdin, env.Stdout, io.MultiWriter(env.Stderr, &errBuf))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		if hint := explainTTYFailure(errBuf.String()); hint != "" {
			return fmt.Errorf("%s could not start: %s", a.Name, hint)
		}
		return fmt.Errorf("%s exited with status %d", a.Name, res.ExitCode)
	}
	return nil
}

func authDescription(o agent.Options) string {
	if o.AuthMode == "env" {
		var names []string
		for _, kv := range o.AuthEnv {
			names = append(names, strings.SplitN(kv, "=", 2)[0])
		}
		return "env (" + strings.Join(names, ", ") + ")"
	}
	return "volume " + o.Agent.VolumeName() + " (no host credentials)"
}

// projectSlug derives a docker-safe name fragment from a path.
func projectSlug(path string) string {
	base := strings.ToLower(filepath.Base(path))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "project"
	}
	if len(s) > 30 {
		s = s[:30]
	}
	return s
}

// proxyImageTag is the image carrying the egress sidecar.
const proxyImageTag = "dev2-proxy:latest"

// ensureProxyImage builds the sidecar image if it is missing. The binary is
// compiled inside the build so the host needs no cross-compilation setup,
// and the runtime layer is scratch: the sidecar has no shell, no package
// manager and nothing to pivot to if it were ever compromised.
func ensureProxyImage(ctx context.Context, eng *container.Engine, env *Env) (string, error) {
	exists, err := eng.ImageExists(ctx, proxyImageTag)
	if err != nil {
		return "", err
	}
	if exists {
		return proxyImageTag, nil
	}
	return "", fmt.Errorf(
		"egress sidecar image %s is missing; build it with `make proxy-image` from the go/ directory",
		proxyImageTag)
}

// isTerminal reports whether f is an interactive terminal.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// wantTTY resolves the --tty setting. Auto-detection is a heuristic: stdin
// can look like a terminal while the backend transport still does not carry
// one through (orb -m <vm> sudo docker is one such case), so the override
// exists for when the guess is wrong in either direction.
func wantTTY(mode string, stdin *os.File) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on", "true", "yes", "1":
		return true
	case "off", "false", "no", "0":
		return false
	default:
		return isTerminal(stdin)
	}
}

// explainTTYFailure recognizes docker's TTY error and says what to do about
// it, rather than leaving the user with a bare message from three layers
// down.
func explainTTYFailure(output string) string {
	if !strings.Contains(output, "input device is not a TTY") {
		return ""
	}
	return "the backend did not pass a terminal through; re-run with --tty off " +
		"(non-interactive) or from a real terminal"
}
