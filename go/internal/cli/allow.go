package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/netpolicy"
	"github.com/mwing/isolated-dev/go/internal/trust"
)

// scopeOf picks the file a command writes to.
func scopeOf(s *trust.Store, global bool) *trust.File {
	if global {
		return s.Global
	}
	return s.Project
}

func newAgentAllowCmd(env *Env) *cobra.Command {
	var global bool
	var agentName string

	cmd := &cobra.Command{
		Use:   "allow <host>...",
		Short: "Grant egress destinations for this project, persistently",
		Long: "Records destinations so they need not be repeated as --allow-host\n" +
			"on every run.\n\n" +
			"Grants are stored under ~/.dev-envs, keyed by project path, never\n" +
			"inside the project. Configuration in a repository is configuration\n" +
			"the repository can grant itself, so a clone could widen its own\n" +
			"egress before anyone read it.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			// Validate before recording: a typo saved now becomes a
			// confusing failure on some later run.
			if _, err := netpolicy.Parse(args); err != nil {
				return err
			}
			store, err := trust.Load(env.Paths.Home, env.Paths.ProjectDir)
			if err != nil {
				return err
			}
			scope := scopeOf(store, global)
			added, err := store.Grant(scope, agentName, args)
			if err != nil {
				return err
			}

			where := "this project"
			if global {
				where = "every project"
			}
			if len(added) == 0 {
				fmt.Fprintf(env.Stdout, "Already granted for %s.\n", where)
				return nil
			}
			fmt.Fprintf(env.Stdout, "Granted for %s (agent: %s):\n", where, agentOrDefault(agentName))
			for _, h := range added {
				fmt.Fprintf(env.Stdout, "  + %s\n", h)
			}
			fmt.Fprintf(env.Stdout, "\nStored in %s\n", scope.Path())
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "grant for every project")
	cmd.Flags().StringVar(&agentName, "agent", "default",
		"agent this applies to, or 'default' for all")
	return cmd
}

func newAgentRevokeCmd(env *Env) *cobra.Command {
	var global bool
	var agentName string

	cmd := &cobra.Command{
		Use:   "revoke <host>...",
		Short: "Remove previously granted egress destinations",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			store, err := trust.Load(env.Paths.Home, env.Paths.ProjectDir)
			if err != nil {
				return err
			}
			removed, err := store.Revoke(scopeOf(store, global), agentName, args)
			if err != nil {
				return err
			}
			if len(removed) == 0 {
				fmt.Fprintln(env.Stdout, "Nothing to revoke.")
				return nil
			}
			for _, h := range removed {
				fmt.Fprintf(env.Stdout, "  - %s\n", h)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "revoke from the global file")
	cmd.Flags().StringVar(&agentName, "agent", "default", "agent this applies to")
	return cmd
}

func newAgentConfigCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or edit per-project agent configuration",
	}
	cmd.AddCommand(newConfigEditCmd(env))
	cmd.AddCommand(newConfigShowCmd(env))
	cmd.AddCommand(newConfigPathCmd(env))
	cmd.AddCommand(newConfigListCmd(env))
	return cmd
}

func newConfigEditCmd(env *Env) *cobra.Command {
	var global bool
	var agentName string

	cmd := &cobra.Command{
		Use:   "edit [agent]",
		Short: "Open this project's agent configuration in $EDITOR",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				agentName = args[0]
			}
			store, err := trust.Load(env.Paths.Home, env.Paths.ProjectDir)
			if err != nil {
				return err
			}
			scope := scopeOf(store, global)
			path := scope.Path()

			// A first edit opens a commented template rather than an empty
			// buffer: the file has to explain its own layout, since there
			// is nowhere else the user would look.
			if _, err := os.Stat(path); os.IsNotExist(err) {
				if err := writeTemplate(path, env.Paths.ProjectDir, agentOrDefault(agentName)); err != nil {
					return err
				}
			}

			editor := firstNonEmpty(lookupEnv(env.Env, "VISUAL"), lookupEnv(env.Env, "EDITOR"), "vi")
			c := exec.Command(editor, path)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("running %s: %w", editor, err)
			}

			// Parse immediately: a typo should surface now, not on the
			// next agent run when the user has moved on.
			if _, err := trust.ReadProjectFile(path); err != nil {
				return fmt.Errorf("%w\n(the file was saved; fix it and run edit again)", err)
			}
			fmt.Fprintf(env.Stdout, "Saved %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "edit the file that applies to every project")
	cmd.Flags().StringVar(&agentName, "agent", "claude", "agent the template should scaffold")
	return cmd
}

func newConfigShowCmd(env *Env) *cobra.Command {
	var agentName string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the effective configuration for this project",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := trust.Load(env.Paths.Home, env.Paths.ProjectDir)
			if err != nil {
				return err
			}
			cfg := store.Resolve(agentName)

			fmt.Fprintf(env.Stdout, "Project: %s\n", env.Paths.ProjectDir)
			fmt.Fprintf(env.Stdout, "Agent:   %s\n\n", agentName)
			fmt.Fprintf(env.Stdout, "global:  %s\n", store.Global.Path())
			fmt.Fprintf(env.Stdout, "project: %s\n\n", store.Project.Path())

			if len(cfg.AllowHosts) == 0 {
				fmt.Fprintln(env.Stdout, "extra allow_hosts: none")
			} else {
				fmt.Fprintln(env.Stdout, "extra allow_hosts:")
				for _, h := range cfg.AllowHosts {
					fmt.Fprintf(env.Stdout, "  %s\n", h)
				}
			}
			for label, v := range map[string]string{
				"base": cfg.Base, "memory": cfg.Memory, "cpus": cfg.CPUs,
			} {
				if v != "" {
					fmt.Fprintf(env.Stdout, "%s: %s\n", label, v)
				}
			}
			if len(cfg.Args) > 0 {
				fmt.Fprintf(env.Stdout, "args: %s\n", strings.Join(cfg.Args, " "))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "claude", "agent to resolve for")
	return cmd
}

func newConfigPathCmd(env *Env) *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the configuration file path for this project",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := trust.Load(env.Paths.Home, env.Paths.ProjectDir)
			if err != nil {
				return err
			}
			fmt.Fprintln(env.Stdout, scopeOf(store, global).Path())
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "print the global file instead")
	return cmd
}

func newConfigListCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every project with recorded configuration",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			files, err := trust.ProjectFiles(env.Paths.Home)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				fmt.Fprintln(env.Stdout, "No per-project configuration recorded.")
				return nil
			}
			for _, path := range files {
				f, err := trust.ReadProjectFile(path)
				if err != nil {
					fmt.Fprintf(env.Stdout, "%s: unreadable (%v)\n", path, err)
					continue
				}
				fmt.Fprintf(env.Stdout, "%s\n", f.Project)
				for name, cfg := range f.Agents {
					if len(cfg.AllowHosts) > 0 {
						fmt.Fprintf(env.Stdout, "  %s: %s\n", name, strings.Join(cfg.AllowHosts, " "))
					}
				}
			}
			return nil
		},
	}
}

func writeTemplate(path, projectDir, agentName string) error {
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(trust.Template(projectDir, agentName)), 0o600)
}

func dirOf(path string) string {
	if i := strings.LastIndex(path, "/"); i > 0 {
		return path[:i]
	}
	return "."
}

func agentOrDefault(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

func lookupEnv(environ []string, name string) string {
	prefix := name + "="
	for i := len(environ) - 1; i >= 0; i-- {
		if strings.HasPrefix(environ[i], prefix) {
			return environ[i][len(prefix):]
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
