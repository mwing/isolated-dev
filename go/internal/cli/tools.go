package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/container"
	"github.com/mwing/isolated-dev/go/internal/project"
	"github.com/mwing/isolated-dev/go/internal/trust"
)

func newAddCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <tool>...",
		Short: "Add a tool to this project's environment, persistently",
		Long: "Records the tool and rebuilds the image with it. The record is a\n" +
			"declaration, not a mutated container: `docker commit` would give an\n" +
			"image nobody can reproduce and an environment that exists only on\n" +
			"the machine where the command was typed.\n\n" +
			"It is stored outside the repository, so it is yours until you\n" +
			"choose to share it.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return addTools(cmd.Context(), env, args)
		},
	}
	return cmd
}

func addTools(ctx context.Context, env *Env, names []string) error {
	for _, n := range names {
		if !project.ValidToolName(n) {
			// The list is interpolated into a RUN line during the build.
			return fmt.Errorf("%q is not a plain package name; "+
				"tool names may contain letters, digits, . - _ + only", n)
		}
	}

	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	store, err := trust.Load(env.Paths.Home, p.Dir)
	if err != nil {
		return err
	}

	added, err := store.AddTools(names)
	if err != nil {
		return err
	}
	if len(added) == 0 {
		fmt.Fprintln(env.Stdout, "Already present.")
		return nil
	}
	for _, n := range added {
		fmt.Fprintf(env.Stdout, "  + %s\n", n)
	}
	fmt.Fprintf(env.Stdout, "\nRecorded in %s\n\n", store.Project.Path())

	eng := container.New(env.driver(cfg.VMName))
	return buildTools(ctx, env, eng, p, store.Tools())
}

func newRemoveToolCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <tool>...",
		Short: "Remove a tool from this project's environment",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := resolveProject(env)
			if err != nil {
				return err
			}
			store, err := trust.Load(env.Paths.Home, p.Dir)
			if err != nil {
				return err
			}
			removed, err := store.RemoveTools(args)
			if err != nil {
				return err
			}
			if len(removed) == 0 {
				fmt.Fprintln(env.Stdout, "Nothing to remove.")
				return nil
			}
			for _, n := range removed {
				fmt.Fprintf(env.Stdout, "  - %s\n", n)
			}
			fmt.Fprintln(env.Stdout, "\nThe image rebuilds on the next run.")
			return nil
		},
	}
}

func newToolsCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "List tools added to this project's environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, p, err := resolveProject(env)
			if err != nil {
				return err
			}
			store, err := trust.Load(env.Paths.Home, p.Dir)
			if err != nil {
				return err
			}
			tools := store.Tools()
			if len(tools) == 0 {
				fmt.Fprintf(env.Stdout, "No tools added. `dev2 add <tool>` records one "+
					"and rebuilds.\n")
				return nil
			}
			fmt.Fprintf(env.Stdout, "Tools for %s:\n", p.Name)
			for _, t := range tools {
				fmt.Fprintf(env.Stdout, "  %s\n", t)
			}
			fmt.Fprintf(env.Stdout, "\nImage:    %s\n", p.ToolsImage(tools))
			fmt.Fprintf(env.Stdout, "Recorded: %s\n", store.Project.Path())

			eng := container.New(env.driver(cfg.VMName))
			if built, err := eng.ImageExists(cmd.Context(), p.ToolsImage(tools)); err == nil && !built {
				fmt.Fprintf(env.Stdout, "\nNot built yet; the next run builds it.\n")
			}
			return nil
		},
	}
}

// buildTools builds the derived image carrying the project's tools.
func buildTools(ctx context.Context, env *Env, eng *container.Engine,
	p *project.Project, tools []string) error {
	if len(tools) == 0 {
		return nil
	}
	tag := p.ToolsImage(tools)

	base, err := eng.ImageExists(ctx, p.Image)
	if err != nil {
		return err
	}
	if !base {
		return fmt.Errorf("build the project image first: dev2 build")
	}

	fmt.Fprintf(env.Stdout, "Adding %s to %s\n", strings.Join(tools, ", "), p.Image)
	return eng.BuildWithDockerfileStdin(ctx, container.BuildSpec{Tag: tag},
		project.ToolsDockerfile(p.Image, tools), env.Stdout)
}

// ensureTools returns the image a run should use, building the tools layer
// if it is missing. Tools are added when a need appears rather than
// configured in advance, so the build has to happen on the next run rather
// than being demanded of the user.
func ensureTools(ctx context.Context, env *Env, eng *container.Engine,
	p *project.Project, store *trust.Store) (string, error) {
	tools := store.Tools()
	if len(tools) == 0 {
		return p.Image, nil
	}
	tag := p.ToolsImage(tools)
	exists, err := eng.ImageExists(ctx, tag)
	if err != nil {
		return "", err
	}
	if exists {
		return tag, nil
	}
	if err := buildTools(ctx, env, eng, p, tools); err != nil {
		return "", err
	}
	return tag, nil
}
