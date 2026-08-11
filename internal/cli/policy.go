package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/policy"
)

// loadPolicy reads the machine's policy. Every caller treats a malformed
// policy as fatal: a rule that fails to parse is a rule that stops
// applying, and this is the one thing here that answers to someone other
// than the person running it.
func loadPolicy(env *Env) (*policy.Policy, error) {
	return policy.Load(env.Paths.Home)
}

// checkHosts refuses destinations the policy denies, loading the policy for
// the callers that have none in hand — `dev allow`, `dev agent accept`,
// `dev agent policy` and `dev agent run`. The commands that already hold one
// (the runs, the prompts) call CheckHost themselves.
//
// Every route that widens what a run may reach is checked one way or the
// other. It used to be reachable from the grant command alone, while the
// package doc claimed enforcement at every route in — a rule with one
// polite entrance is not a rule, it is a suggestion with good manners.
func checkHosts(env *Env, hosts []string) error {
	if len(hosts) == 0 {
		return nil
	}
	pol, err := loadPolicy(env)
	if err != nil {
		return err
	}
	return pol.CheckHosts(hosts)
}

// permittedHosts drops denied destinations from an assembled allowlist,
// naming each one it drops.
//
// The routes above refuse outright, which is where a user finds out and can
// do something about it. This is the last gate before the sidecar, and it
// exists because a grant recorded before a rule existed must not outlive the
// rule — the same reason enforceConsent puts policy above an acceptance
// already given. Dropping rather than failing is deliberate here: the run
// proceeds with the destination closed, which is the outcome the rule asks
// for, and a machine that suddenly refuses every run because of one stale
// grant teaches people to delete the policy.
func permittedHosts(env *Env, pol *policy.Policy, hosts []string) []string {
	if pol == nil || len(pol.DenyHosts) == 0 {
		return hosts
	}
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if verr := pol.CheckHost(h); verr != nil {
			fmt.Fprintf(env.Stderr, "⚠  dropped from this run's allowlist: %v\n", verr)
			continue
		}
		out = append(out, h)
	}
	return out
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
