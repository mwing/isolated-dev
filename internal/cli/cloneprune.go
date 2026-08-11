package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// Clones are full copies of repositories, and making them the default for
// agent runs means they accumulate without anyone deciding to accumulate
// them. A gigabyte per project, one per project worked on, kept until
// something removes them — that is a disk problem arriving quietly, which
// is the worst kind.
//
// So the tool says the number before it becomes a surprise: when a clone is
// created past the threshold, in `dev doctor`, and in `dev clone list`. And
// `dev clone prune` removes the ones that hold nothing, which is most of
// them most of the time.

// cloneSpaceWarning is where "some disk" becomes "worth telling you". Not a
// limit — nothing is refused — just the point where silence stops being
// honest.
const cloneSpaceWarning = 5 << 30 // 5 GiB

func newClonePruneCmd(env *Env) *cobra.Command {
	var (
		olderThan string
		force     bool
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove clones that hold no work",
		Long: "Deletes clones with nothing in them: no commits the project does\n" +
			"not have, and no uncommitted changes. Anything holding work is\n" +
			"listed and kept.\n\n" +
			"Clones touched recently are kept too, whatever their state — a run\n" +
			"that finished this morning is one somebody may still be reading.\n" +
			"--older-than changes that window.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			age, err := time.ParseDuration(normalizeDuration(olderThan))
			if err != nil {
				return fmt.Errorf("--older-than %q: %w (try 24h, 7d, 720h)", olderThan, err)
			}
			return pruneClones(cmd.Context(), env, age, force, dryRun)
		},
	}
	// A week by default: a clone touched more recently than that is one
	// somebody may still be reading, whatever git says about its contents.
	cmd.Flags().StringVar(&olderThan, "older-than", "7d",
		"only prune clones untouched for at least this long (e.g. 24h, 7d)")
	cmd.Flags().BoolVar(&force, "force", false,
		"also remove clones holding work that is not in their project")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list what would go, and remove nothing")
	return cmd
}

// normalizeDuration accepts days, which time.ParseDuration does not, because
// "7d" is how people describe this and "168h" is not.
func normalizeDuration(s string) string {
	if len(s) > 1 && s[len(s)-1] == 'd' {
		var days float64
		if _, err := fmt.Sscanf(s[:len(s)-1], "%g", &days); err == nil {
			return fmt.Sprintf("%gh", days*24)
		}
	}
	return s
}

func pruneClones(ctx context.Context, env *Env, age time.Duration, force, dryRun bool) error {
	root := clonesRoot(env)
	clones, err := scanClones(ctx, env.Runner, root)
	if err != nil {
		return err
	}
	if len(clones) == 0 {
		fmt.Fprintln(env.Stdout, "No clones.")
		return nil
	}

	var (
		remove  []cloneInfo
		holding []cloneInfo
		recent  []cloneInfo
		freed   int64
	)
	cutoff := time.Now().Add(-age)
	for _, c := range clones {
		switch {
		case c.Modified.After(cutoff):
			recent = append(recent, c)
		case (c.Dirty > 0 || c.Unmerged > 0) && !force:
			holding = append(holding, c)
		default:
			remove = append(remove, c)
			freed += c.Size
		}
	}

	for _, c := range remove {
		if dryRun {
			fmt.Fprintf(env.Stdout, "  would remove %-24s %s\n", c.Name, humanSize(c.Size))
			continue
		}
		if err := removeClone(c.Path); err != nil {
			fmt.Fprintf(env.Stderr, "  ⚠ %s: %v\n", c.Name, err)
			continue
		}
		fmt.Fprintf(env.Stdout, "  removed %-24s %s\n", c.Name, humanSize(c.Size))
	}

	// What was kept, and why, because a prune that quietly skips things
	// looks like a prune that did not work.
	for _, c := range holding {
		fmt.Fprintf(env.Stdout, "  kept    %-24s %s  holds work (%d commit(s), %d uncommitted)\n",
			c.Name, humanSize(c.Size), c.Unmerged, c.Dirty)
	}
	for _, c := range recent {
		fmt.Fprintf(env.Stdout, "  kept    %-24s %s  touched %s\n",
			c.Name, humanSize(c.Size), ago(c.Modified))
	}

	fmt.Fprintln(env.Stdout)
	switch {
	case dryRun && len(remove) > 0:
		fmt.Fprintf(env.Stdout, "%s in %d clone(s) would be freed. Run without --dry-run.\n",
			humanSize(freed), len(remove))
	case len(remove) > 0:
		fmt.Fprintf(env.Stdout, "Freed %s across %d clone(s).\n", humanSize(freed), len(remove))
	default:
		fmt.Fprintln(env.Stdout, "Nothing to prune.")
	}
	if len(holding) > 0 && !force {
		fmt.Fprintf(env.Stdout, "%d clone(s) hold work. `dev clone list` shows what, "+
			"and `--force` removes them anyway.\n", len(holding))
	}
	return nil
}

// warnCloneSpace reports the total when it has grown past the point where
// saying nothing would be misleading. Printed where a clone is made, since
// that is the moment the number changed and the person is present.
func warnCloneSpace(ctx context.Context, env *Env) {
	clones, err := scanClones(ctx, env.Runner, clonesRoot(env))
	if err != nil || len(clones) == 0 {
		return
	}
	var total int64
	for _, c := range clones {
		total += c.Size
	}
	if total < cloneSpaceWarning {
		return
	}
	fmt.Fprintf(env.Stderr, "\n⚠  Clones now use %s across %d project(s) in %s\n",
		humanSize(total), len(clones), clonesRoot(env))
	fmt.Fprintf(env.Stderr, "   `dev clone list` shows which; `dev clone prune` "+
		"removes the ones holding nothing.\n\n")
}
