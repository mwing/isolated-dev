package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/agent"
	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/console"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/history"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/runner"
	"github.com/mwing/isolated-dev/internal/trust"
)

func newConsoleCmd(env *Env) *cobra.Command {
	var (
		command    string
		rebuild    bool
		extraHosts []string
		shell      bool
		agentName  string
		record     string
		replay     string
		replaySize string
		useCloneD  bool
		inPlace    bool
		cloneDepth int
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
			if replay != "" {
				return replayRecording(env, replay, replaySize)
			}
			return runConsole(cmd.Context(), env, splitCommand(command, args),
				rebuild, extraHosts, shell, agentName, record,
				cloneOpts{use: useCloneD, inPlace: inPlace, depth: cloneDepth})
		},
	}
	addCloneFlag(cmd, &useCloneD, &cloneDepth)
	cmd.Flags().BoolVar(&inPlace, "in-place", false,
		"run an agent in the working tree instead of a private clone")
	cmd.Flags().StringVarP(&command, "command", "c", "", "command to run")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "rebuild the image first")
	cmd.Flags().StringArrayVar(&extraHosts, "allow-host", nil, "add a destination for this run")
	cmd.Flags().BoolVar(&shell, "shell", false,
		"treat the command as interactive: give it a terminal and the keyboard")
	cmd.Flags().StringVar(&agentName, "agent", "",
		"run this agent in the console, using its stored login")
	cmd.Flags().StringVar(&record, "record", "",
		"record the workload's output and terminal sizes to a file")
	cmd.Flags().StringVar(&replay, "replay", "",
		"render a recording instead of running anything")
	cmd.Flags().StringVar(&replaySize, "replay-size", "",
		"replay at a fixed WxH, ignoring the recorded resizes")
	return cmd
}

// replayRecording renders a recorded session, showing the sizes the
// workload was given alongside the screen it produced. A screenshot cannot
// show the size, and the size is usually the question.
func replayRecording(env *Env, path, size string) error {
	var (
		screen []string
		sizes  []console.Entry
		err    error
	)
	if size != "" {
		var cols, rows int
		if _, serr := fmt.Sscanf(size, "%dx%d", &cols, &rows); serr != nil || cols <= 0 || rows <= 0 {
			return fmt.Errorf("--replay-size wants WxH, e.g. 188x44")
		}
		screen, sizes, err = console.ReplayAt(path, cols, rows)
	} else {
		screen, sizes, err = console.Replay(path)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Terminal sizes given to the workload:\n")
	for _, s := range sizes {
		if s.Type == "note" {
			fmt.Fprintf(env.Stdout, "  %6dms  %s\n", s.Millis, s.Note)
			continue
		}
		fmt.Fprintf(env.Stdout, "  %6dms  %dx%d  %s\n", s.Millis, s.Cols, s.Rows, s.Note)
	}
	fmt.Fprintf(env.Stdout, "\nFinal screen (%d rows):\n", len(screen))
	for i, line := range screen {
		fmt.Fprintf(env.Stdout, "%3d |%s\n", i, line)
	}
	return nil
}

func runConsole(ctx context.Context, env *Env, command []string, rebuild bool,
	extraHosts []string, interactive bool, agentName string, record string,
	cl cloneOpts) error {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	store, err := trust.Load(env.Paths.Home, p.Dir)
	if err != nil {
		return err
	}
	if err := enforceConsent(env, cfg, p, store); err != nil {
		return err
	}
	if p.Network != project.NetworkAllowlist {
		return fmt.Errorf("console needs `network: allowlist`; this project is %q, "+
			"and with nothing filtered there is nothing to decide", p.Network)
	}
	pol, err := loadPolicy(env)
	if err != nil {
		return err
	}
	// Refused before anything is built or started: the console is a view
	// over the same run, so --allow-host means here what it means there.
	if err := pol.CheckHosts(extraHosts); err != nil {
		return err
	}
	if len(command) == 0 && agentName == "" {
		command = []string{"/bin/bash"}
		interactive = true
	}

	// An agent driven from the console gets a private clone by default,
	// exactly as `dev agent run` does, and for the same reason: what makes
	// the clone right is who is driving — a model rather than the person in
	// the room — not which view they are watching through. The console
	// documents itself as a view over the same run, and a view that quietly
	// ran agents against the working tree was the weaker way to start one.
	//
	// A console with no agent is a person running their own command, which
	// is the case the plain mount is right for. There --clone is opt-in,
	// matching `dev run` and `dev shell`.
	var workspaceDir string
	if consoleWantsClone(cfg, agentName, cl) {
		// To stderr, and before the program takes the screen: the clone's
		// account of itself is several lines, and the full-screen view owns
		// stdout from here on.
		workspaceDir, err = prepareCloneDir(ctx, env, p.Dir, cl.depth, env.Stderr)
		if err != nil {
			return err
		}
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
		grants    project.Grants
	)
	if agentName != "" {
		agentOpts, image, allowed, err = prepareAgent(ctx, env, eng, p, store, cfg, agentName, command)
		if err != nil {
			return err
		}
		if workspaceDir != "" {
			agentOpts.Workspace = workspaceDir
		}
		// An agent gets a terminal whether or not a command was given: its
		// whole interface is interactive.
		interactive = true
	} else {
		image, err = ensureTools(ctx, env, eng, p, store, cfg)
		if err != nil {
			return err
		}
		allowed = workspaceAllowlist(cfg, p, store)
		// The console is a view over the same run, so it honors the same
		// grants. An agent deliberately gets none of them: it runs at the
		// untrusted level whatever the project's own trust.
		grants, err = resolveGrants(ctx, env, eng, cfg, store, image)
		if err != nil {
			return err
		}
	}
	allowed = permittedHosts(env, pol, append(allowed, extraHosts...))

	side, topo, err := startSidecar(ctx, env, eng, p, allowed, EgressAsk)
	if err != nil {
		return err
	}

	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()

	// Cancelling kills the docker CLIENT, not the container: `docker run`
	// attaches, it does not own. Without an explicit removal, leaving the
	// console leaves the workload running and the next run collides on the
	// name. Named for exactly that reason.
	workloadName := "dev-" + p.Name + "-console"
	// A previous console killed outright leaves its container behind, and
	// the next run would fail on the name rather than explaining itself.
	_ = eng.Remove(ctx, workloadName)
	defer func() {
		_ = eng.Remove(context.WithoutCancel(ctx), workloadName)
	}()

	var term *console.Terminal
	if interactive {
		// Start at the real pane size rather than a default. A workload
		// that draws its own interface at 80x24 inside a much larger
		// window looks like a frozen console, not a mis-sized one.
		cols, rows := paneSize()
		term = console.NewTerminal(cols, rows)
		if record != "" {
			rec, err := console.NewRecorder(record)
			if err != nil {
				return err
			}
			defer func() { _ = rec.Close() }()
			term.Rec = rec
			rec.Resize(cols, rows, "startup estimate")
			fmt.Fprintf(env.Stderr, "recording to %s\n", record)
		}
	}

	model := console.New(
		fmt.Sprintf("dev console — %s", p.Name),
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
				// The dialog is a route in like the CLI prompt: it widens
				// egress mid-run and can persist what it widens. A denied
				// destination is refused outright rather than held.
				if verr := pol.CheckHost(host); verr != nil {
					if err := side.Grant(runCtx, "refuse", host); err != nil {
						return err
					}
					return verr
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

	rec := runRecord{
		Path:    history.Path(store.Project.Path()),
		Start:   time.Now(),
		Command: command,
		Image:   image,
		Network: string(p.Network),
	}
	prog := tea.NewProgram(model, tea.WithContext(ctx))

	go streamEvents(runCtx, eng, topo, prog)
	if term != nil && agentOpts != nil {
		go runAgentInteractive(runCtx, eng, *agentOpts, topo, prog, term, workloadName)
	} else if term != nil {
		go runInteractive(runCtx, eng, p, cfg, grants, command, topo, prog, image, term, workloadName, workspaceDir)
	} else {
		go runWorkload(runCtx, eng, p, cfg, grants, command, topo, prog, image, workspaceDir)
	}

	if _, err := prog.Run(); err != nil {
		// A console that failed to start still ran a workload, so the
		// record is written on this path too.
		_, _ = finishRun(ctx, env, side, rec)
		if strings.Contains(err.Error(), "/dev/tty") {
			return fmt.Errorf("the console needs a terminal and this session has none; " +
				"use `dev run --egress-prompt ask` for the same blocking prompts " +
				"without the full-screen view")
		}
		return err
	}

	summary, stopErr := finishRun(ctx, env, side, rec)
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
		return &exitStatus{Code: code}
	}
	return nil
}

// streamEvents forwards sidecar decisions into the UI.
func streamEvents(ctx context.Context, eng *container.Engine,
	topo netpolicy.Topology, prog *tea.Program) {
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
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

// paneSize estimates the workload pane from the terminal, for the first
// frame. The exact size arrives with the window-size message a moment
// later and resizes both the emulator and the workload's own terminal.
func paneSize() (cols, rows int) {
	cols, rows = 80, 24
	if ws, err := pty.GetsizeFull(os.Stdout); err == nil && ws.Cols > 0 {
		cols = int(ws.Cols)
		// Header, separator, event pane and footer come off the top and
		// bottom; the same arithmetic the model uses.
		rows = int(ws.Rows) - 12
		if rows < 5 {
			rows = 5
		}
	}
	return cols, rows
}

// prepareAgent builds the agent's image, volume and run options, reusing
// exactly what `dev agent run` uses — the console is a view, not a second
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

	// The project's request counts here exactly as it does for `dev agent
	// run`. Resolving it differently would mean the same agent ran on a
	// different image depending on which command started it.
	request := projectRequest(cfg, a.Name)
	if pending := store.Pending(a.Name, request); len(pending) > 0 {
		return nil, "", nil, fmt.Errorf(
			"%s requests egress you have not accepted: %s\nReview with: dev accept%s",
			env.Paths.Project, strings.Join(pending, " "), agentSuffix(a.Name))
	}

	saved := store.Resolve(a.Name)
	base := saved.Base
	if base == "" {
		base = request.Base
	}
	if base == "" {
		// The project's own environment, so the agent has the toolchain of
		// the thing it is working on. See agentBaseImage.
		base, err = agentBaseImage(ctx, env, eng, cfg, p, store)
		if err != nil {
			return nil, "", nil, err
		}
	}
	opts := agent.Options{
		Agent:       a,
		Project:     p.Dir,
		Interactive: true,
		Args:        command,
		Image:       base,
		Memory:      firstSet(saved.Memory, request.Memory),
		CPUs:        firstSet(saved.CPUs, request.CPUs),
		// Without this the agent cannot commit — the container has no
		// ~/.gitconfig, so git refuses with "please tell me who you are",
		// and in a clone that means the work has no way back out. `dev agent
		// run` has always set it; this path did not, so the same agent could
		// commit or not depending on which command started it.
		GitIdentity: gitIdentity(env),
	}

	runner := &agent.Runner{Engine: eng, Out: env.Stdout}
	image, err := runner.EnsureImage(ctx, opts, false)
	if err != nil {
		return nil, "", nil, err
	}
	if err := runner.EnsureVolume(ctx, a); err != nil {
		return nil, "", nil, err
	}

	allowed := agentEgress(p, opts.Allowlist(), saved.AllowHosts,
		store.AcceptedRequest(a.Name, request))
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
	topo netpolicy.Topology, prog *tea.Program, term *console.Terminal, name string) {
	spec := agent.Spec(opts, topo)
	spec.Name = name
	spec.TTY = true
	spec.Env = append(spec.Env, "TERM=xterm-256color")
	spec.Ports = nil

	var dirty atomic.Bool
	go repaint(ctx, prog, &dirty)

	sink, drain := term.Feed()
	cols, rows := term.Size()
	res, err := eng.RunPTY(ctx, spec, redrawWriter{sink: sink, dirty: &dirty}, &runner.PTY{
		Rows: uint16(rows), Cols: uint16(cols), Ready: term.Attach,
	})
	drain()
	prog.Send(console.DoneMsg{Err: err, ExitCode: res.ExitCode})
}

// runInteractive runs the workload on a pseudo-terminal and feeds its
// screen into the console's emulator, so a shell behaves like a shell
// while the console keeps the surrounding layout.
func runInteractive(ctx context.Context, eng *container.Engine, p *project.Project,
	cfg config.Config, grants project.Grants, command []string, topo netpolicy.Topology,
	prog *tea.Program, image string, term *console.Terminal, name, workspaceDir string) {
	spec := p.RunSpec(cfg, grants, command, true)
	spec.Name = name
	spec.Image = image
	mountWorkspace(&spec, workspaceDir)
	spec.Network = topo.InternalNetwork
	spec.DNS = []string{topo.SidecarIP}
	spec.Env = append(spec.Env, topo.Env()...)
	spec.Env = append(spec.Env, "TERM=xterm-256color")
	spec.Ports = nil

	var dirty atomic.Bool
	go repaint(ctx, prog, &dirty)

	sink, drain := term.Feed()
	cols, rows := term.Size()
	res, err := eng.RunPTY(ctx, spec, redrawWriter{sink: sink, dirty: &dirty}, &runner.PTY{
		Rows: uint16(rows), Cols: uint16(cols),
		Ready: term.Attach,
	})
	drain()
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
	sink  io.Writer
	dirty *atomic.Bool
}

func (w redrawWriter) Write(p []byte) (int, error) {
	n, err := w.sink.Write(p)
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
	cfg config.Config, grants project.Grants, command []string, topo netpolicy.Topology,
	prog *tea.Program, image, workspaceDir string) {
	spec := p.RunSpec(cfg, grants, command, false)
	spec.Image = image
	mountWorkspace(&spec, workspaceDir)
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
	_ = pw.Close()
	prog.Send(console.DoneMsg{Err: err, ExitCode: res.ExitCode})
}
