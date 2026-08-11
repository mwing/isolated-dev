package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	assets "github.com/mwing/isolated-dev"
	"github.com/mwing/isolated-dev/internal/langs"
)

// languagesStampFile records which embedded plugin set was installed, so a
// binary can tell "these are mine" from "these are older than me".
const languagesStampFile = ".installed-by-dev"

func newLanguagesCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "languages",
		Short: "The language plugins this tool detects and scaffolds with",
		Long: "Plugins live in ~/.dev-envs/languages and decide what a project is\n" +
			"detected as, what its image is built from, and what `dev new`\n" +
			"scaffolds.\n\n" +
			"This binary carries a copy. Yours win: a plugin you have edited is\n" +
			"never overwritten unless you ask for it.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return listLanguages(env)
		},
	}
	cmd.AddCommand(newLanguagesInstallCmd(env))
	return cmd
}

func listLanguages(env *Env) error {
	set, err := langs.Load(env.Paths.Languages)
	if err != nil {
		return err
	}
	names := set.Names()
	sort.Strings(names)

	if len(names) == 0 {
		fmt.Fprintf(env.Stdout, "No plugins installed in %s\n", env.Paths.Languages)
		fmt.Fprintln(env.Stdout, "Install the set this binary carries:  dev languages install")
		return nil
	}
	for _, n := range names {
		fmt.Fprintf(env.Stdout, "  %s\n", n)
	}
	fmt.Fprintf(env.Stdout, "\n%s\n", env.Paths.Languages)
	for _, note := range set.Notes {
		fmt.Fprintf(env.Stderr, "⚠  %s\n", note)
	}
	if stale, err := languagesStale(env); err == nil && stale {
		fmt.Fprintln(env.Stdout, "\nThis binary carries a newer set. `dev languages install` "+
			"adds what is missing;\n--force also replaces plugins you have not edited.")
	}
	return nil
}

// languagesStale reports whether the installed set came from a different
// build than this one.
func languagesStale(env *Env) (bool, error) {
	b, err := os.ReadFile(filepath.Join(env.Paths.Languages, languagesStampFile))
	if err != nil {
		// No stamp: installed by v1, or by hand. Not stale by itself —
		// saying so would nag every user who has ever edited a plugin.
		return false, err
	}
	return string(b) != assets.LanguagesHash(), nil
}

func newLanguagesInstallCmd(env *Env) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the plugin set this binary carries into ~/.dev-envs",
		Long: "Adds plugins that are missing. Existing files are left alone: you\n" +
			"may have edited one, and reverting that silently would be this tool\n" +
			"overwriting work it did not create. --force replaces them.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			written, err := installLanguages(env, force)
			if err != nil {
				return err
			}
			if len(written) == 0 {
				fmt.Fprintln(env.Stdout, "Nothing to write: every plugin file is already there.")
				fmt.Fprintln(env.Stdout, "`--force` replaces them with this binary's copies.")
				return nil
			}
			for _, w := range written {
				fmt.Fprintf(env.Stdout, "  %s\n", w)
			}
			fmt.Fprintf(env.Stdout, "\n%d file(s) into %s\n", len(written), env.Paths.Languages)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace files that already exist")
	return cmd
}

func installLanguages(env *Env, force bool) ([]string, error) {
	written, err := assets.WriteLanguages(env.Paths.Languages, force)
	if err != nil {
		return nil, err
	}
	// Stamped only when the write was unconditional. After a partial
	// install the directory is a mixture, and claiming it matches this
	// build would make `languages` report the opposite of the truth.
	if force || len(written) > 0 {
		stamp := filepath.Join(env.Paths.Languages, languagesStampFile)
		if force {
			_ = os.WriteFile(stamp, []byte(assets.LanguagesHash()), 0o644)
		}
	}
	return written, nil
}

// loadLanguages is the loader every command that needs plugins uses.
//
// It installs the embedded set first when nothing is installed. Loading
// directly is how `dev new` shipped a version that answered "available:"
// with nothing on a fresh machine while `dev run` two files away installed
// them correctly — a second call site is a second chance to forget.
func loadLanguages(env *Env) (*langs.Set, error) {
	ensureLanguages(env)
	return langs.Load(env.Paths.Languages)
}

// ensureLanguages installs the embedded plugins when none are present.
//
// A binary with no plugins detects nothing, builds nothing and scaffolds
// nothing — every command fails at once, for a reason the user cannot act
// on. Writing the set it carries is not a surprise: it is the tool's own
// data, and only the absence of any plugin at all triggers it.
func ensureLanguages(env *Env) {
	if entries, err := os.ReadDir(env.Paths.Languages); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				return
			}
		}
	}
	written, err := assets.WriteLanguages(env.Paths.Languages, false)
	if err != nil || len(written) == 0 {
		return
	}
	_ = os.WriteFile(filepath.Join(env.Paths.Languages, languagesStampFile),
		[]byte(assets.LanguagesHash()), 0o644)
	fmt.Fprintf(env.Stderr, "Installed %d language plugin file(s) into %s\n",
		len(written), env.Paths.Languages)
}
