// Package wizard is the guided front door: what state is this project in,
// and what is worth doing next.
//
// v1 had an interactive menu of the tool's commands. A menu of commands
// helps someone who already knows the tool and does nothing for the person
// this is actually for — someone with a repository, no idea what a sandbox
// will do to it, and no reason to trust that running it is safe.
//
// So this reports a checklist first and offers actions second, and every
// action says what it will do before it does it. The list is derived from
// the project rather than fixed, because "what should I do next" has a
// different answer on the first run than on the fiftieth.
package wizard

import (
	"fmt"
	"sort"
	"strings"
)

// State is how a check turned out.
type State int

const (
	// OK is settled: nothing to do.
	OK State = iota
	// Todo is not done but not wrong — the ordinary state of a project
	// that has not been set up yet.
	Todo
	// Broken stops a run from working at all.
	Broken
)

func (s State) mark() string {
	switch s {
	case OK:
		return "✓"
	case Broken:
		return "✗"
	default:
		return "•"
	}
}

// Check is one line of the situation report.
type Check struct {
	State State
	Label string
	// Detail is the part that changes: a version, a size, a count.
	Detail string
}

func (c Check) String() string {
	if c.Detail == "" {
		return fmt.Sprintf("%s %s", c.State.mark(), c.Label)
	}
	return fmt.Sprintf("%s %s — %s", c.State.mark(), c.Label, c.Detail)
}

// Action is something the menu can do. Args are the command line it maps
// to, so every action is a thing the user could have typed: the wizard
// teaches the tool rather than replacing it.
type Action struct {
	Label   string
	Args    []string
	Explain string
	// Internal marks an action this package performs itself rather than
	// dispatching to a command, e.g. writing a .dockerignore.
	Internal string
}

// Command renders the equivalent command line, for showing the user what
// the menu entry is about to run.
func (a Action) Command() string {
	if len(a.Args) == 0 {
		return ""
	}
	// An empty argument is a slot the wizard fills by asking. Printed as
	// itself it produced "dev run --command " — a flag with nothing after
	// it, which reads as a command that would fail if you typed it.
	parts := make([]string, 0, len(a.Args))
	for _, arg := range a.Args {
		if arg == "" {
			arg = "…"
		}
		parts = append(parts, arg)
	}
	return "dev " + strings.Join(parts, " ")
}

// Facts are what the caller observed. Gathering them needs a daemon, a
// filesystem and a config; deciding what they mean does not, which is why
// they are separated.
type Facts struct {
	Project  string
	Detected string
	Language string
	// BackendReady is false when the VM or daemon is not usable, which
	// makes most other checks unanswerable rather than false.
	BackendReady         bool
	BackendDetail        string
	SidecarImage         bool
	ImageBuilt           bool
	Network              string
	Registries           []string
	Grants               []string
	PendingGrants        []string
	PendingSettings      int
	Tools                []string
	HasDockerignore      bool
	ContextBytes         int64
	ContextFiles         int
	CompletionsInstalled bool
	Agents               []string
	Runs                 int
	ClonePresent         bool
}

// Assess turns the facts into the situation report.
func Assess(f Facts) []Check {
	var out []Check

	if f.Detected != "" {
		out = append(out, Check{OK, "language detected", f.Detected})
	} else {
		out = append(out, Check{Broken, "no language detected",
			"add a Dockerfile, or `dev new <language>` in an empty directory"})
	}

	if f.BackendReady {
		out = append(out, Check{OK, "container backend", f.BackendDetail})
	} else {
		out = append(out, Check{Broken, "container backend", f.BackendDetail})
	}

	if !f.BackendReady {
		// Saying "image not built" when the daemon cannot be reached is a
		// guess presented as a finding.
		out = append(out, Check{Todo, "image and sidecar", "not checked: backend unavailable"})
	} else {
		if f.SidecarImage {
			out = append(out, Check{OK, "egress sidecar image", ""})
		} else {
			out = append(out, Check{Broken, "egress sidecar image",
				"built automatically on the next run"})
		}
		if f.ImageBuilt {
			out = append(out, Check{OK, "project image", "built"})
		} else {
			out = append(out, Check{Todo, "project image", "not built yet"})
		}
	}

	net := f.Network
	if len(f.Registries) > 0 {
		net += ": " + strings.Join(f.Registries, " ")
	}
	out = append(out, Check{OK, "network", net})

	if n := len(f.PendingGrants) + f.PendingSettings; n > 0 {
		out = append(out, Check{Todo, "this project requests things you have not accepted",
			fmt.Sprintf("%d item(s)", n)})
	}

	// A build context nobody has looked at is the difference between a
	// two-second rebuild and a two-minute one, and it is invisible until
	// someone reads a build log.
	switch {
	case f.HasDockerignore:
		out = append(out, Check{OK, ".dockerignore", "present"})
	case f.ContextBytes > 100<<20:
		out = append(out, Check{Todo, "no .dockerignore",
			fmt.Sprintf("every build sends %s (%d files) to the daemon",
				humanBytes(f.ContextBytes), f.ContextFiles)})
	default:
		out = append(out, Check{Todo, "no .dockerignore", "builds send the whole directory"})
	}

	if f.CompletionsInstalled {
		out = append(out, Check{OK, "shell completions", "installed"})
	} else {
		out = append(out, Check{Todo, "shell completions", "not installed"})
	}

	return out
}

// Menu is the ordered list of things worth doing, most useful first.
//
// Ordering is the whole value here. Everything this offers is available as
// a command already; what a newcomer lacks is knowing which one comes
// next, and what it will do to their files.
func Menu(f Facts) []Action {
	var out []Action

	if n := len(f.PendingGrants) + f.PendingSettings; n > 0 {
		out = append(out, Action{
			Label: fmt.Sprintf("Review what this project requests (%d)", n),
			Args:  []string{"accept"},
			Explain: "The .devenv.yaml in this repository asks for things — extra egress, " +
				"tools, a network mode. Asking is not getting: nothing is granted until " +
				"you accept it here, and the decision is recorded outside the repository.",
		})
	}

	if f.BackendReady && !f.ImageBuilt {
		out = append(out, Action{
			Label: "Build this project's image",
			Args:  []string{"build"},
			Explain: "Renders the language template (or your Dockerfile) and builds it. " +
				"Nothing runs yet — this only produces the image a run would use.",
		})
	}

	out = append(out, Action{
		Label: "Open a shell in the sandbox",
		Args:  []string{"shell"},
		Explain: "A shell in the container, with this directory mounted at /workspace. " +
			"No SSH keys, no host environment, no docker socket, and egress limited " +
			"to the language's package registries plus what you have granted.",
	})

	out = append(out, Action{
		Label: "Run the project's tests or a command",
		Args:  []string{"run", "--command", ""},
		Explain: "Runs one command in the sandbox and exits. You will be asked what " +
			"to run.",
	})

	for _, a := range f.Agents {
		out = append(out, Action{
			Label: "Run the " + a + " agent, in a private clone",
			Args:  []string{"agent", "run", a, "--clone"},
			Explain: "The agent works in a copy of this repository, so an unattended " +
				"session cannot damage what you are editing. Its commits come back " +
				"through git when you want them.",
		})
	}

	if !f.HasDockerignore {
		out = append(out, Action{
			Label:    "Write a .dockerignore for this project",
			Internal: "dockerignore",
			Explain: "Docker sends the whole directory to the daemon before every build. " +
				"Excluding history, dependencies and build output usually turns minutes " +
				"into seconds, and costs nothing: your files are mounted at run time, " +
				"not baked into the image.",
		})
	}

	if f.BackendReady && f.ImageBuilt {
		out = append(out, Action{
			Label:   "Scan the image for known vulnerabilities",
			Args:    []string{"scan"},
			Explain: "Reports findings that have a fix available, in the image you actually run.",
		})
		out = append(out, Action{
			Label: "Update base image and packages",
			Args:  []string{"update"},
			Explain: "Moves the base image and its packages to current, and reports what " +
				"moved. This is the deliberate half of pinning.",
		})
	}

	if f.Runs > 0 {
		out = append(out, Action{
			Label:   fmt.Sprintf("Look at what past runs reached (%d recorded)", f.Runs),
			Args:    []string{"history"},
			Explain: "What each run connected to, and what was blocked.",
		})
	}

	if !f.CompletionsInstalled {
		out = append(out, Action{
			Label:   "Install shell completions",
			Args:    []string{"completion", "install"},
			Explain: "Writes the completion script where your shell will find it.",
		})
	}

	out = append(out, Action{
		Label:   "Check the setup",
		Args:    []string{"doctor"},
		Explain: "Reports the backend, the sidecar, disk usage and this project's build context. Changes nothing.",
	})

	return out
}

// Dockerignore is a starting point for a project, built from what the
// language leaves lying around plus what every repository has.
//
// It is deliberately conservative: excluding too little costs build time,
// excluding too much breaks a build in a way that is hard to attribute.
func ignoreEntries(language string) []string {
	common := []string{".git", ".DS_Store", "*.log", "tmp", ".cache"}
	byLang := map[string][]string{
		"node":   {"node_modules", "**/node_modules", "dist", "build", ".next", "coverage"},
		"python": {"__pycache__", "**/__pycache__", ".venv", "venv", "*.pyc", ".pytest_cache", "dist", "build"},
		"golang": {"bin", "vendor"},
		"rust":   {"target"},
		"java":   {"target", "build", ".gradle"},
		"kotlin": {"target", "build", ".gradle"},
		"php":    {"vendor"},
	}

	lines := append([]string(nil), common...)
	lines = append(lines, byLang[language]...)
	sort.Strings(lines)
	return lines
}

func Dockerignore(language string) string {
	lines := ignoreEntries(language)

	var b strings.Builder
	b.WriteString("# Written by `dev interactive`. Everything here is excluded from the\n")
	b.WriteString("# build context, not from the container: your working tree is mounted\n")
	b.WriteString("# at /workspace when a run starts, so these files are still there.\n")
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

func humanBytes(n int64) string {
	switch {
	case n <= 0:
		return "?"
	case n < 1<<20:
		return fmt.Sprintf("%dK", n>>10)
	case n < 1<<30:
		return fmt.Sprintf("%dM", n>>20)
	default:
		return fmt.Sprintf("%.1fG", float64(n)/float64(1<<30))
	}
}
