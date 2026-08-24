package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/project"
)

func newPinCmd(env *Env) *cobra.Command {
	var update bool
	cmd := &cobra.Command{
		Use:   "pin",
		Short: "Pin this project's base images to digests",
		Long: "Resolves every image a build starts FROM — the Dockerfile's, and\n" +
			"the ones an agent overlay uses — to the digest it resolves to\n" +
			"today, and records it in the project file.\n\n" +
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

// PinChange records one base image moving.
type PinChange struct {
	Image string
	Old   string
	New   string
}

func pinImages(ctx context.Context, env *Env, update bool) error {
	changes, err := resolvePins(ctx, env, update, env.Stderr)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Fprintln(env.Stdout, "Nothing changed: every base image is already pinned "+
			"to what its tag points at.")
		return nil
	}
	for _, c := range changes {
		if c.Old == "" {
			fmt.Fprintf(env.Stdout, "  + %s\n      %s\n", c.Image, c.New)
			continue
		}
		fmt.Fprintf(env.Stdout, "  ~ %s\n      was %s\n      now %s\n", c.Image, c.Old, c.New)
	}
	fmt.Fprintf(env.Stdout, "\nRecorded in %s\n", env.Paths.Project)
	fmt.Fprintln(env.Stdout, "Commit it: a teammate then builds the same image, "+
		"not merely the same tag.")
	// An agent overlay is only rebuilt when its tag is missing, and the tag
	// does not carry the pins, so a recorded digest sits unused until one is
	// asked for. Said here, at the moment it becomes true.
	if intersects(changes, agentImages(env)) {
		fmt.Fprintln(env.Stdout, "An agent image moved: dev agent run <name> --rebuild "+
			"picks it up.")
	}
	return nil
}

func intersects(changes []PinChange, images []string) bool {
	in := map[string]bool{}
	for _, i := range images {
		in[i] = true
	}
	for _, c := range changes {
		if in[c.Image] {
			return true
		}
	}
	return false
}

// resolvePins resolves base images to digests and records them. It prints
// nothing about the outcome: `pin` and `update` report it differently, and
// two commands narrating the same thing twice is how output stops being
// read.
func resolvePins(ctx context.Context, env *Env, update bool, progress io.Writer) ([]PinChange, error) {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return nil, err
	}
	dockerfile, err := p.RenderedDockerfile()
	if err != nil {
		return nil, err
	}

	// The tool's own images count. An agent overlay is built FROM
	// `debian:bookworm-slim` and copies a runtime out of
	// `node:22-bookworm-slim`, both mutable tags — so the argument this
	// command makes about a project's bases applies to them unchanged, and
	// exempting them would make the rule advice rather than practice.
	projectImages := project.BaseImages(dockerfile)
	agentOnly := map[string]bool{}
	for _, i := range agentImages(env) {
		agentOnly[i] = true
	}
	for _, i := range projectImages {
		delete(agentOnly, i)
	}
	images := dedupe(append(projectImages, agentImages(env)...))
	if len(images) == 0 {
		fmt.Fprintln(env.Stdout, "Nothing to pin: every FROM is already a digest, "+
			"a stage name, or scratch.")
		return nil, nil
	}

	eng := container.New(env.driver(cfg.VMName))
	pins := map[string]string{}
	for k, v := range cfg.Pins {
		pins[k] = v
	}

	var changes []PinChange
	for _, image := range images {
		if _, ok := pins[image]; ok && !update {
			continue
		}
		// Pull first: a digest can only be read from an image the daemon
		// has, and the local copy may predate what the tag points at now.
		//
		// An agent's image failing is reported and skipped, not fatal. The
		// registry holds every agent definition on the machine, including
		// files this project never runs, so one private or mistyped image
		// name would otherwise break `dev pin` — and `dev update`, which
		// resolves pins on the way past — for a project that has nothing to
		// do with it. A base image of the project's own is different: it is
		// what the build starts from, and pinning half of it silently is
		// worse than stopping.
		if err := eng.Pull(ctx, image, progress); err != nil {
			if agentOnly[image] {
				fmt.Fprintf(progress, "⚠  skipping %s: %v\n", image, err)
				continue
			}
			return nil, err
		}
		digest, err := eng.Digest(ctx, image)
		if err != nil {
			if agentOnly[image] {
				fmt.Fprintf(progress, "⚠  skipping %s: %v\n", image, err)
				continue
			}
			return nil, err
		}
		if pins[image] == digest {
			continue
		}
		changes = append(changes, PinChange{Image: image, Old: pins[image], New: digest})
		pins[image] = digest
	}

	if len(changes) == 0 {
		return nil, nil
	}
	if err := writeProjectPins(env.Paths.Project, pins); err != nil {
		return nil, err
	}
	return changes, nil
}

// agentImages are the upstream images every known agent's overlay builds
// FROM. A registry that cannot be read is not fatal here: pinning the
// project's own bases is still worth doing, and the alternative is a
// command that fails over an unrelated agent file.
func agentImages(env *Env) []string {
	reg, err := registry(env)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range reg.List() {
		out = append(out, a.Images()...)
	}
	return out
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
	b.WriteString("# Re-resolve deliberately with `dev pin --update`.\n")
	b.WriteString("pins:\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "  %q: %q\n", k, pins[k])
	}
	return replaceBlock(path, "pins:", b.String())
}
