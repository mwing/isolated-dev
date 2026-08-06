package cli

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/backend"
	"github.com/mwing/isolated-dev/go/internal/backend/orbstack"
	"github.com/mwing/isolated-dev/go/internal/config"
)

func newDoctorCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local setup without changing it",
		Long: "doctor reports what dev2 can see: configuration layers, the\n" +
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

	fmt.Fprintf(out, "dev2 doctor\n\n")

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
		fmt.Fprintf(out, "    (v2 will confirm these on first use; see ROADMAP 4.2)\n")
	}

	for _, n := range cfg.Notes {
		fmt.Fprintf(out, "  ⚠  %s\n", n)
	}

	fmt.Fprintf(out, "\nBackend\n")
	drv := orbstack.New(cfg.VMName, env.Runner)
	st, err := drv.Probe(cmd.Context())
	if err != nil {
		fmt.Fprintf(out, "  ✗ probing %s: %v\n", drv.Name(), err)
		ok = false
	} else {
		ok = reportStatus(out, st) && ok
	}

	fmt.Fprintln(out)
	if !ok {
		fmt.Fprintf(out, "Not ready. Fix the items marked ✗ above.\n")
		return fmt.Errorf("doctor: environment not ready")
	}
	fmt.Fprintf(out, "Ready.\n")
	return nil
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
