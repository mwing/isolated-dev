package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/console"
	"github.com/mwing/isolated-dev/go/internal/container"
	"github.com/mwing/isolated-dev/go/internal/netpolicy"
	"github.com/mwing/isolated-dev/go/internal/project"
	"github.com/mwing/isolated-dev/go/internal/trust"
)

func newConsoleCmd(env *Env) *cobra.Command {
	var (
		command    string
		rebuild    bool
		extraHosts []string
	)

	cmd := &cobra.Command{
		Use:   "console [-- args...]",
		Short: "Run with a live view: output, egress decisions, and prompts",
		Long: "The console owns the screen, which is what makes a blocking prompt\n" +
			"workable: outside it a prompt and the workload fight over stdin.\n\n" +
			"It is a view over the same code `run` uses, not a second way to\n" +
			"run things — everything here has a non-interactive equivalent, so\n" +
			"nothing becomes console-only.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConsole(cmd.Context(), env, splitCommand(command, args), rebuild, extraHosts)
		},
	}
	cmd.Flags().StringVarP(&command, "command", "c", "", "command to run")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "rebuild the image first")
	cmd.Flags().StringArrayVar(&extraHosts, "allow-host", nil, "add a destination for this run")
	return cmd
}

func runConsole(ctx context.Context, env *Env, command []string, rebuild bool, extraHosts []string) error {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	store, err := trust.Load(env.Paths.Home, p.Dir)
	if err != nil {
		return err
	}
	if err := enforceConsent(env, cfg, store); err != nil {
		return err
	}
	if p.Network != project.NetworkAllowlist {
		return fmt.Errorf("console needs `network: allowlist`; this project is %q, "+
			"and with nothing filtered there is nothing to decide", p.Network)
	}
	if len(command) == 0 {
		return fmt.Errorf("console needs a command: -c '<cmd>' or -- <argv>\n" +
			"(an interactive shell inside the console needs a pty per pane, " +
			"which is not built yet)")
	}

	eng := container.New(env.driver(cfg.VMName))
	exists, err := eng.ImageExists(ctx, p.Image)
	if err != nil {
		return err
	}
	if rebuild || !exists {
		if err := buildImage(ctx, env, cfg, p, ""); err != nil {
			return err
		}
	}

	image, err := ensureTools(ctx, env, eng, p, store)
	if err != nil {
		return err
	}

	allowed := append(p.Registries(), store.Resolve("default").AllowHosts...)
	allowed = append(allowed, extraHosts...)

	side, topo, err := startSidecar(ctx, eng, p, allowed, EgressAsk)
	if err != nil {
		return err
	}

	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()

	model := console.New(
		fmt.Sprintf("dev2 console — %s", p.Name),
		fmt.Sprintf("allowlist · %d destination(s)", len(allowed)),
		console.Actions{
			Grant: func(host string, d console.Decision) error {
				// Apply to the running sidecar first: that is what releases
				// the held request. Recording it afterwards is bookkeeping
				// and must not delay the connection.
				if err := side.Grant(runCtx, "allow", host); err != nil {
					return err
				}
				if d == console.DecideProject {
					_, err := store.Grant(store.Project, "default", []string{host})
					return err
				}
				return nil
			},
			Quit: stopRun,
		})

	prog := tea.NewProgram(model, tea.WithContext(ctx))

	go streamEvents(runCtx, eng, topo, prog)
	go runWorkload(runCtx, eng, p, cfg, command, topo, prog, image)

	if _, err := prog.Run(); err != nil {
		_, _ = side.Stop(context.WithoutCancel(ctx))
		if strings.Contains(err.Error(), "/dev/tty") {
			return fmt.Errorf("the console needs a terminal, and this session has none.\n" +
				"Use `dev2 run --egress-prompt ask` for the same blocking prompts\n" +
				"without the full-screen view.")
		}
		return err
	}

	summary, stopErr := side.Stop(context.WithoutCancel(ctx))
	for _, line := range summary {
		fmt.Fprintf(env.Stderr, "  %s\n", line)
	}
	if stopErr != nil {
		fmt.Fprintf(env.Stderr, "warning: %v\n", stopErr)
	}
	if err := model.Err(); err != nil {
		return err
	}
	if code := model.ExitCode(); code != 0 {
		return fmt.Errorf("exited with status %d", code)
	}
	return nil
}

// streamEvents forwards sidecar decisions into the UI.
func streamEvents(ctx context.Context, eng *container.Engine,
	topo netpolicy.Topology, prog *tea.Program) {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_ = eng.LogsFollow(ctx, topo.SidecarName, pw)
	}()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e netpolicy.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		prog.Send(console.EventMsg(e))
	}
}

// runWorkload runs the container, forwarding its output line by line.
func runWorkload(ctx context.Context, eng *container.Engine, p *project.Project,
	cfg config.Config, command []string, topo netpolicy.Topology, prog *tea.Program,
	image string) {
	spec := p.RunSpec(cfg, command, false)
	spec.Image = image
	spec.Network = topo.InternalNetwork
	spec.DNS = []string{topo.SidecarIP}
	spec.Env = append(spec.Env, topo.Env()...)
	// The console owns the screen; the workload gets no stdin and no
	// published ports, since an internal network has no gateway anyway.
	spec.Interactive = false
	spec.Ports = nil

	pr, pw := io.Pipe()
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			prog.Send(console.OutputMsg(sc.Text()))
		}
	}()

	res, err := eng.Run(ctx, spec, nil, pw, pw)
	pw.Close()
	prog.Send(console.DoneMsg{Err: err, ExitCode: res.ExitCode})
}
