package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Build metadata, set by goreleaser via -ldflags. Defaults describe a
// `go build` from a working tree.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func newVersionCmd(env *Env) *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			v, c := Version, Commit
			if c == "" {
				c = vcsRevision()
			}
			if short {
				fmt.Fprintln(env.Stdout, v)
				return nil
			}
			fmt.Fprintf(env.Stdout, "dev2 %s\n", v)
			if c != "" {
				fmt.Fprintf(env.Stdout, "  commit:   %s\n", c)
			}
			if Date != "" {
				fmt.Fprintf(env.Stdout, "  built:    %s\n", Date)
			}
			fmt.Fprintf(env.Stdout, "  go:       %s\n", runtime.Version())
			fmt.Fprintf(env.Stdout, "  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print just the version string")
	return cmd
}

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return ""
}
