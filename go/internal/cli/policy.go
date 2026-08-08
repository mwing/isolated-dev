package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/policy"
)

// loadPolicy reads the machine's policy. Every caller treats a malformed
// policy as fatal: a rule that fails to parse is a rule that stops
// applying, and this is the one thing here that answers to someone other
// than the person running it.
func loadPolicy(env *Env) (*policy.Policy, error) {
	return policy.Load(env.Paths.Home)
}

func newPolicyCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "policy",
		Short: "Show the rules this machine enforces",
		Long: "A policy constrains everyone using this machine, including you.\n" +
			"It is meant for a team handing out a tool with the unsafe paths\n" +
			"already closed.\n\n" +
			"It is not a defense against the owner of the machine: the file is\n" +
			"on their disk. It closes those paths for people who are not\n" +
			"attacking their own laptop, and makes an override deliberate\n" +
			"rather than accidental.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			p, err := loadPolicy(env)
			if err != nil {
				return err
			}
			if !p.Active() {
				fmt.Fprintf(env.Stdout, "No policy in force.\n\n")
				fmt.Fprintf(env.Stdout, "One would be read from %s\n",
					policy.DefaultPath(env.Paths.Home))
				return nil
			}
			fmt.Fprintf(env.Stdout, "Policy: %s\n\n", p.Path())
			for _, line := range p.Describe() {
				fmt.Fprintf(env.Stdout, "  %s\n", line)
			}
			return nil
		},
	}
}
