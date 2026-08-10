package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/clone"
	"github.com/mwing/isolated-dev/internal/runner"
)

func newCloneCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clone",
		Short: "The private copies that --clone runs work in",
		Long: "A clone is a full copy of a repository, so they take real disk and\n" +
			"they hold real work. Neither is visible from anywhere else: the\n" +
			"directory is outside the project, and a run that made changes there\n" +
			"says so once and then scrolls away.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listClones(cmd.Context(), env)
		},
	}
	cmd.AddCommand(newCloneListCmd(env), newClonePathCmd(env), newCloneRemoveCmd(env))
	return cmd
}

func newCloneListCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Every clone on this machine, with what is still in it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listClones(cmd.Context(), env)
		},
	}
}

// cloneInfo is what is worth knowing about a clone without opening it.
type cloneInfo struct {
	Name string
	Path string
	// Dirty is the number of uncommitted changes.
	Dirty int
	// Unmerged is the number of commits that exist only here — work that
	// deleting the directory would destroy.
	Unmerged int
	Branch   string
	Shallow  bool
	Size     int64
	Modified time.Time
}

func scanClones(ctx context.Context, run runner.Runner, root string) ([]cloneInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []cloneInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			continue
		}
		info := cloneInfo{Name: e.Name(), Path: path}
		if fi, err := e.Info(); err == nil {
			info.Modified = fi.ModTime()
		}
		info.Dirty, info.Unmerged, info.Branch, info.Shallow = clone.State(ctx, run, path)
		info.Size = dirSize(ctx, run, path)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// dirSize shells out to du, which is far faster than walking a tree that
// may hold a full git object store. An unavailable du costs a column, not
// the command.
func dirSize(ctx context.Context, run runner.Runner, path string) int64 {
	res, err := run.Run(ctx, runner.Command{Path: "du", Args: []string{"-sk", path}})
	if err != nil || res.ExitCode != 0 {
		return 0
	}
	fields := strings.Fields(res.Stdout)
	if len(fields) == 0 {
		return 0
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

func humanSize(n int64) string {
	switch {
	case n <= 0:
		return "?"
	case n < 1<<20:
		return fmt.Sprintf("%dK", n>>10)
	case n < 1<<30:
		return fmt.Sprintf("%dM", n>>20)
	default:
		return fmt.Sprintf("%.1fG", float64(n)/float64(1<<30))
	}
}

func listClones(ctx context.Context, env *Env) error {
	root := filepath.Join(env.Paths.Home, "clones")
	clones, err := scanClones(ctx, env.Runner, root)
	if err != nil {
		return err
	}
	if len(clones) == 0 {
		fmt.Fprintln(env.Stdout, "No clones. `dev run --clone` makes one.")
		return nil
	}

	var total int64
	var holding int
	for _, c := range clones {
		total += c.Size

		state := "clean"
		if c.Dirty > 0 || c.Unmerged > 0 {
			holding++
			parts := []string{}
			if c.Unmerged > 0 {
				parts = append(parts, fmt.Sprintf("%d commit(s) only here", c.Unmerged))
			}
			if c.Dirty > 0 {
				parts = append(parts, fmt.Sprintf("%d uncommitted", c.Dirty))
			}
			state = strings.Join(parts, ", ")
		}
		branch := c.Branch
		if c.Shallow {
			branch += " (shallow)"
		}
		fmt.Fprintf(env.Stdout, "  %-24s %-8s %-22s %s\n",
			c.Name, humanSize(c.Size), branch, state)
	}

	fmt.Fprintf(env.Stdout, "\n%d clone(s), %s on disk, in %s\n",
		len(clones), humanSize(total), root)
	if holding > 0 {
		fmt.Fprintf(env.Stdout, "%d hold work that is not in their project. "+
			"`dev clone rm` refuses those until it is fetched back.\n", holding)
	}
	return nil
}

func newClonePathCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Where this project's clone is, for scripting",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path := clone.Dir(env.Paths.Home, projectSlug(env.Paths.ProjectDir))
			if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
				return fmt.Errorf("this project has no clone yet; `dev run --clone` makes one")
			}
			fmt.Fprintln(env.Stdout, path)
			return nil
		},
	}
}

func newCloneRemoveCmd(env *Env) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm [name]",
		Short: "Delete a clone",
		Long: "Deletes this project's clone, or a named one from `dev clone list`.\n\n" +
			"It refuses while the clone holds commits that exist nowhere else,\n" +
			"or uncommitted changes. That is the whole risk of working in a\n" +
			"clone: the work is real, it is not where anyone looks for it, and\n" +
			"deleting the directory is the one irreversible thing this tool can\n" +
			"do to it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := clone.Dir(env.Paths.Home, projectSlug(env.Paths.ProjectDir))
			if len(args) == 1 {
				path = filepath.Join(env.Paths.Home, "clones", filepath.Base(args[0]))
			}
			if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
				return fmt.Errorf("no clone at %s", path)
			}

			dirty, unmerged, _, _ := clone.State(cmd.Context(), env.Runner, path)
			if (dirty > 0 || unmerged > 0) && !force {
				return fmt.Errorf("%s still holds work: %d commit(s) that exist only "+
					"there, %d uncommitted change(s).\n"+
					"  Bring it back:  git -C %s fetch %s\n"+
					"  Or discard it:  dev clone rm --force",
					path, unmerged, dirty, env.Paths.ProjectDir, path)
			}
			if err := clone.Remove(path); err != nil {
				return err
			}
			fmt.Fprintf(env.Stdout, "Removed %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "delete even if it holds unmerged work")
	return cmd
}
