package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/trust"
)

// projectAsks returns the security-relevant settings this project's own
// .devenv.yaml requested.
//
// Only values that came from the PROJECT file count. The global file is
// the user's own machine and needs no consent from them, and a default is
// nobody's request. This is the whole reason config tracks provenance.
func projectAsks(cfg config.Config) []trust.Ask {
	var asks []trust.Ask

	if cfg.Origin("network") == config.OriginProject && cfg.Network == "open" {
		asks = append(asks, trust.Ask{
			Key: "network", Value: "open",
			Effect: "turn OFF egress filtering: the container reaches any host, " +
				"and nothing is reported as blocked because nothing is blocked",
		})
	}
	if cfg.Origin("mount_ssh_keys") == config.OriginProject && cfg.MountSSHKeys {
		asks = append(asks, trust.Ask{
			Key: "mount_ssh_keys", Value: "true",
			Effect: "mount ~/.ssh into the container, exposing your private keys to " +
				"anything running in it",
		})
	}
	if cfg.Origin("mount_git_config") == config.OriginProject && cfg.MountGitConfig {
		asks = append(asks, trust.Ask{
			Key: "mount_git_config", Value: "true",
			Effect: "mount ~/.gitconfig, which may carry signing keys, credential " +
				"helpers and insteadOf rules that redirect remotes",
		})
	}
	if cfg.Origin("mount_docker_socket") == config.OriginProject && cfg.MountDockerSocket {
		asks = append(asks, trust.Ask{
			Key: "mount_docker_socket", Value: "true",
			Effect: "mount the docker socket, which is root on the docker host",
		})
	}
	if cfg.Origin("pass_env_vars") == config.OriginProject && !cfg.PassEnvVars.Empty() {
		v := strings.Join(append(append([]string(nil), cfg.PassEnvVars.Patterns...),
			cfg.PassEnvVars.Explicit...), " ")
		asks = append(asks, trust.Ask{
			Key: "pass_env_vars", Value: v,
			Effect: "copy matching host environment variables into the container: " + v,
		})
	}
	return asks
}

// enforceConsent stops a run when the project asks for something the user
// has not accepted. A project file is a request; running it is not consent.
func enforceConsent(env *Env, cfg config.Config, store *trust.Store) error {
	pending := store.PendingSettings(projectAsks(cfg))
	if len(pending) == 0 {
		return nil
	}
	fmt.Fprintf(env.Stderr, "%s requests settings you have not accepted:\n\n", env.Paths.Project)
	for _, a := range pending {
		fmt.Fprintf(env.Stderr, "  %s: %s\n", a.Key, a.Value)
		fmt.Fprintf(env.Stderr, "      %s\n", a.Effect)
	}
	fmt.Fprintf(env.Stderr, "\nReview and accept:  dev2 accept\n")
	fmt.Fprintf(env.Stderr, "Or ignore the file:  --network allowlist\n")
	return fmt.Errorf("unaccepted project settings")
}

func newAcceptCmd(env *Env) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "accept [key...]",
		Short: "Review and accept what this project's .devenv.yaml requests",
		Long: "A project file states what the project needs. It is a request,\n" +
			"not a grant: running the project is not consent. Decisions are\n" +
			"recorded under ~/.dev-envs, never in the repository, so a clone\n" +
			"cannot bring its own approval with it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccept(cmd.Context(), env, args, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "accept everything requested")
	return cmd
}

func runAccept(_ context.Context, env *Env, keys []string, all bool) error {
	cfg, err := config.Load(env.Paths, env.Env)
	if err != nil {
		return err
	}
	store, err := trust.Load(env.Paths.Home, env.Paths.ProjectDir)
	if err != nil {
		return err
	}

	pending := store.PendingSettings(projectAsks(cfg))
	if len(pending) == 0 {
		fmt.Fprintln(env.Stdout, "Nothing pending: this project requests no settings you have not accepted.")
		fmt.Fprintln(env.Stdout, "Egress destinations are accepted with `dev2 agent accept`.")
		return nil
	}

	if !all && len(keys) == 0 {
		fmt.Fprintf(env.Stdout, "%s requests:\n\n", env.Paths.Project)
		for _, a := range pending {
			fmt.Fprintf(env.Stdout, "  %s: %s\n", a.Key, a.Value)
			fmt.Fprintf(env.Stdout, "      %s\n\n", a.Effect)
		}
		fmt.Fprintf(env.Stdout, "Accept all:  dev2 accept --all\n")
		fmt.Fprintf(env.Stdout, "Accept one:  dev2 accept %s\n", pending[0].Key)
		return nil
	}

	wanted := pending
	if !all {
		wanted = nil
		for _, a := range pending {
			for _, k := range keys {
				if a.Key == k {
					wanted = append(wanted, a)
				}
			}
		}
		if len(wanted) == 0 {
			return fmt.Errorf("none of %s is pending", strings.Join(keys, ", "))
		}
	}

	added, err := store.AcceptSettings(wanted)
	if err != nil {
		return err
	}
	fmt.Fprintln(env.Stdout, "Accepted:")
	for _, a := range added {
		fmt.Fprintf(env.Stdout, "  + %s: %s\n", a.Key, a.Value)
	}
	fmt.Fprintf(env.Stdout, "\nRecorded in %s\n", store.Project.Path())
	return nil
}
