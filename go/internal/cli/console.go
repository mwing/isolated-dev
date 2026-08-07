package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/go/internal/agent"
	"github.com/mwing/isolated-dev/go/internal/config"
	"github.com/mwing/isolated-dev/go/internal/console"
	"github.com/mwing/isolated-dev/go/internal/container"
	"github.com/mwing/isolated-dev/go/internal/netpolicy"
	"github.com/mwing/isolated-dev/go/internal/project"
	"github.com/mwing/isolated-dev/go/internal/runner"
	"github.com/mwing/isolated-dev/go/internal/trust"
)

func newConsoleCmd(env *Env) *cobra.Command {
	var (
		command    string
		rebuild    bool
		extraHosts []string
		shell      bool
		agentName  string
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
			return runConsole(cmd.Context(), env, splitCommand(command, args),
				rebuild, extraHosts, shell, agentName)
		},
	}
	cmd.Flags().StringVarP(&command, "command", "c", "", "command to run")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "rebuild the image first")
	cmd.Flags().StringArrayVar(&extraHosts, "allow-host", nil, "add a destination for this run")
	cmd.Flags().BoolVar(&shell, "shell", false,
		"treat the command as interactive: give it a terminal and the keyboard")
	cmd.Flags().StringVar(&agentName, "agent", "",
		"run this agent in the console, using its stored login")
	return cmd
}

func runConsole(ctx context.Context, env *Env, command []string, rebuild bool,
	extraHosts []string, interactive bool, agentName string) error {
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
	if len(command) == 0 && agentName == "" {
		command = []string{"/bin/bash"}
		interactive = true
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

	var (
		image     string
		agentOpts *agent.Options
		allowed   []string
	)
	if agentName != "" {
		agentOpts, image, allowed, err = prepareAgent(ctx, env, eng, p, store, cfg, agentName, command)
		if err != nil {
			return err
		}
		// An agent gets a terminal whether or not a command was given: its
		// whole interface is interactive.
		interactive = true
	} else {
		image, err = ensureTools(ctx, env, eng, p, store)
		if err != nil {
			return err
		}
		allowed = append(p.Registries(), store.Resolve("default").AllowHosts...)
	}
	allowed = append(allowed, extraHosts...)

	side, topo, err := startSidecar(ctx, eng, p, allowed, EgressAsk)
	if err != nil {
		return err
	}

	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()

	var term *console.Terminal
	if interactive {
		term = console.NewTerminal(0, 0)
	}

	model := console.New(
		fmt.Sprintf("dev2 console — %s", p.Name),
		fmt.Sprintf("allowlist · %d destination(s)", len(allowed)),
		console.Actions{
			Grant: func(host string, d console.Decision) error {
				// A "no" has to reach the sidecar too. Without it every
				// retry is held for the full timeout again while the
				// console stays silent, which is indistinguishable from a
				// hang.
				if d == console.DecideNo {
					return side.Grant(runCtx, "refuse", host)
				}
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
	model.Term = term

	prog := tea.NewProgram(model, tea.WithContext(ctx))

	go streamEvents(runCtx, eng, topo, prog)
	if term != nil && agentOpts != nil {
		go runAgentInteractive(runCtx, eng, *agentOpts, topo, prog, term)
	} else if term != nil {
		go runInteractive(runCtx, eng, p, cfg, command, topo, prog, image, term)
	} else {
		go runWorkload(runCtx, eng, p, cfg, command, topo, prog, image)
	}

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

// prepareAgent builds the agent's image, volume and run options, reusing
// exactly what `dev2 agent run` uses — the console is a view, not a second
// way to run an agent. The stored login comes with it: the agent's home is
// the same named volume, so a session started here is already
// authenticated.
func prepareAgent(ctx context.Context, env *Env, eng *container.Engine, p *project.Project,
	store *trust.Store, cfg config.Config, name string, command []string) (*agent.Options, string, []string, error) {
	reg, err := registry(env)
	if err != nil {
		return nil, "", nil, err
	}
	a, err := reg.Get(name)
	if err != nil {
		return nil, "", nil, err
	}

	// The project's request counts here exactly as it does for `dev2 agent
	// run`. Resolving it differently would mean the same agent ran on a
	// different image depending on which command started it.
	request := projectRequest(cfg, a.Name)
	if pending := store.Pending(a.Name, request); len(pending) > 0 {
		return nil, "", nil, fmt.Errorf(
			"%s requests egress you have not accepted: %s\nReview with: dev2 agent accept --agent %s",
			env.Paths.Project, strings.Join(pending, " "), a.Name)
	}

	saved := store.Resolve(a.Name)
	base := saved.Base
	if base == "" {
		base = request.Base
	}
	opts := agent.Options{
		Agent:       a,
		Project:     p.Dir,
		Interactive: true,
		Command:     command,
		Image:       base,
		Memory:      firstSet(saved.Memory, request.Memory),
		CPUs:        firstSet(saved.CPUs, request.CPUs),
	}

	runner := &agent.Runner{Engine: eng, Out: env.Stdout}
	image, err := runner.EnsureImage(ctx, opts, false)
	if err != nil {
		return nil, "", nil, err
	}
	if err := runner.EnsureVolume(ctx, a); err != nil {
		return nil, "", nil, err
	}

	allowed := append(opts.Allowlist(), saved.AllowHosts...)
	allowed = append(allowed, store.AcceptedRequest(a.Name, request)...)
	return &opts, image, allowed, nil
}

func firstSet(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// runAgentInteractive runs an agent on a pseudo-terminal inside the
// console, so its blocked destinations become questions rather than
// failures it has to work around.
func runAgentInteractive(ctx context.Context, eng *container.Engine, opts agent.Options,
	topo netpolicy.Topology, prog *tea.Program, term *console.Terminal) {
	spec := agent.Spec(opts, topo)
	spec.TTY = true
	spec.Env = append(spec.Env, "TERM=xterm-256color")
	spec.Ports = nil

	var dirty atomic.Bool
	go repaint(ctx, prog, &dirty)

	res, err := eng.RunPTY(ctx, spec, redrawWriter{term: term, dirty: &dirty}, &runner.PTY{
		Rows: 24, Cols: 80, Ready: term.Attach,
	})
	prog.Send(console.DoneMsg{Err: err, ExitCode: res.ExitCode})
}

// runInteractive runs the workload on a pseudo-terminal and feeds its
// screen into the console's emulator, so a shell behaves like a shell
// while the console keeps the surrounding layout.
func runInteractive(ctx context.Context, eng *container.Engine, p *project.Project,
	cfg config.Config, command []string, topo netpolicy.Topology, prog *tea.Program,
	image string, term *console.Terminal) {
	spec := p.RunSpec(cfg, command, true)
	spec.Image = image
	spec.Network = topo.InternalNetwork
	spec.DNS = []string{topo.SidecarIP}
	spec.Env = append(spec.Env, topo.Env()...)
	spec.Env = append(spec.Env, "TERM=xterm-256color")
	spec.Ports = nil

	var dirty atomic.Bool
	go repaint(ctx, prog, &dirty)

	res, err := eng.RunPTY(ctx, spec, redrawWriter{term: term, dirty: &dirty}, &runner.PTY{
		Rows: 24, Cols: 80,
		Ready: term.Attach,
	})
	prog.Send(console.DoneMsg{Err: err, ExitCode: res.ExitCode})
}

// redrawWriter feeds workload bytes into the emulator and marks the screen
// dirty. bubbletea only redraws on a message, so something has to ask.
//
// It does not ask per write. A chatty workload writes thousands of times a
// second, and a message each floods the queue and starves key handling —
// the console stops responding while it renders frames nobody sees. A
// ticker repaints at a fixed rate instead, so render cost is bounded by
// time rather than by how talkative the workload is.
type redrawWriter struct {
	term  *console.Terminal
	dirty *atomic.Bool
}

func (w redrawWriter) Write(p []byte) (int, error) {
	n, err := w.term.Write(p)
	w.dirty.Store(true)
	return n, err
}

// repaint sends a redraw at most once per interval while there is
// something new to show.
func repaint(ctx context.Context, prog *tea.Program, dirty *atomic.Bool) {
	const frame = 33 * time.Millisecond
	t := time.NewTicker(frame)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if dirty.Swap(false) {
				prog.Send(console.RedrawMsg{})
			}
		}
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
