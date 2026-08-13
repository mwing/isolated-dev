package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
	// What stops a run, gathered as it is found. "Fix the items marked ✗
	// above" asks the reader to scan back through six sections for a mark
	// they may not be able to see; the verdict can just say it.
	var blockers []string

	fmt.Fprintf(out, "%s\n\n", sectionStyle.Render("dev doctor"))

	section(out, "Host")
	fmt.Fprintf(out, "  platform:      %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(out, "  project dir:   %s\n", env.Paths.ProjectDir)

	cfg, err := config.Load(env.Paths, env.Env)
	if err != nil {
		fmt.Fprintln(out)
		section(out, "Configuration")
		fmt.Fprintf(out, "  %s %v\n", failStyle.Render("✗"), err)
		return err
	}

	fmt.Fprintln(out)
	section(out, "Configuration")
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
		fmt.Fprintf(out, "  %s  %s\n", warnStyle.Render("⚠"), n)
	}

	fmt.Fprintln(out)
	section(out, "Backend")
	drv := env.driver(cfg.VMName)
	backendUsable := false
	st, err := drv.Probe(cmd.Context())
	if err != nil {
		fmt.Fprintf(out, "  %s probing %s: %v\n", failStyle.Render("✗"), drv.Name(), err)
		blockers = append(blockers, fmt.Sprintf("the %s backend could not be probed: %v", drv.Name(), err))
		ok = false
	} else {
		backendUsable = reportStatus(out, st)
		ok = backendUsable && ok
		if !backendUsable {
			blockers = append(blockers,
				firstNonEmpty(st.Detail, "the container backend is not usable"))
		}
	}

	// A missing sidecar image blocks every agent run, and without this it
	// only surfaced once a run had already built the overlay. Asking the
	// daemon is a read: doctor never builds the image it reports on.
	fmt.Fprintln(out)
	section(out, "Egress sidecar")
	if backendUsable {
		present := reportProxyImage(cmd.Context(), out, container.New(drv))
		if !present {
			blockers = append(blockers, "the egress sidecar image is missing")
		}
		ok = present && ok
	} else {
		fmt.Fprintf(out, "  %s  image %s %s\n", dimStyle.Render("?"), proxyImageTag,
			dimStyle.Render("(not checked: backend unavailable)"))
	}

	// v1 had `dev disk` and `dev troubleshoot` as separate commands.
	// Nobody runs a disk command; people run doctor when something is
	// wrong, and disk pressure is one of the things that is wrong.
	fmt.Fprintln(out)
	section(out, "Disk")
	if backendUsable {
		reportDisk(cmd.Context(), env, out, container.New(drv))
	} else {
		fmt.Fprintf(out, "  %s  %s\n", dimStyle.Render("?"),
			dimStyle.Render("not checked: backend unavailable"))
	}

	fmt.Fprintln(out)
	section(out, "This project")
	reportProjectHygiene(out, env)

	fmt.Fprintln(out)
	if !ok {
		fmt.Fprintln(out, failStyle.Render("Not ready."))
		for _, b := range blockers {
			fmt.Fprintf(out, "  %s  %s\n", arrowStyle.Render("→"), b)
		}
		return fmt.Errorf("doctor: environment not ready")
	}
	fmt.Fprintln(out, okStyle.Render("Ready."))
	return nil
}

// section writes a heading. Bold rather than underlined or boxed: the
// output is a report someone skims for one line, and headings that draw
// more attention than the marks under them invert what matters.
func section(out interface{ Write([]byte) (int, error) }, name string) {
	fmt.Fprintf(out, "%s\n", sectionStyle.Render(name))
}

// reportDisk shows what this tool is using on the daemon, which is the
// number behind "my disk is full" far more often than anything in the
// project directory.
func reportDisk(ctx context.Context, env *Env, out interface{ Write([]byte) (int, error) },
	eng *container.Engine) {
	usage, err := eng.DiskUsage(ctx)
	if err != nil {
		fmt.Fprintf(out, "  %s  %v\n", dimStyle.Render("?"), err)
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

	rep, err := measureContext(dir)
	if err != nil && rep.Files == 0 {
		fmt.Fprintf(out, "  .dockerignore: absent\n")
		return
	}
	atLeast := ""
	if rep.Truncated {
		atLeast = "at least "
	}
	fmt.Fprintf(out, "  .dockerignore: absent — every build sends %s%s (%s%d files) to the daemon\n",
		atLeast, humanSize(rep.Bytes), atLeast, rep.Files)
	if named := namedEntries(rep.Largest, 3); named != "" {
		fmt.Fprintf(out, "  %-15s %s\n", "biggest:", named)
	}
	if rep.Bytes > contextWarnBytes {
		fmt.Fprintf(out, "  %s  add one: .git, node_modules, .venv, dist, build\n", warnStyle.Render("⚠"))
	}
}

func reportStatus(out interface{ Write([]byte) (int, error) }, st backend.Status) bool {
	fmt.Fprintf(out, "  driver:        %s\n", st.Backend)

	cli := firstNonEmpty(st.CLIName, "container CLI")
	fmt.Fprintf(out, "  %s  %s", mark(st.CLIFound), cli)
	if st.CLIPath != "" {
		fmt.Fprintf(out, " %s", dimStyle.Render("("+st.CLIPath+")"))
	}
	fmt.Fprintln(out)

	if st.CLIFound {
		// A backend with no VM says so in VMName rather than pretending to
		// have one, so the two VM lines would be noise repeating it.
		if st.VMName != "" && !strings.HasPrefix(st.VMName, "(none") {
			fmt.Fprintf(out, "  %s  VM %q exists\n", mark(st.VMExists), st.VMName)
			fmt.Fprintf(out, "  %s  VM running\n", mark(st.VMRunning))
		}
		fmt.Fprintf(out, "  %s  docker daemon", mark(st.DaemonUp))
		if st.DaemonVersion != "" {
			fmt.Fprintf(out, " %s", dimStyle.Render("(server "+st.DaemonVersion+")"))
		}
		fmt.Fprintln(out)
	}
	if st.Detail != "" {
		fmt.Fprintf(out, "  %s  %s\n", arrowStyle.Render("→"), st.Detail)
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
		return okStyle.Render("✓")
	}
	return failStyle.Render("✗")
}

func presence(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "(present)"
	}
	return "(absent)"
}
