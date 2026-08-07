package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/container"
	"github.com/mwing/isolated-dev/go/internal/netpolicy"
	"github.com/mwing/isolated-dev/go/internal/project"
	"github.com/mwing/isolated-dev/go/internal/runner"
	"github.com/mwing/isolated-dev/go/internal/trust"
)

func newStatusCmd(env *Env) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what is running, under which policy",
		Long: "Replaces v1's `list`, which showed images. What matters is what\n" +
			"is running, in which project, what it is allowed to reach, and\n" +
			"what it has been stopped from reaching.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return showStatus(cmd.Context(), env, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include projects other than this one")
	return cmd
}

func showStatus(ctx context.Context, env *Env, all bool) error {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	eng := container.New(env.driver(cfg.VMName))

	fmt.Fprintf(env.Stdout, "Project:  %s (%s)\n", p.Name, p.Dir)
	fmt.Fprintf(env.Stdout, "Image:    %s", p.Image)
	if exists, err := eng.ImageExists(ctx, p.Image); err == nil && !exists {
		fmt.Fprintf(env.Stdout, "  (not built)")
	}
	fmt.Fprintln(env.Stdout)

	fmt.Fprintf(env.Stdout, "Network:  %s (%s)\n", p.Network, cfg.Origin("network"))
	if p.Network == project.NetworkAllowlist {
		store, err := trust.Load(env.Paths.Home, p.Dir)
		if err != nil {
			return err
		}
		reportAllowed(env, p, store)
	}

	running, err := eng.List(ctx, "dev2.role")
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Stdout, "\nContainers\n")
	shown := 0
	for _, c := range running {
		mine := c.Label("dev2.project") == p.Name ||
			strings.Contains(c.Names, p.Name)
		if !all && !mine {
			continue
		}
		shown++
		fmt.Fprintf(env.Stdout, "  %-28s %-10s %-22s %s\n",
			c.Names, c.Label("dev2.role"), truncate(c.Image, 22), c.Status)
	}
	if shown == 0 {
		fmt.Fprintln(env.Stdout, "  none running")
		if !all {
			fmt.Fprintln(env.Stdout, "  (use --all for other projects)")
		}
	}

	// A sidecar still up means a run is in progress; its log is the live
	// answer to "what has this been stopped from reaching?".
	for _, c := range running {
		if c.Label("dev2.role") != "egress-sidecar" || !strings.HasPrefix(c.State, "running") {
			continue
		}
		logs, err := eng.Logs(ctx, c.Names)
		if err != nil {
			continue
		}
		summary := netpolicy.Summary(netpolicy.ParseDenials(logs))
		if len(summary) == 0 {
			continue
		}
		fmt.Fprintf(env.Stdout, "\nBlocked so far (%s)\n", c.Names)
		for _, line := range summary {
			fmt.Fprintf(env.Stdout, "  %s\n", line)
		}
	}
	return nil
}

func reportAllowed(env *Env, p *project.Project, store *trust.Store) {
	registries := p.Registries()
	granted := store.Resolve("default").AllowHosts

	if len(registries) > 0 {
		fmt.Fprintf(env.Stdout, "  registries: %s\n", strings.Join(registries, " "))
	}
	if len(granted) > 0 {
		fmt.Fprintf(env.Stdout, "  granted:    %s\n", strings.Join(granted, " "))
	}
	if len(registries) == 0 && len(granted) == 0 {
		fmt.Fprintf(env.Stdout, "  nothing allowed — runs will have no egress\n")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func newCleanCmd(env *Env) *cobra.Command {
	var images bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove this project's containers, networks and sidecar",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clean(cmd.Context(), env, images)
		},
	}
	cmd.Flags().BoolVar(&images, "images", false, "also remove the project image")
	return cmd
}

func clean(ctx context.Context, env *Env, images bool) error {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	eng := container.New(env.driver(cfg.VMName))

	// Containers first: a network cannot be removed while something is
	// attached, so removing them in the other order fails confusingly.
	containers, err := eng.List(ctx, "dev2.project="+p.Name)
	if err != nil {
		return err
	}
	sidecar := "dev2-" + p.Name + "-proxy"
	names := []string{p.Container, sidecar}
	for _, c := range containers {
		names = append(names, c.Names)
	}
	for _, name := range dedupe(names) {
		if err := eng.Remove(ctx, name); err != nil {
			fmt.Fprintf(env.Stderr, "  ⚠ %v\n", err)
			continue
		}
		fmt.Fprintf(env.Stdout, "  removed container %s\n", name)
	}

	nets, err := eng.Networks(ctx, "dev2-"+p.Name+"-")
	if err != nil {
		return err
	}
	for _, n := range nets {
		if err := eng.NetworkRemove(ctx, n); err != nil {
			fmt.Fprintf(env.Stderr, "  ⚠ %v\n", err)
			continue
		}
		fmt.Fprintf(env.Stdout, "  removed network %s\n", n)
	}

	if images {
		if err := eng.RemoveImage(ctx, p.Image); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "  removed image %s\n", p.Image)
	} else {
		fmt.Fprintf(env.Stdout, "  kept image %s (use --images to remove)\n", p.Image)
	}

	// The agent's home volume is deliberately untouched: it holds a login
	// the user would have to redo, and `dev2 agent logout` exists to
	// discard it on purpose.
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// vmCommands manages the OrbStack VM the containers run in.
func newEnvCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vm",
		Short: "Inspect and control the container VM",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the VM if it is not running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(env.Paths, env.Env)
			if err != nil {
				return err
			}
			return startVM(cmd.Context(), env, cfg)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Report VM and daemon state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(env.Paths, env.Env)
			if err != nil {
				return err
			}
			st, err := env.driver(cfg.VMName).Probe(cmd.Context())
			if err != nil {
				return err
			}
			return reportVM(env, st.Backend, st.VMName, st.VMExists, st.VMRunning, st.DaemonUp, st.Detail)
		},
	})
	return cmd
}

// startVM starts the VM. Unlike v1, which called `orb start` and assumed
// success, the result is checked and reported.
func startVM(ctx context.Context, env *Env, cfg config.Config) error {
	drv := env.driver(cfg.VMName)
	st, err := drv.Probe(ctx)
	if err != nil {
		return err
	}
	if !st.CLIFound {
		return fmt.Errorf("orb CLI not found: %s", st.Detail)
	}
	if st.VMRunning {
		fmt.Fprintf(env.Stdout, "VM %s is already running.\n", cfg.VMName)
		return nil
	}
	if !st.VMExists {
		return fmt.Errorf("VM %s does not exist; create it with OrbStack first", cfg.VMName)
	}

	res, err := env.Runner.Run(ctx, runner.Command{Path: "orb", Args: []string{"start", cfg.VMName}})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("starting VM %s: %s", cfg.VMName, strings.TrimSpace(res.Stderr))
	}
	fmt.Fprintf(env.Stdout, "Started VM %s.\n", cfg.VMName)
	return nil
}

func reportVM(env *Env, backend, name string, exists, running, daemon bool, detail string) error {
	fmt.Fprintf(env.Stdout, "backend: %s\n", backend)
	fmt.Fprintf(env.Stdout, "vm:      %s\n", name)
	fmt.Fprintf(env.Stdout, "%s  exists\n", mark(exists))
	fmt.Fprintf(env.Stdout, "%s  running\n", mark(running))
	fmt.Fprintf(env.Stdout, "%s  docker daemon\n", mark(daemon))
	if detail != "" {
		fmt.Fprintf(env.Stdout, "→  %s\n", detail)
	}
	return nil
}
