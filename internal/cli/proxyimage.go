package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	assets "github.com/mwing/isolated-dev"
	"github.com/mwing/isolated-dev/internal/container"
)

// SourceLabel records which build of this tool produced a sidecar image.
//
// The image is where egress is actually enforced, so an old one enforces an
// old policy while the binary reports the new one. That is not theoretical:
// verifying the SNI check against a live proxy, a mismatched name completed
// its handshake because the image predated the check by five commits.
// Nothing reported the skew — the image was present, the run called itself
// filtered, and the bypass just closed was open.
const proxySourceLabel = "dev.proxy.source"

// This lives in the CLI, not in netpolicy, because netpolicy is compiled
// into the sidecar itself. A dependency from there on the embedded assets
// would put a copy of this tool's entire source inside the image the
// sidecar runs from — found by the inner build failing, which was the
// cheap version of that lesson.
//
// buildProxyImage returns a sidecar image built from this binary's own
// source, building it when it is missing or was built by a different
// version.
//
// Building here rather than in a Makefile is what makes a release binary
// usable: the sidecar is mandatory for every filtered run, and requiring a
// checkout to produce it meant the flagship feature did not work from a
// clean install. The build itself runs inside a golang image, so the host
// needs no toolchain.
func ensureProxyImageBuilt(ctx context.Context, eng *container.Engine, tag, workDir string,
	out io.Writer) (string, error) {
	want := assets.SourceHash()

	exists, err := eng.ImageExists(ctx, tag)
	if err != nil {
		return "", err
	}
	if exists {
		got, err := eng.ImageLabel(ctx, tag, proxySourceLabel)
		if err != nil {
			return "", err
		}
		if got == want {
			return tag, nil
		}
		// Rebuilt rather than refused. Refusing would be correct and
		// useless: the user cannot act on it except by running the command
		// this would have run, and an unbuilt sidecar is not a state worth
		// preserving. What matters is that the stale one is never used.
		if got == "" {
			fmt.Fprintf(out, "Rebuilding the egress sidecar: %s carries no build marker, "+
				"so what it enforces is unknown.\n", tag)
		} else {
			fmt.Fprintf(out, "Rebuilding the egress sidecar: %s was built from %s, "+
				"this binary carries %s.\n", tag, got, want)
		}
	} else {
		fmt.Fprintf(out, "Building the egress sidecar image %s (first run).\n", tag)
	}

	if err := runProxyBuild(ctx, eng, tag, want, workDir, out); err != nil {
		return "", err
	}
	return tag, nil
}

// buildProxyImage materializes the embedded source and builds it.
func runProxyBuild(ctx context.Context, eng *container.Engine, tag, hash, workDir string,
	out io.Writer) error {
	// Under the user's own directory rather than the system temp: the
	// daemon runs in a VM that sees the host filesystem, and what it shares
	// is the home directory. A context the daemon cannot read fails with a
	// message about the Dockerfile, which is the wrong thing to look at.
	dir, err := os.MkdirTemp(workDir, "proxy-build-")
	if err != nil {
		return fmt.Errorf("preparing the sidecar build: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if err := assets.WriteProxySource(dir); err != nil {
		return fmt.Errorf("writing the sidecar source: %w", err)
	}

	spec := container.BuildSpec{
		Tag:        tag,
		Context:    dir,
		Dockerfile: filepath.Join(dir, "Dockerfile.proxy"),
		Labels:     map[string]string{proxySourceLabel: hash},
	}
	if err := eng.Build(ctx, spec, nil, out); err != nil {
		return fmt.Errorf("building the sidecar image: %w", err)
	}
	return nil
}
