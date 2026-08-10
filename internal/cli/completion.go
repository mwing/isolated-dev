package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// commonImages are offered when completing --image.
//
// A completion listing every image on the daemon would be noise. The
// sandboxing case starts with someone who does not yet know what to name,
// so the list is short and general on purpose: a base for each common
// language, plus a plain distribution for things that are not projects.
var commonImages = []cobra.Completion{
	cobra.CompletionWithDesc("debian:bookworm-slim", "plain Debian, for anything that is not a project"),
	cobra.CompletionWithDesc("alpine:3", "smallest general base"),
	cobra.CompletionWithDesc("ubuntu:24.04", "Ubuntu LTS"),
	cobra.CompletionWithDesc("golang:1.26", "Go toolchain"),
	cobra.CompletionWithDesc("node:22-bookworm-slim", "Node"),
	cobra.CompletionWithDesc("python:3.13-slim", "Python"),
	cobra.CompletionWithDesc("rust:1", "Rust toolchain"),
}

func completeImage(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	// NoFileComp: an image reference is not a path, and offering the
	// working directory's files here is worse than offering nothing.
	return commonImages, cobra.ShellCompDirectiveNoFileComp
}

// registerCompletions attaches value completions to the flags where a
// blank prompt is unhelpful.
func registerCompletions(root *cobra.Command) {
	for _, path := range [][]string{{"run"}, {"shell"}, {"scan"}, {"agent", "run"}, {"console"}} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil {
			continue
		}
		if cmd.Flag("image") != nil {
			_ = cmd.RegisterFlagCompletionFunc("image", completeImage)
		}
	}
	if cmd, _, err := root.Find([]string{"scan"}); err == nil && cmd != nil {
		_ = cmd.RegisterFlagCompletionFunc("severity",
			func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
				return []cobra.Completion{"low", "medium", "high", "critical"},
					cobra.ShellCompDirectiveNoFileComp
			})
	}
	for _, path := range [][]string{{"run"}, {"shell"}} {
		if cmd, _, err := root.Find(path); err == nil && cmd != nil {
			_ = cmd.RegisterFlagCompletionFunc("network",
				func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
					return []cobra.Completion{
						cobra.CompletionWithDesc("allowlist", "registries plus what you granted"),
						cobra.CompletionWithDesc("open", "no filtering"),
						cobra.CompletionWithDesc("none", "no network at all"),
					}, cobra.ShellCompDirectiveNoFileComp
				})
		}
	}
}

func newCompletionInstallCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "install [bash|zsh|fish]",
		Short: "Install the completion script where your shell will find it",
		Long: "`dev completion zsh` prints a script; this writes it somewhere\n" +
			"the shell loads, which is the part people actually want.\n\n" +
			"Without a shell argument it uses $SHELL.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := ""
			if len(args) == 1 {
				shell = args[0]
			}
			return installCompletion(env, cmd.Root(), shell)
		},
	}
}

// completionTarget is where a shell looks for completions, and what the
// user may still have to do about it.
type completionTarget struct {
	Path string
	Note string
}

func targetFor(shell, home string) (completionTarget, error) {
	switch shell {
	case "zsh":
		return completionTarget{
			Path: filepath.Join(home, ".zsh", "completions", "_dev"),
			Note: "If completions do not appear, add this to ~/.zshrc before compinit:\n" +
				"  fpath=(~/.zsh/completions $fpath)",
		}, nil
	case "bash":
		return completionTarget{
			Path: filepath.Join(home, ".local", "share", "bash-completion", "completions", "dev"),
			Note: "Requires the bash-completion package; start a new shell to pick it up.",
		}, nil
	case "fish":
		return completionTarget{
			Path: filepath.Join(home, ".config", "fish", "completions", "dev.fish"),
			Note: "fish loads this automatically in a new shell.",
		}, nil
	default:
		return completionTarget{}, fmt.Errorf(
			"unsupported shell %q; dev completion install accepts bash, zsh or fish", shell)
	}
}

// detectShell reads $SHELL, which is the shell the user chose rather than
// whatever happens to be running this command.
func detectShell(environ []string) string {
	sh := lookupEnv(environ, "SHELL")
	return filepath.Base(strings.TrimSpace(sh))
}

func installCompletion(env *Env, root *cobra.Command, shell string) error {
	if shell == "" {
		shell = detectShell(env.Env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	target, err := targetFor(shell, home)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(target.Path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(target.Path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	switch shell {
	case "zsh":
		err = root.GenZshCompletion(f)
	case "bash":
		err = root.GenBashCompletionV2(f, true)
	case "fish":
		err = root.GenFishCompletion(f, true)
	}
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Stdout, "Installed %s completions:\n  %s\n\n", shell, target.Path)
	fmt.Fprintln(env.Stdout, target.Note)
	return nil
}
