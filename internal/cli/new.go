package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/scaffold"
)

func newNewCmd(env *Env) *cobra.Command {
	var (
		version string
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "new <language> [directory]",
		Short: "Start a new project from a language plugin",
		Long: "Creates the plugin's scaffolding files. It does not write a\n" +
			"Dockerfile: the language template is rendered at build time and\n" +
			"stays current, whereas a copy in the project is a copy that goes\n" +
			"stale. Write one when you want to change it — `dev build` uses a\n" +
			"project Dockerfile in preference to the template.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 2 {
				dir = args[1]
			}
			return newProject(env, args[0], dir, version, force)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "language version (default: the plugin's newest)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")

	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveFilterDirs
		}
		set, err := loadLanguages(env)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, l := range set.All() {
			out = append(out, cobra.CompletionWithDesc(l.Name, l.DisplayName))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

func newProject(env *Env, language, dir, version string, force bool) error {
	set, err := loadLanguages(env)
	if err != nil {
		return err
	}
	l, ok := set.Get(language)
	if !ok {
		return fmt.Errorf("unknown language %q; available: %s",
			language, strings.Join(set.Names(), ", "))
	}

	if version == "" {
		version = l.DefaultVersion()
	} else if !l.HasVersion(version) {
		// A version the plugin does not know produces an image tag that
		// does not exist, and the failure arrives much later, in a build.
		return fmt.Errorf("%s has no version %q; it declares %s",
			l.Name, version, strings.Join(l.Versions, ", "))
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}

	plan, err := scaffold.Build(l, abs, scaffold.Vars{
		ProjectName: project.SanitizeName(filepath.Base(abs)),
		Version:     version,
	})
	if err != nil {
		return err
	}
	if err := plan.Apply(force); err != nil {
		return err
	}

	fmt.Fprintf(env.Stdout, "Created a %s %s project in %s\n\n", l.Name, version, abs)
	for _, path := range plan.Paths() {
		fmt.Fprintf(env.Stdout, "  %s\n", path)
	}
	for _, missing := range plan.Missing {
		// The plugin said it ships this and does not. Inventing content
		// would be a surprise attributed to the plugin.
		fmt.Fprintf(env.Stderr, "  ⚠ %s: declared by the plugin but not shipped\n", missing)
	}

	fmt.Fprintf(env.Stdout, "\nNext:\n")
	if dir != "." {
		fmt.Fprintf(env.Stdout, "  cd %s\n", dir)
	}
	fmt.Fprintf(env.Stdout, "  dev run -c '<command>'   build and run, sandboxed\n")
	fmt.Fprintf(env.Stdout, "  dev pin                  fix the base image to a digest\n")
	return nil
}
