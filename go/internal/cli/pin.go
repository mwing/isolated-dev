package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/container"
	"github.com/mwing/isolated-dev/go/internal/project"
)

func newPinCmd(env *Env) *cobra.Command {
	var update bool
	cmd := &cobra.Command{
		Use:   "pin",
		Short: "Pin this project's base images to digests",
		Long: "Resolves every image the Dockerfile builds FROM to the digest it\n" +
			"resolves to today, and records it in the project file.\n\n" +
			"A tag says which image you meant; a digest says which image you\n" +
			"got. Builds run unfiltered and fetch whatever the tag points at\n" +
			"now, so a tag is the one part of the sandbox that can change\n" +
			"under you without anything being edited.\n\n" +
			"Re-resolve deliberately with --update; that is the point of\n" +
			"pinning, not a limitation of it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return pinImages(cmd.Context(), env, update)
		},
	}
	cmd.Flags().BoolVar(&update, "update", false,
		"re-resolve images that are already pinned")
	return cmd
}

func pinImages(ctx context.Context, env *Env, update bool) error {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	dockerfile, err := p.RenderedDockerfile()
	if err != nil {
		return err
	}

	images := project.BaseImages(dockerfile)
	if len(images) == 0 {
		fmt.Fprintln(env.Stdout, "Nothing to pin: every FROM is already a digest, "+
			"a stage name, or scratch.")
		return nil
	}

	eng := container.New(env.driver(cfg.VMName))
	pins := map[string]string{}
	for k, v := range cfg.Pins {
		pins[k] = v
	}

	changed := 0
	for _, image := range images {
		if existing, ok := pins[image]; ok && !update {
			fmt.Fprintf(env.Stdout, "  = %s\n      %s\n", image, existing)
			continue
		}
		// Pull first: a digest can only be read from an image the daemon
		// has, and the local copy may predate what the tag points at now.
		if err := eng.Pull(ctx, image, env.Stderr); err != nil {
			return err
		}
		digest, err := eng.Digest(ctx, image)
		if err != nil {
			return err
		}
		if pins[image] == digest {
			fmt.Fprintf(env.Stdout, "  = %s\n      unchanged\n", image)
			continue
		}
		if old, ok := pins[image]; ok {
			fmt.Fprintf(env.Stdout, "  ~ %s\n      was %s\n      now %s\n", image, old, digest)
		} else {
			fmt.Fprintf(env.Stdout, "  + %s\n      %s\n", image, digest)
		}
		pins[image] = digest
		changed++
	}

	if changed == 0 {
		fmt.Fprintln(env.Stdout, "\nNothing changed.")
		return nil
	}
	if err := writeProjectPins(env.Paths.Project, pins); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "\nRecorded in %s\n", env.Paths.Project)
	fmt.Fprintln(env.Stdout, "Commit it: a teammate then builds the same image, "+
		"not merely the same tag.")
	return nil
}

// writeProjectPins updates the pins block, leaving the rest of the project
// file alone.
func writeProjectPins(path string, pins map[string]string) error {
	keys := make([]string, 0, len(pins))
	for k := range pins {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# Base images pinned to the digests they resolved to. A tag says\n")
	b.WriteString("# which image you meant; a digest says which image you got.\n")
	b.WriteString("# Re-resolve deliberately with `dev2 pin --update`.\n")
	b.WriteString("pins:\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "  %q: %q\n", k, pins[k])
	}
	return replaceBlock(path, "pins:", b.String())
}
