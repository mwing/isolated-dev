package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/clone"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/history"
	"github.com/mwing/isolated-dev/internal/trust"
	"github.com/mwing/isolated-dev/internal/wizard"
)

func newInteractiveCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "interactive",
		Aliases: []string{"i"},
		Short:   "Guided setup: what state this project is in, and what to do next",
		Long: "Reports what is set up and what is not, then offers the next steps\n" +
			"with an explanation of what each one will do.\n\n" +
			"Every entry maps to a command you could have typed, and the menu\n" +
			"shows which one. It is a way into the tool, not a layer on top of\n" +
			"it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWizard(cmd.Context(), env)
		},
	}
}

func runWizard(ctx context.Context, env *Env) error {
	for {
		facts, err := gatherFacts(ctx, env)
		if err != nil {
			return err
		}
		title := fmt.Sprintf("dev — %s", facts.Project)
		model := wizard.New(title, wizard.Assess(facts), wizard.Menu(facts))

		prog := tea.NewProgram(model, tea.WithContext(ctx))
		if _, err := prog.Run(); err != nil {
			// The menu needs a terminal. Saying so, and naming the command
			// that answers the same question without one, beats passing
			// the terminal library's own wording to someone who did not
			// ask for a TTY.
			if strings.Contains(err.Error(), "/dev/tty") {
				return fmt.Errorf("this needs a terminal; `dev doctor` reports the same " +
					"state without one")
			}
			return err
		}
		chosen := model.Chosen()
		if chosen == nil {
			return nil
		}

		if err := performAction(ctx, env, *chosen); err != nil {
			fmt.Fprintf(env.Stderr, "\ndev: %v\n", err)
		}

		// Back to the menu, with the checks recomputed: the point of the
		// list is that it reflects what is true now, and the thing that
		// just ran is usually what changed it.
		fmt.Fprint(env.Stdout, "\nPress enter to return to the menu, or ctrl-c to leave. ")
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			return nil
		}
	}
}

// performAction runs the chosen entry. Commands are dispatched through the
// same command tree the user would type into, so there is one code path
// and no second implementation to drift.
func performAction(ctx context.Context, env *Env, a wizard.Action) error {
	if a.Internal == "dockerignore" {
		return writeDockerignore(env)
	}

	args := append([]string(nil), a.Args...)
	// The one action that needs an answer: what to run.
	if len(args) >= 3 && args[0] == "run" && args[1] == "--command" && args[2] == "" {
		fmt.Fprint(env.Stdout, "Command to run in the sandbox: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		args[2] = line
	}

	fmt.Fprintf(env.Stdout, "\n$ dev %s\n\n", strings.Join(args, " "))
	root := NewRootCmd(env)
	root.SetArgs(args)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	return root.ExecuteContext(ctx)
}

func writeDockerignore(env *Env) error {
	path := filepath.Join(env.Paths.ProjectDir, ".dockerignore")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	_, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	language := ""
	if p.Detected.Found() {
		language = p.Detected.Language.Name
	}
	body := wizard.Dockerignore(language)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Wrote %s:\n\n%s\n", path, body)
	fmt.Fprintln(env.Stdout, "Read it before committing — it is a starting point, "+
		"and only you know what this project needs at build time.")
	return nil
}

// gatherFacts observes the project. Everything that needs a daemon degrades
// to "not checked" rather than to a wrong answer.
func gatherFacts(ctx context.Context, env *Env) (wizard.Facts, error) {
	cfg, p, err := resolveProject(env)
	if err != nil {
		return wizard.Facts{}, err
	}
	f := wizard.Facts{
		Project:    p.Name,
		Network:    string(p.Network),
		Registries: p.Registries(),
	}
	if p.Detected.Found() {
		f.Detected = p.Detected.Explain()
		f.Language = p.Detected.Language.Name
	}

	drv := env.driver(cfg.VMName)
	if st, perr := drv.Probe(ctx); perr == nil && st.DaemonUp {
		f.BackendReady = true
		f.BackendDetail = fmt.Sprintf("%s, VM %q, docker %s",
			st.Backend, st.VMName, st.DaemonVersion)
		eng := container.New(drv)
		if built, ierr := eng.ImageExists(ctx, p.Image); ierr == nil {
			f.ImageBuilt = built
		}
		if sidecar, ierr := eng.ImageExists(ctx, proxyImageTag); ierr == nil {
			f.SidecarImage = sidecar
		}
	} else if perr != nil {
		f.BackendDetail = perr.Error()
	} else {
		f.BackendDetail = firstNonEmpty(st.Detail, "docker daemon not reachable")
	}

	if store, serr := trust.Load(env.Paths.Home, p.Dir); serr == nil {
		f.Grants = store.Project.Hosts("default")
		f.Tools = store.Tools()
		f.PendingGrants = store.Pending("default", cfg.Agents["default"])
		f.PendingSettings = len(store.PendingSettings(projectAsks(cfg, p)))
		if runs, herr := history.Load(history.Path(store.Project.Path())); herr == nil {
			f.Runs = len(runs)
		}
	}

	if _, serr := os.Stat(filepath.Join(p.Dir, ".dockerignore")); serr == nil {
		f.HasDockerignore = true
	} else {
		f.ContextBytes, f.ContextFiles, _ = contextSize(p.Dir)
	}

	if home, herr := os.UserHomeDir(); herr == nil {
		if target, terr := targetFor(detectShell(env.Env), home); terr == nil {
			if _, serr := os.Stat(target.Path); serr == nil {
				f.CompletionsInstalled = true
			}
		}
	}

	if r, rerr := registry(env); rerr == nil {
		for _, a := range r.List() {
			f.Agents = append(f.Agents, a.Name)
		}
	}

	if _, serr := os.Stat(filepath.Join(clone.Dir(env.Paths.Home, projectSlug(p.Dir)), ".git")); serr == nil {
		f.ClonePresent = true
	}
	return f, nil
}
