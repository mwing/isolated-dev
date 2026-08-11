package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/backend"
	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/container"
)

func newDoctorCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local setup without changing it",
		Long: "doctor reports what dev can see: configuration layers, the\n" +
			"container backend, and anything that would block a run. It never\n" +
			"starts a VM or repairs state; diagnosis and mutation stay separate.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd, env)
		},
	}
}

func runDoctor(cmd *cobra.Command, env *Env) error {
	out := env.Stdout
	ok := true

	fmt.Fprintf(out, "dev doctor\n\n")

	fmt.Fprintf(out, "Host\n")
	fmt.Fprintf(out, "  platform:      %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(out, "  project dir:   %s\n", env.Paths.ProjectDir)

	cfg, err := config.Load(env.Paths, env.Env)
	if err != nil {
		fmt.Fprintf(out, "\nConfiguration\n  ✗ %v\n", err)
		return err
	}

	fmt.Fprintf(out, "\nConfiguration\n")
	fmt.Fprintf(out, "  global:        %s %s\n", env.Paths.Global, presence(env.Paths.Global))
	fmt.Fprintf(out, "  project:       %s %s\n", env.Paths.Project, presence(env.Paths.Project))
	fmt.Fprintf(out, "  vm_name:       %s (%s)\n", cfg.VMName, cfg.Origin("vm_name"))
	fmt.Fprintf(out, "  auto_start_vm: %t (%s)\n", cfg.AutoStartVM, cfg.Origin("auto_start_vm"))

	asks := cfg.Asks()
	if asks.Empty() {
		fmt.Fprintf(out, "  grants:        none (sandbox defaults)\n")
	} else {
		fmt.Fprintf(out, "  grants requested by config:\n")
		for _, line := range asks.Describe() {
			fmt.Fprintf(out, "    • %s\n", line)
		}
		fmt.Fprintf(out, "    (a request, not a grant: `dev accept` before any run honors it)\n")
	}

	for _, n := range cfg.Notes {
		fmt.Fprintf(out, "  ⚠  %s\n", n)
	}

	fmt.Fprintf(out, "\nBackend\n")
	drv := env.driver(cfg.VMName)
	backendUsable := false
	st, err := drv.Probe(cmd.Context())
	if err != nil {
		fmt.Fprintf(out, "  ✗ probing %s: %v\n", drv.Name(), err)
		ok = false
	} else {
		backendUsable = reportStatus(out, st)
		ok = backendUsable && ok
	}

	// A missing sidecar image blocks every agent run, and without this it
	// only surfaced once a run had already built the overlay. Asking the
	// daemon is a read: doctor never builds the image it reports on.
	fmt.Fprintf(out, "\nEgress sidecar\n")
	if backendUsable {
		ok = reportProxyImage(cmd.Context(), out, container.New(drv)) && ok
	} else {
		fmt.Fprintf(out, "  ?  image %s (not checked: backend unavailable)\n", proxyImageTag)
	}

	// v1 had `dev disk` and `dev troubleshoot` as separate commands.
	// Nobody runs a disk command; people run doctor when something is
	// wrong, and disk pressure is one of the things that is wrong.
	fmt.Fprintf(out, "\nDisk\n")
	if backendUsable {
		reportDisk(cmd.Context(), env, out, container.New(drv))
	} else {
		fmt.Fprintf(out, "  ?  not checked: backend unavailable\n")
	}

	fmt.Fprintf(out, "\nThis project\n")
	reportProjectHygiene(out, env)

	fmt.Fprintln(out)
	if !ok {
		fmt.Fprintf(out, "Not ready. Fix the items marked ✗ above.\n")
		return fmt.Errorf("doctor: environment not ready")
	}
	fmt.Fprintf(out, "Ready.\n")
	return nil
}

// reportDisk shows what this tool is using on the daemon, which is the
// number behind "my disk is full" far more often than anything in the
// project directory.
func reportDisk(ctx context.Context, env *Env, out interface{ Write([]byte) (int, error) },
	eng *container.Engine) {
	usage, err := eng.DiskUsage(ctx)
	if err != nil {
		fmt.Fprintf(out, "  ?  %v\n", err)
	}
	for _, line := range usage {
		fmt.Fprintf(out, "  %s\n", line)
	}

	// What this tool is using, which is the part a reader can act on. The
	// totals above belong to the daemon and include everything else on it;
	// listing `dev clean` under them implied otherwise.
	clones, cerr := scanClones(ctx, env.Runner, clonesRoot(env))
	if cerr == nil && len(clones) > 0 {
		var total int64
		var holding int
		for _, c := range clones {
			total += c.Size
			if c.Dirty > 0 || c.Unmerged > 0 {
				holding++
			}
		}
		// Same column widths as the daemon's rows above, so the two read
		// as one table rather than as a table and an afterthought.
		fmt.Fprintf(out, "  %-15s %4d clones %10s", "dev clones:", len(clones), humanSize(total))
		if holding > 0 {
			fmt.Fprintf(out, "  %d holding work", holding)
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  %-15s dev clone prune, dev clean --all\n", "free space:")
		return
	}
	fmt.Fprintf(out, "  %-15s dev clean --all\n", "free space:")
}

// reportProjectHygiene names the things that make a project slow or
// surprising rather than broken. A build that ships gigabytes to the
// daemon is invisible until someone reads a build log, and a working tree
// with no .dockerignore is the usual reason.
func reportProjectHygiene(out interface{ Write([]byte) (int, error) }, env *Env) {
	dir := env.Paths.ProjectDir
	ignore := filepath.Join(dir, ".dockerignore")
	if _, err := os.Stat(ignore); err == nil {
		fmt.Fprintf(out, "  .dockerignore: present\n")
		return
	}

	size, files, err := contextSize(dir)
	if err != nil {
		fmt.Fprintf(out, "  .dockerignore: absent\n")
		return
	}
	fmt.Fprintf(out, "  .dockerignore: absent — every build sends %s (%d files) to the daemon\n",
		humanSize(size), files)
	if size > 200<<20 {
		fmt.Fprintf(out, "  ⚠  add one: .git, node_modules, .venv, dist, build\n")
	}
}

// contextSize measures what docker would send. It stops early: the answer
// only has to be good enough to say "this is large", and walking a
// multi-gigabyte tree to report a number nobody reads precisely would make
// doctor the slow command.
func contextSize(dir string) (int64, int, error) {
	const limit = 20000
	var total int64
	var count int
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		count++
		if count > limit {
			return filepath.SkipAll
		}
		return nil
	})
	return total, count, err
}

func reportStatus(out interface{ Write([]byte) (int, error) }, st backend.Status) bool {
	fmt.Fprintf(out, "  driver:        %s\n", st.Backend)
	fmt.Fprintf(out, "  %s  orb CLI", mark(st.CLIFound))
	if st.CLIPath != "" {
		fmt.Fprintf(out, " (%s)", st.CLIPath)
	}
	fmt.Fprintln(out)

	if st.CLIFound {
		fmt.Fprintf(out, "  %s  VM %q exists\n", mark(st.VMExists), st.VMName)
		fmt.Fprintf(out, "  %s  VM running\n", mark(st.VMRunning))
		fmt.Fprintf(out, "  %s  docker daemon", mark(st.DaemonUp))
		if st.DaemonVersion != "" {
			fmt.Fprintf(out, " (server %s)", st.DaemonVersion)
		}
		fmt.Fprintln(out)
	}
	if st.Detail != "" {
		fmt.Fprintf(out, "  →  %s\n", st.Detail)
	}
	return st.Ready()
}

// reportProxyImage reports whether the egress sidecar image is present,
// naming the one command that fixes it. Building here would make doctor a
// mutation, which is exactly the line this command does not cross.
func reportProxyImage(ctx context.Context, out interface{ Write([]byte) (int, error) }, eng *container.Engine) bool {
	exists, err := eng.ImageExists(ctx, proxyImageTag)
	if err != nil {
		fmt.Fprintf(out, "  ✗  image %s: %v\n", proxyImageTag, err)
		return false
	}
	fmt.Fprintf(out, "  %s  image %s\n", mark(exists), proxyImageTag)
	if !exists {
		fmt.Fprintf(out, "  →  built automatically on the next run, from the source "+
			"this binary carries\n")
	}
	return exists
}

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func presence(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "(present)"
	}
	return "(absent)"
}
