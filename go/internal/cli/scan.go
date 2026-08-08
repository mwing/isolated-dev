package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/backend"
	"github.com/mwing/isolated-dev/go/internal/container"
	"github.com/mwing/isolated-dev/go/internal/scan"
	"github.com/mwing/isolated-dev/go/internal/trust"
)

func newScanCmd(env *Env) *cobra.Command {
	var (
		severity   string
		reportOnly bool
		image      string
	)
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan this project's image for known vulnerabilities",
		Long: "Runs whichever of trivy and grype are installed against the image\n" +
			"this project actually runs, including any tools added to it.\n\n" +
			"It exits non-zero when something is found at or above the\n" +
			"threshold, so CI can gate on it. A scan that reports problems and\n" +
			"exits successfully is a scan nothing can act on.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScan(cmd.Context(), env, severity, image, reportOnly)
		},
	}
	cmd.Flags().StringVar(&severity, "severity", "high",
		"lowest severity to fail on: low, medium, high or critical")
	cmd.Flags().BoolVar(&reportOnly, "report-only", false,
		"print findings but exit zero")
	cmd.Flags().StringVar(&image, "image", "", "scan this image instead of the project's")
	return cmd
}

func runScan(ctx context.Context, env *Env, severity, image string, reportOnly bool) error {
	pol, err := loadPolicy(env)
	if err != nil {
		return err
	}
	if floored, raised := pol.FloorSeverity(severity); raised {
		fmt.Fprintf(env.Stderr,
			"Policy raises the threshold from %s to %s.\n", severity, floored)
		severity = floored
	}

	sev, err := scan.ParseSeverity(severity)
	if err != nil {
		return err
	}

	scanners := scan.Available()
	if len(scanners) == 0 {
		return fmt.Errorf("no scanner installed; `brew install trivy` or `brew install grype`")
	}

	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	eng := container.New(env.driver(cfg.VMName))

	target := image
	if target == "" {
		store, err := trust.Load(env.Paths.Home, p.Dir)
		if err != nil {
			return err
		}
		// Scan what actually runs: the tools layer is part of the image a
		// container starts from, and its packages are as real as any other.
		target, err = ensureTools(ctx, env, eng, p, store, cfg)
		if err != nil {
			return err
		}
	}
	exists, err := eng.ImageExists(ctx, target)
	if err != nil {
		return err
	}
	if !exists {
		if image == "" {
			return fmt.Errorf("%s is not built yet; run `dev2 build` first", target)
		}
		// An image named explicitly may not be local yet: scanning
		// something before running it is a reasonable thing to want.
		fmt.Fprintf(env.Stdout, "Pulling %s…\n", target)
		if err := eng.Pull(ctx, target, env.Stderr); err != nil {
			return err
		}
	}

	names := make([]string, 0, len(scanners))
	for _, s := range scanners {
		names = append(names, s.Name)
	}
	fmt.Fprintf(env.Stdout, "Image:     %s\n", target)
	fmt.Fprintf(env.Stdout, "Scanners:  %s\n", strings.Join(names, ", "))
	fmt.Fprintf(env.Stdout, "Threshold: %s and above\n\n", sev)

	archive, cleanup, err := exportImage(ctx, env, eng, target)
	if err != nil {
		return err
	}
	defer cleanup()

	var results []scan.Result
	for _, s := range scanners {
		fmt.Fprintf(env.Stdout, "── %s ──\n", s.Name)
		results = append(results, scan.Run(ctx, env.Runner, s, archive, sev, env.Stdout))
		fmt.Fprintln(env.Stdout)
	}

	failed, reasons := scan.Summarize(results)
	if !failed {
		fmt.Fprintf(env.Stdout, "Clean at %s and above.\n", sev)
		return nil
	}
	for _, r := range reasons {
		fmt.Fprintf(env.Stderr, "  %s\n", r)
	}
	if reportOnly {
		fmt.Fprintln(env.Stderr, "\nExiting zero because --report-only was given.")
		return nil
	}
	return fmt.Errorf("scan failed at severity %s", sev)
}

// exportImage writes the image to a file the host scanners can read. The
// image lives in the VM and the scanners run here, so there is no shared
// path between them.
func exportImage(ctx context.Context, env *Env, eng *container.Engine, image string) (string, func(), error) {
	f, err := os.CreateTemp("", "dev2-scan-*.tar")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}

	fmt.Fprintf(env.Stdout, "Exporting %s…\n", image)
	res, err := eng.Backend.Docker(ctx, backend.Call{
		Args: []string{"save", image}, Stdout: f, Stderr: env.Stderr,
	})
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	if res.ExitCode != 0 {
		cleanup()
		return "", func() {}, fmt.Errorf("exporting %s failed", image)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return f.Name(), cleanup, nil
}
