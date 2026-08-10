package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/trust"
)

func newToolsCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Tools added to this project's environment",
		Long: "Tools are recorded and the image rebuilt from that record, so the\n" +
			"environment stays reproducible and can be handed to someone else.\n" +
			"Nothing is configured in advance: add what you need when you find\n" +
			"you need it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listTools(cmd.Context(), env)
		},
	}
	cmd.AddCommand(newToolsAddCmd(env))
	cmd.AddCommand(newToolsRemoveCmd(env))
	cmd.AddCommand(newToolsListCmd(env))
	cmd.AddCommand(newToolsSearchCmd(env))
	return cmd
}

func newToolsListCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tools added to this project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listTools(cmd.Context(), env)
		},
	}
}

func listTools(ctx context.Context, env *Env) error {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	store, err := trust.Load(env.Paths.Home, p.Dir)
	if err != nil {
		return err
	}
	tools := effectiveTools(cfg, store)
	if len(tools) == 0 {
		fmt.Fprintf(env.Stdout, "No tools added.\n\n")
		fmt.Fprintf(env.Stdout, "  dev tools search <term>   find what is available\n")
		fmt.Fprintf(env.Stdout, "  dev tools add <tool>      add it, permanently\n")
		return nil
	}
	fmt.Fprintf(env.Stdout, "Tools for %s:\n", p.Name)
	for _, t := range tools {
		fmt.Fprintf(env.Stdout, "  %s\n", t)
	}
	fmt.Fprintf(env.Stdout, "\nImage:    %s\n", p.ToolsImage(tools))
	fmt.Fprintf(env.Stdout, "Recorded: %s\n", store.Project.Path())

	eng := container.New(env.driver(cfg.VMName))
	if built, err := eng.ImageExists(ctx, p.ToolsImage(tools)); err == nil && !built {
		fmt.Fprintf(env.Stdout, "\nNot built yet; the next run builds it.\n")
	}
	return nil
}

func newToolsAddCmd(env *Env) *cobra.Command {
	var shared bool
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
			if shared {
				return shareTools(cmd.Context(), env, args)
			}
			return addTools(cmd.Context(), env, args)
		},
	}
	cmd.Flags().BoolVar(&shared, "shared", false,
		"add to the project's .devenv.yaml so the team gets it too")
	return cmd
}

// addTools records tools for this machine and rebuilds the image.
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
	if err := buildTools(ctx, env, eng, p, effectiveTools(cfg, store)); err != nil {
		// A name that does not exist in the index fails here, deep in a
		// build log. Undo the record and point at search rather than
		// leaving the project with a tool that can never install.
		if _, rerr := store.RemoveTools(added); rerr == nil {
			fmt.Fprintf(env.Stderr, "\nBacked out: %s\n", strings.Join(added, " "))
			fmt.Fprintf(env.Stderr, "If the name is wrong, look it up:\n")
			fmt.Fprintf(env.Stderr, "  dev tools search %s\n", added[0])
		}
		return err
	}
	return nil
}

// shareTools writes tools into the project's own file, where they become a
// request the team shares rather than a record on one machine.
//
// The author's acceptance is recorded at the same time: they are the one
// asking, and making them accept their own edit would teach them to click
// through the prompt that protects everyone else.
func shareTools(ctx context.Context, env *Env, names []string) error {
	for _, n := range names {
		if !project.ValidToolName(n) {
			return fmt.Errorf("%q is not a plain package name; "+
				"tool names may contain letters, digits, . - _ + only", n)
		}
	}
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}

	merged := appendMissing(cfg.Tools, names)
	if err := writeProjectTools(env.Paths.Project, merged); err != nil {
		return err
	}

	store, err := trust.Load(env.Paths.Home, p.Dir)
	if err != nil {
		return err
	}
	if _, err := store.AcceptSettings([]trust.Ask{{
		Key: "tools", Value: strings.Join(merged, " "),
	}}); err != nil {
		return err
	}

	fmt.Fprintf(env.Stdout, "Added to %s:\n", env.Paths.Project)
	for _, n := range names {
		fmt.Fprintf(env.Stdout, "  + %s\n", n)
	}
	fmt.Fprintf(env.Stdout, "\nCommit it and your team gets the same environment;\n")
	fmt.Fprintf(env.Stdout, "each of them accepts it once with `dev accept`.\n\n")

	eng := container.New(env.driver(cfg.VMName))
	return buildTools(ctx, env, eng, p, effectiveTools(cfg, store))
}

func appendMissing(have, add []string) []string {
	seen := map[string]bool{}
	for _, h := range have {
		seen[h] = true
	}
	out := append([]string(nil), have...)
	for _, a := range add {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// effectiveTools combines this user's own tools with the project's, the
// latter only once accepted.
func effectiveTools(cfg config.Config, store *trust.Store) []string {
	tools := store.Tools()
	if len(cfg.Tools) == 0 {
		return tools
	}
	if store.AcceptedSettings()["tools"] != strings.Join(cfg.Tools, " ") {
		return tools
	}
	return appendMissing(tools, cfg.Tools)
}

// writeProjectTools updates the tools list in a project file.
func writeProjectTools(path string, tools []string) error {
	var b strings.Builder
	b.WriteString("# Tools the project needs. A request: each user accepts it once\n")
	b.WriteString("# with `dev accept`, because packages install during a build.\n")
	b.WriteString("tools:\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "  - %s\n", t)
	}
	return replaceBlock(path, "tools:", b.String())
}

// replaceBlock rewrites one top-level block of a YAML file and leaves
// everything else untouched, comments included. It is the team's file and
// may hold anything, so rewriting it wholesale would discard work.
//
// Comment lines directly above the block go with it: a heading left over a
// block that moved reads as a description of whatever follows it.
func replaceBlock(path, key, block string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(string(raw), "\n")

	var out []string
	var pendingComments []string
	skipping := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipping {
			// The block continues through indented lines and blanks.
			if line != trimmed && trimmed != "" {
				continue
			}
			if trimmed == "" {
				continue
			}
			skipping = false
		}
		if strings.HasPrefix(line, key) {
			skipping = true
			pendingComments = nil
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			pendingComments = append(pendingComments, line)
			continue
		}
		out = append(out, pendingComments...)
		pendingComments = nil
		out = append(out, line)
	}
	out = append(out, pendingComments...)

	body := strings.TrimRight(strings.Join(out, "\n"), "\n")
	var b strings.Builder
	if body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	b.WriteString(block)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func newToolsRemoveCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <tool>...",
		Short: "Remove a tool from this project's environment",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
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

func newToolsSearchCmd(env *Env) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <term>",
		Short: "Search the image's package index for a tool",
		Long: "Asks the package manager inside this project's image what it has,\n" +
			"so a name can be looked up rather than guessed at and a failed\n" +
			"build used as the error message.\n\n" +
			"The search runs unfiltered, like an image build: it reads a\n" +
			"package index, which is the same path an install takes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return searchTools(cmd.Context(), env, args[0], limit)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 25, "maximum results")
	return cmd
}

// searchScript asks whichever package manager the image has. The image
// family is not known in advance: it comes from the project, or from
// whatever base a language template chose.
func searchScript(term string, limit int) string {
	return fmt.Sprintf(`
if command -v apt-get >/dev/null 2>&1; then
  apt-get update -qq >/dev/null 2>&1 || true
  apt-cache search --names-only %[1]s 2>/dev/null | head -n %[2]d
elif command -v apk >/dev/null 2>&1; then
  apk update -q >/dev/null 2>&1 || true
  apk search -q %[1]s 2>/dev/null | head -n %[2]d
elif command -v dnf >/dev/null 2>&1; then
  dnf -q search %[1]s 2>/dev/null | head -n %[2]d
else
  echo "no supported package manager in this image" >&2
  exit 1
fi`, shellQuote(term), limit)
}

// shellQuote makes a search term safe inside the script above.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func searchTools(ctx context.Context, env *Env, term string, limit int) error {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	eng := container.New(env.driver(cfg.VMName))

	image := p.Image
	if exists, err := eng.ImageExists(ctx, image); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("the project image is not built yet; run `dev build` first")
	}

	fmt.Fprintf(env.Stdout, "Searching %s for %q…\n\n", image, term)

	// Root, because a package index refresh writes to /var/lib. This
	// container reads an index and exits; it mounts nothing and keeps
	// nothing.
	spec := container.RunSpec{
		Image:   image,
		User:    "0:0",
		Remove:  true,
		Command: []string{"/bin/sh", "-c", searchScript(term, limit)},
		Labels:  map[string]string{"dev.role": "tool-search"},
	}
	res, err := eng.Run(ctx, spec, nil, env.Stdout, env.Stderr)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("search failed with status %d", res.ExitCode)
	}
	fmt.Fprintf(env.Stdout, "\nAdd one with: dev tools add <name>\n")
	return nil
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
		return fmt.Errorf("build the project image first: dev build")
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
	p *project.Project, store *trust.Store, cfg config.Config) (string, error) {
	tools := effectiveTools(cfg, store)
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
