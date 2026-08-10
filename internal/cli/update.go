package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/trust"
)

func newUpdateCmd(env *Env) *cobra.Command {
	var (
		withScan bool
		keepPins bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Move this project's image to current patched versions",
		Long: "Re-resolves the base images to what their tags point at now, then\n" +
			"rebuilds without cache so package installs fetch current versions.\n\n" +
			"Pinning and updating are the same trade seen from opposite ends.\n" +
			"A pin fixes what a build fetches, which is what makes a build\n" +
			"reproducible and also what stops security updates arriving. This\n" +
			"is the command that moves the pin on purpose, and says what moved,\n" +
			"so a project can be left pinned safely.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd.Context(), env, withScan, keepPins)
		},
	}
	cmd.Flags().BoolVar(&withScan, "scan", false,
		"scan the image afterwards, to see whether it helped")
	cmd.Flags().BoolVar(&keepPins, "keep-pins", false,
		"rebuild without moving the pins: packages update, the base image does not")
	return cmd
}

func runUpdate(ctx context.Context, env *Env, withScan, keepPins bool) error {
	if !keepPins {
		fmt.Fprintln(env.Stdout, "Base images")
		// Pull chatter belongs to the pull, not to the report of what
		// moved; the interesting output is three lines at the end.
		changes, err := resolvePins(ctx, env, true, io.Discard)
		if err != nil {
			return err
		}
		switch {
		case len(changes) == 0:
			fmt.Fprintln(env.Stdout, "  already current")
		default:
			// What moved is the point: a pinned project is only safe to
			// leave pinned if someone can see when it last advanced.
			for _, c := range changes {
				if c.Old == "" {
					fmt.Fprintf(env.Stdout, "  pinned %s\n    %s\n", c.Image, c.New)
					continue
				}
				fmt.Fprintf(env.Stdout, "  moved %s\n    from %s\n    to   %s\n",
					c.Image, c.Old, c.New)
			}
			fmt.Fprintf(env.Stdout, "\n  Recorded in %s — commit it.\n", env.Paths.Project)
		}
	}

	// Turn on package upgrades and record it. Applying it once and not
	// recording it would leave the next plain build quietly reintroducing
	// what this command just fixed.
	if err := setUpgradePackages(env.Paths.Project, true); err != nil {
		return err
	}

	// Reload: the pins and the upgrade flag just changed on disk.
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	eng := container.New(env.driver(cfg.VMName))

	fmt.Fprintln(env.Stdout, "\nPackages")
	fmt.Fprintln(env.Stdout, "  upgrading what the base image shipped with")
	fmt.Fprintln(env.Stdout, "\nRebuilding without cache")
	if err := buildImageNoCache(ctx, env, cfg, p); err != nil {
		return err
	}

	// The tools layer installs packages of its own, and they are as stale
	// as anything else in the image.
	store, err := trust.Load(env.Paths.Home, p.Dir)
	if err != nil {
		return err
	}
	if tools := effectiveTools(cfg, store); len(tools) > 0 {
		fmt.Fprintf(env.Stdout, "\nRebuilding tools: %s\n", strings.Join(tools, ", "))
		if err := eng.BuildWithDockerfileStdin(ctx, container.BuildSpec{
			Tag:     p.ToolsImage(tools),
			NoCache: true,
		}, project.ToolsDockerfile(p.Image, tools), env.Stdout); err != nil {
			return err
		}
	}

	fmt.Fprintln(env.Stdout, "\nUpdated.")
	if !withScan {
		fmt.Fprintln(env.Stdout, "Check what it fixed with: dev scan")
		return nil
	}
	fmt.Fprintln(env.Stdout)
	return runScan(ctx, env, "high", "", true, false)
}

// setUpgradePackages records that builds should upgrade the base image's
// own packages.
//
// Recorded rather than applied once: a later plain build would otherwise
// drop the upgrade and quietly reintroduce what this command just fixed.
func setUpgradePackages(path string, on bool) error {
	block := "# Upgrade the base image's own packages on every build. Set by\n" +
		"# `dev update`: most of what a scanner reports lives here, and\n" +
		"# neither a new base digest nor a cacheless rebuild touches it.\n" +
		fmt.Sprintf("upgrade_packages: %t\n", on)
	return replaceBlock(path, "upgrade_packages:", block)
}
