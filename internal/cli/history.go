package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/history"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/textsafe"
	"github.com/mwing/isolated-dev/internal/trust"
)

// runRecord is what a teardown knows about the run it is ending.
type runRecord struct {
	Path    string
	Start   time.Time
	Command []string
	Image   string
	Network string
}

// finishRun stops the sidecar, records what the run reached, and returns
// the blocked destinations.
//
// Recording happens here rather than in each caller because it must happen
// on every exit path, including the ones that failed: a run that was
// killed is exactly the run someone will want to look up later.
func finishRun(ctx context.Context, env *Env, side *netpolicy.Sidecar, rec runRecord) ([]string, error) {
	logs, err := side.StopAndLog(context.WithoutCancel(ctx))
	if err != nil {
		return nil, err
	}
	_, denied := netpolicy.ParseEvents(logs)

	if rec.Path != "" {
		r := history.Run{
			Start:   rec.Start,
			End:     time.Now(),
			Command: strings.Join(rec.Command, " "),
			Image:   rec.Image,
			Network: rec.Network,
		}
		// Recorded as destinations rather than as rendered keys. The old
		// shape is still read (see history.Run), and no longer written:
		// what it could not represent was an address with colons in it,
		// and what it let the workload choose was how its own denial was
		// described.
		for _, d := range netpolicy.Destinations(logs) {
			dest := history.Dest{Host: d.Host, Port: d.Port, Method: d.Method, Count: d.Count}
			if d.Denied {
				r.Blocked = append(r.Blocked, dest)
				continue
			}
			r.To = append(r.To, dest)
		}
		// A failure to record is worth saying but never worth failing a
		// run over: the work is already done, and losing the record is
		// not a reason to also lose the exit code.
		if err := history.Append(rec.Path, r); err != nil {
			fmt.Fprintf(env.Stderr, "warning: recording run history: %v\n", err)
		}
	}
	return netpolicy.Summary(denied), nil
}

func newHistoryCmd(env *Env) *cobra.Command {
	var limit int
	var deniedOnly bool
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "history",
		Short: "What this project's runs reached, and what was blocked",
		Long: "A run's egress summary scrolls away with the terminal, which is\n" +
			"the wrong lifetime for it: the question worth asking — what did\n" +
			"that thing I ran last week try to talk to? — comes later.\n\n" +
			"Records are kept under ~/.dev-envs beside the project's grants,\n" +
			"never in the repository. It is a record of what this machine did.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return showHistory(env, limit, deniedOnly, asJSON)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "how many runs to show")
	cmd.Flags().BoolVar(&deniedOnly, "denied", false, "only runs that had something blocked")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	cmd.AddCommand(newHistoryHostsCmd(env))
	return cmd
}

func projectHistoryPath(env *Env) (string, *trust.Store, error) {
	store, err := trust.Load(env.Paths.Home, env.Paths.ProjectDir)
	if err != nil {
		return "", nil, err
	}
	return history.Path(store.Project.Path()), store, nil
}

// The JSON shapes. Separate from history.Run because the stored record
// keys its counts by a rendered string — "host:port", or "host (DNS)" —
// which is right for a file that has to stay readable by an older build
// and wrong for something a program will act on. Decomposed here rather
// than by changing the record, because the records on disk predate this.
type historyJSON struct {
	Project string    `json:"project"`
	Runs    []runJSON `json:"runs"`
}

type runJSON struct {
	Start   time.Time  `json:"start"`
	End     time.Time  `json:"end"`
	Command string     `json:"command,omitempty"`
	Image   string     `json:"image,omitempty"`
	Network string     `json:"network,omitempty"`
	Allowed []destJSON `json:"allowed"`
	Denied  []destJSON `json:"denied"`
}

// destJSON is one destination. Host is exactly what `dev allow` takes,
// which is the point of this output: the caller relays a decision to a
// person rather than parsing prose to find out what to relay.
type destJSON struct {
	Host string `json:"host"`
	// No omitempty: a destination with no port, a name lookup and a value
	// that could not be parsed would otherwise be indistinguishable to a
	// program, which is the audience.
	Port int `json:"port"`
	// Method is "DNS" for a name refused before any request existed, and
	// "connect" for one refused at the proxy. The difference decides what
	// the user is told: only the second could have been held for an
	// answer.
	Method string `json:"method"`
	Count  int    `json:"count"`
}

// destinations renders recorded destinations for output.
//
// The host is sanitized on the way out for the reason internal/textsafe
// exists: it was chosen by the workload, it is about to be read by a
// person deciding whether to allow it, and a name that renders as one
// thing and grants another is the whole point of that package. A name
// carrying anything a terminal acts on will not match a real host, which
// is the correct outcome — it is not a host anyone should allow.
func destinations(in []history.Dest) []destJSON {
	out := make([]destJSON, 0, len(in))
	for _, d := range in {
		out = append(out, destJSON{
			Host:   textsafe.Sanitize(d.Host),
			Port:   d.Port,
			Method: d.Method,
			Count:  d.Count,
		})
	}
	return out
}

func showHistory(env *Env, limit int, deniedOnly, asJSON bool) error {
	path, _, err := projectHistoryPath(env)
	if err != nil {
		return err
	}
	runs, err := history.Load(path)
	if err != nil {
		return err
	}
	if deniedOnly {
		kept := runs[:0]
		for _, r := range runs {
			if len(r.Refused()) > 0 {
				kept = append(kept, r)
			}
		}
		runs = kept
	}
	if len(runs) == 0 && !asJSON {
		fmt.Fprintln(env.Stdout, "No runs recorded for this project yet.")
		fmt.Fprintln(env.Stdout, "Only filtered runs are recorded: `--network open` "+
			"has no proxy to record from.")
		return nil
	}

	if limit > 0 && len(runs) > limit {
		runs = runs[len(runs)-limit:]
	}

	if asJSON {
		// An empty history is an empty list, not a sentence. A caller
		// parsing this should never have to tell "nothing happened" apart
		// from "the output changed".
		out := historyJSON{Project: env.Paths.ProjectDir, Runs: []runJSON{}}
		for _, r := range runs {
			out.Runs = append(out.Runs, runJSON{
				Start: r.Start, End: r.End, Command: r.Command,
				Image: r.Image, Network: r.Network,
				Allowed: destinations(r.Reached()),
				Denied:  destinations(r.Refused()),
			})
		}
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	// Most recent last, so the newest is next to the prompt.
	for _, r := range runs {
		cmd := r.Command
		if cmd == "" {
			cmd = "(shell)"
		}
		// A -c one-liner can be a whole script. The list is for finding
		// the run you mean, not for reading it back.
		if len(cmd) > 72 {
			cmd = cmd[:69] + "..."
		}
		fmt.Fprintf(env.Stdout, "%s  %s  (%s)\n",
			r.Start.Local().Format("2006-01-02 15:04"), cmd, r.Duration().Round(time.Second))
		if reached := r.Reached(); len(reached) > 0 {
			fmt.Fprintf(env.Stdout, "    reached: %s\n", strings.Join(names(reached, 6), " "))
		}
		for _, line := range refusalLines(r.Refused()) {
			fmt.Fprintf(env.Stdout, "    ✗ %s\n", line)
		}
	}
	return nil
}

// names renders the busiest destinations, so a long tail of one-request
// hosts does not bury the ones a run actually depended on. Sanitized for
// the same reason the JSON is: the names came from the workload.
func names(dests []history.Dest, n int) []string {
	out := make([]string, 0, len(dests))
	for _, d := range dests {
		out = append(out, textsafe.Sanitize(history.Key(d.Host, d.Port)))
	}
	if len(out) > n {
		return append(out[:n:n], fmt.Sprintf("(+%d more)", len(out)-n))
	}
	return out
}

// refusalLines renders blocked destinations the way a run's own summary
// does, so the same event reads the same whether it is seen live or looked
// up later.
func refusalLines(dests []history.Dest) []string {
	out := make([]string, 0, len(dests))
	for _, d := range dests {
		name := textsafe.Sanitize(history.Key(d.Host, d.Port))
		if d.Method == history.MethodDNS {
			name += " (DNS)"
		}
		if d.Count != 1 {
			out = append(out, fmt.Sprintf("blocked: %s x%d", name, d.Count))
			continue
		}
		out = append(out, "blocked: "+name)
	}
	return out
}

func newHistoryHostsCmd(env *Env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "Every destination this project has reached, most recent first",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path, _, err := projectHistoryPath(env)
			if err != nil {
				return err
			}
			runs, err := history.Load(path)
			if err != nil {
				return err
			}
			contacts := history.Contacts(runs)
			if asJSON {
				out := make([]contactJSON, 0, len(contacts))
				for _, c := range contacts {
					out = append(out, contactJSON{
						Host: textsafe.Sanitize(c.Host), Port: c.Port,
						Last: c.Last, Count: c.Count,
					})
				}
				enc := json.NewEncoder(env.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(hostsJSON{Project: env.Paths.ProjectDir, Hosts: out})
			}
			if len(contacts) == 0 {
				fmt.Fprintln(env.Stdout, "Nothing recorded for this project yet.")
				return nil
			}
			for _, c := range contacts {
				fmt.Fprintf(env.Stdout, "  %-45s  %s  (%d)\n",
					textsafe.Sanitize(history.Key(c.Host, c.Port)), ago(c.Last), c.Count)
			}
			return nil
		},
	}
	// The same flag as the parent, because a caller told "use --json" and
	// finding it on only one of two adjacent commands would reasonably
	// conclude the output was broken.
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

type hostsJSON struct {
	Project string        `json:"project"`
	Hosts   []contactJSON `json:"hosts"`
}

type contactJSON struct {
	Host  string    `json:"host"`
	Port  int       `json:"port"`
	Last  time.Time `json:"last"`
	Count int       `json:"count"`
}

// ago is a coarse relative time. The exact minute a host was last reached
// is never the question; whether it was this week or never is.
func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Local().Format("2006-01-02")
	}
}
