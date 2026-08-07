package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/config"
)

func newMigrateCmd(env *Env) *cobra.Command {
	var write bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Report what changes when moving a v1 setup to v2",
		Long: "v2 reads v1's files, so nothing has to be converted. What changes\n" +
			"is what some settings MEAN, and which of them never did anything.\n" +
			"This reports both, and with --write removes the keys that were\n" +
			"always no-ops.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return migrate(cmd.Context(), env, write)
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "remove never-implemented keys from the global config")
	return cmd
}

func migrate(_ context.Context, env *Env, write bool) error {
	cfg, err := config.Load(env.Paths, env.Env)
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Stdout, "Global config: %s\n", env.Paths.Global)
	fmt.Fprintf(env.Stdout, "Project config: %s\n\n", env.Paths.Project)

	// 1. Keys that never did anything.
	var dead []config.Note
	for _, n := range cfg.Notes {
		if strings.Contains(n.Text, "never implemented") {
			dead = append(dead, n)
		}
	}
	if len(dead) > 0 {
		fmt.Fprintln(env.Stdout, "Keys that never did anything in v1 and do nothing now:")
		for _, n := range dead {
			fmt.Fprintf(env.Stdout, "  %s\n", n.Key)
		}
		fmt.Fprintln(env.Stdout)
	}

	// 2. Settings whose meaning changed. This is the part that matters:
	// a key that still parses but now behaves differently is worse than
	// one that was removed, because nothing forces the user to look at it.
	changed := false
	report := func(format string, args ...any) {
		changed = true
		fmt.Fprintf(env.Stdout, format, args...)
	}

	fmt.Fprintln(env.Stdout, "Settings that mean something different in v2:")
	report("  network: %s\n", cfg.Network)
	fmt.Fprintf(env.Stdout, "      v1 ran every container with unrestricted egress.\n")
	fmt.Fprintf(env.Stdout, "      v2 defaults to an allowlist: the language's registries plus\n")
	fmt.Fprintf(env.Stdout, "      what you grant. Set `network: open` per project for v1 behavior.\n")

	if !cfg.PassEnvVars.Empty() {
		names := append(append([]string(nil), cfg.PassEnvVars.Patterns...), cfg.PassEnvVars.Explicit...)
		report("  pass_env_vars: %s\n", strings.Join(names, " "))
		fmt.Fprintf(env.Stdout, "      v1 copied these into every container. In v2 environment\n")
		fmt.Fprintf(env.Stdout, "      passthrough is a grant, not a default, and is not honored\n")
		fmt.Fprintf(env.Stdout, "      at the untrusted level a normal run uses.\n")
		if cfg.Origin("pass_env_vars") == config.OriginGlobal {
			fmt.Fprintf(env.Stdout, "      This is in your GLOBAL config, so it applied to every\n")
			fmt.Fprintf(env.Stdout, "      project you ever ran. Worth deleting if you did not mean it.\n")
		}
	}
	for _, m := range []struct {
		key string
		on  bool
	}{
		{"mount_ssh_keys", cfg.MountSSHKeys},
		{"mount_git_config", cfg.MountGitConfig},
		{"mount_docker_socket", cfg.MountDockerSocket},
	} {
		if !m.on {
			continue
		}
		report("  %s: true\n", m.key)
		fmt.Fprintf(env.Stdout, "      A mount is a grant in v2. Set in a project file it now needs\n")
		fmt.Fprintf(env.Stdout, "      `dev2 accept`; ssh keys are replaced by agent forwarding.\n")
	}
	if !changed {
		fmt.Fprintln(env.Stdout, "  none")
	}

	// 3. What v1 had that v2 does not.
	fmt.Fprintf(env.Stdout, "\nCommands that changed shape: see docs/PARITY.md\n")

	if !write {
		if len(dead) > 0 {
			fmt.Fprintf(env.Stdout, "\nRun `dev2 migrate --write` to strip the %d dead key(s) "+
				"from the global config.\n", len(dead))
		}
		return nil
	}
	if len(dead) == 0 {
		fmt.Fprintln(env.Stdout, "\nNothing to remove.")
		return nil
	}
	return stripDeadKeys(env, dead)
}

// stripDeadKeys rewrites the global config without the no-op keys, keeping
// a timestamped backup. Comment lines introducing a removed key go with
// it, so the file does not end up with a heading over nothing.
func stripDeadKeys(env *Env, dead []config.Note) error {
	raw, err := os.ReadFile(env.Paths.Global)
	if err != nil {
		return err
	}
	drop := map[string]bool{}
	for _, n := range dead {
		drop[n.Key] = true
	}

	lines := strings.Split(string(raw), "\n")
	var out []string
	var pendingComments []string
	removed := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			pendingComments = append(pendingComments, line)
			continue
		}
		key, _, _ := strings.Cut(trimmed, ":")
		if drop[strings.TrimSpace(key)] {
			// Drop the key and the comment block directly above it.
			pendingComments = nil
			removed++
			continue
		}
		out = append(out, pendingComments...)
		pendingComments = nil
		out = append(out, line)
	}
	out = append(out, pendingComments...)

	backup := fmt.Sprintf("%s.bak-%s", env.Paths.Global, time.Now().UTC().Format("20060102150405"))
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		return fmt.Errorf("writing backup: %w", err)
	}
	if err := os.WriteFile(env.Paths.Global, []byte(strings.Join(out, "\n")), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "\nRemoved %d key(s). Backup: %s\n", removed, backup)
	return nil
}
