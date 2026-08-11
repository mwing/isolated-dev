package cli

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/history"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/trust"
)

// grantUse pairs a granted destination with whether anything ever used it.
type grantUse struct {
	Host string
	// Scope is where the grant is recorded: this project, or every project.
	Scope string
	Last  time.Time
	Count int
}

func newGrantsCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grants",
		Short: "Egress destinations granted for this project, and whether they are used",
		Long: "An allowlist that only ever grows stops meaning anything. Every\n" +
			"grant here was added for a reason that was true at the time; this\n" +
			"says which of those reasons still hold, by checking each grant\n" +
			"against what the recorded runs actually reached.\n\n" +
			"A grant with no recorded use is not proof of anything on its own —\n" +
			"it may simply predate the history, or belong to a path not taken\n" +
			"lately. It is a list of candidates to look at, not a verdict.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return showGrants(env)
		},
	}
	cmd.AddCommand(newPruneCmd(env))
	return cmd
}

// collectGrants matches each granted destination against recorded traffic.
//
// Matching uses the allowlist's own rule engine rather than string
// equality, because that is what decided the traffic in the first place: a
// grant of "*.anthropic.com" is used by a connection to
// "api.anthropic.com", and a review that reported otherwise would advise
// removing a grant that is doing its job.
func collectGrants(env *Env) ([]grantUse, []history.Run, *trust.Store, error) {
	store, err := trust.Load(env.Paths.Home, env.Paths.ProjectDir)
	if err != nil {
		return nil, nil, nil, err
	}
	runs, err := history.Load(history.Path(store.Project.Path()))
	if err != nil {
		return nil, nil, nil, err
	}
	contacts := history.Contacts(runs)

	// Accepted hosts are listed but never pruned: they record consent to
	// what the project asked for, and withdrawing consent is a different
	// act from tidying up a grant the user made themselves.
	scopes := []struct {
		name  string
		hosts []string
	}{
		{"project", store.Project.Hosts("default")},
		{"global", store.Global.Hosts("default")},
		{"accepted", store.AcceptedHosts("default")},
	}

	var out []grantUse
	for _, sc := range scopes {
		for _, host := range sc.hosts {
			g := grantUse{Host: host, Scope: sc.name}
			rule, perr := netpolicy.Parse([]string{host})
			for _, c := range contacts {
				if perr != nil || !rule.Allows(c.Host, c.Port) {
					continue
				}
				g.Count += c.Count
				if c.Last.After(g.Last) {
					g.Last = c.Last
				}
			}
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Last.Equal(out[j].Last) {
			return out[i].Host < out[j].Host
		}
		return out[i].Last.After(out[j].Last)
	})
	return out, runs, store, nil
}

func showGrants(env *Env) error {
	grants, runs, _, err := collectGrants(env)
	if err != nil {
		return err
	}
	if len(grants) == 0 {
		fmt.Fprintln(env.Stdout, "No egress grants for this project.")
		fmt.Fprintln(env.Stdout, "Language registries are permitted without a grant; "+
			"see `dev status`.")
		return nil
	}

	var unused int
	for _, g := range grants {
		mark := " "
		if g.Last.IsZero() {
			mark = "?"
			unused++
		}
		fmt.Fprintf(env.Stdout, "%s %-42s %-8s %-10s", mark, g.Host, g.Scope, ago(g.Last))
		if g.Count > 0 {
			fmt.Fprintf(env.Stdout, "  (%d)", g.Count)
		}
		fmt.Fprintln(env.Stdout)
	}

	fmt.Fprintf(env.Stdout, "\n%d recorded run(s) to judge by.\n", len(runs))
	switch {
	case len(runs) == 0:
		fmt.Fprintln(env.Stdout, "Nothing has been recorded yet, so nothing here means "+
			"a grant is unused.")
	case unused > 0:
		fmt.Fprintf(env.Stdout, "%d grant(s) marked ? were never reached in those runs: "+
			"`dev grants prune` to review them.\n", unused)
	}
	return nil
}

func newPruneCmd(env *Env) *cobra.Command {
	var apply bool
	var minRuns int

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove grants that recorded runs never used",
		Long: "Lists grants no recorded run ever reached, and with --apply removes\n" +
			"them.\n\n" +
			"It refuses to act on a thin history, because 'never used' derived\n" +
			"from three runs is not a finding. Removing a grant is cheap to undo\n" +
			"— `dev allow` puts it back — but a tool that quietly narrows access\n" +
			"on weak evidence teaches people to distrust it.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			grants, runs, store, err := collectGrants(env)
			if err != nil {
				return err
			}
			if len(runs) < minRuns {
				return fmt.Errorf("only %d recorded run(s); %d needed before "+
					"an unused grant means anything (--min-runs to change)",
					len(runs), minRuns)
			}

			var stale []grantUse
			for _, g := range grants {
				if g.Last.IsZero() && g.Scope != "accepted" {
					stale = append(stale, g)
				}
			}
			if len(stale) == 0 {
				fmt.Fprintf(env.Stdout, "Every grant was reached in the last %d run(s).\n",
					len(runs))
				return nil
			}

			for _, g := range stale {
				fmt.Fprintf(env.Stdout, "  - %s (%s)\n", g.Host, g.Scope)
			}
			if !apply {
				fmt.Fprintf(env.Stdout, "\n%d grant(s) unused across %d run(s). "+
					"Re-run with --apply to remove them.\n", len(stale), len(runs))
				return nil
			}

			for _, g := range stale {
				scope := store.Project
				if g.Scope == "global" {
					scope = store.Global
				}
				if _, err := store.Revoke(scope, "default", []string{g.Host}); err != nil {
					return err
				}
			}
			fmt.Fprintf(env.Stdout, "\nRemoved %d grant(s). "+
				"`dev allow HOST` puts any of them back.\n", len(stale))
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "actually remove them")
	cmd.Flags().IntVar(&minRuns, "min-runs", 10,
		"recorded runs required before an unused grant is reported")
	return cmd
}
