package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/mwing/isolated-dev/internal/agent"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/policy"
	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/trust"
)

// checkLearnPermitted refuses learning where the machine forbids open
// networking: learning is open minus the denies, and a policy that ruled out
// `open` would otherwise have no way to rule it out.
func checkLearnPermitted(pol *policy.Policy) error {
	if err := pol.CheckNetwork(string(project.NetworkOpen)); err != nil {
		return fmt.Errorf("--learn admits every destination for the run, "+
			"which this machine's policy does not permit: %w", err)
	}
	return nil
}

// The connector gate works by leaving connector hosts out of the allowlist,
// and under learning an absent host is admitted, so without --allow-mcp
// they have to become denies instead.
func learnDenies(machine []string, o *agent.Options) []string {
	out := append([]string(nil), machine...)
	if o != nil && !o.AllowMCP {
		out = append(out, o.Agent.MCPHosts...)
	}
	return out
}

func learning(o *agent.Options) func(*netpolicy.Sidecar) {
	return func(s *netpolicy.Sidecar) {
		s.Learn = true
		s.Deny = learnDenies(s.Deny, o)
	}
}

func announceLearning(env *Env, o *agent.Options) {
	blocked := "Denied hosts, literal IPs and private or metadata addresses stay blocked."
	if o != nil && !o.AllowMCP && len(o.Agent.MCPHosts) > 0 {
		blocked = "Denied hosts, MCP connectors, literal IPs and private or metadata addresses stay blocked."
	}
	fmt.Fprintln(env.Stderr, "Learning: names not on the allowlist are allowed for this run and recorded.")
	fmt.Fprintln(env.Stderr, "          "+blocked)
}

// canAsk is false once the run was interrupted: a prompt then would wait on a terminal whose
// interrupt key is still being swallowed.
func reportLearned(env *Env, store *trust.Store, agentName string, learned []string, canAsk bool) {
	pol, err := loadPolicy(env)
	if err != nil {
		fmt.Fprintf(env.Stderr, "Not offered: the machine policy could not be read: %v\n", err)
		return
	}
	var keep, refused []string
	for _, e := range learned {
		if _, err := netpolicy.Parse([]string{e}); err != nil {
			continue
		}
		if err := pol.CheckHost(e); err != nil {
			refused = append(refused, e)
			continue
		}
		keep = append(keep, e)
	}
	for _, e := range refused {
		fmt.Fprintf(env.Stderr, "Not offered: %s is denied by this machine's policy.\n", e)
	}
	if len(keep) == 0 {
		fmt.Fprintln(env.Stderr, "Learned: nothing beyond the allowlist.")
		return
	}

	fmt.Fprintln(env.Stderr, "Learned: reached this run, not on the allowlist:")
	for _, e := range keep {
		fmt.Fprintf(env.Stderr, "  %s\n", e)
	}
	flag, scope := " --agent "+agentName, "the "+agentName+" agent in this project"
	if agentName == "default" {
		flag, scope = "", "every run and every agent in this project"
	}
	command := "dev allow" + flag + " " + strings.Join(keep, " ")

	if !canAsk {
		fmt.Fprintf(env.Stderr, "Keep them:  %s\n", command)
		return
	}
	// The workload chose every name above, so the answer has to be one it
	// cannot supply: keys typed to the session that just ended are dropped,
	// and a stray "y" is not enough.
	discardTypeahead(env.Stdin)
	fmt.Fprintf(env.Stderr, "Allow all %d for %s? Type yes to keep them: ", len(keep), scope)
	line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "yes" {
		fmt.Fprintf(env.Stderr, "Not saved. Later:  %s\n", command)
		return
	}
	added, err := store.Grant(store.Project, agentName, keep)
	if err != nil {
		fmt.Fprintf(env.Stderr, "Not saved: %v\n", err)
		return
	}
	fmt.Fprintf(env.Stderr, "Granted %d for %s. Stored in %s\n", len(added), scope, store.Project.Path())
}
