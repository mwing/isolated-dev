package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/history"
	"github.com/mwing/isolated-dev/internal/netpolicy"
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
	allowed, denied := netpolicy.ParseEvents(logs)

	if rec.Path != "" {
		r := history.Run{
			Start:   rec.Start,
			End:     time.Now(),
			Command: strings.Join(rec.Command, " "),
			Image:   rec.Image,
			Network: rec.Network,
			Allowed: allowed,
			Denied:  denied,
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
	Port int    `json:"port,omitempty"`
	// Method is "DNS" for a name refused before any request existed, and
	// "connect" for one refused at the proxy. The difference decides what
	// the user is told: only the second could have been held for an
	// answer.
	Method string `json:"method"`
	Count  int    `json:"count"`
}

// splitKey turns a stored count key back into its parts.
func splitKey(key string) destJSON {
	if host, ok := strings.CutSuffix(key, " (DNS)"); ok {
		return destJSON{Host: host, Method: "DNS"}
	}
	host, portText, ok := strings.Cut(key, ":")
	if !ok {
		return destJSON{Host: key, Method: "connect"}
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return destJSON{Host: key, Method: "connect"}
	}
	return destJSON{Host: host, Port: port, Method: "connect"}
}

// destinations renders a count map in a stable order: busiest first, then
// by name, so two runs of the same thing produce the same bytes.
func destinations(m map[string]int) []destJSON {
	out := make([]destJSON, 0, len(m))
	for _, key := range topKeys(m, len(m)) {
		d := splitKey(key)
		d.Count = m[key]
		out = append(out, d)
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
			if len(r.Denied) > 0 {
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
				Allowed: destinations(r.Allowed),
				Denied:  destinations(r.Denied),
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
		if len(r.Allowed) > 0 {
			fmt.Fprintf(env.Stdout, "    reached: %s\n", strings.Join(topKeys(r.Allowed, 6), " "))
		}
		for _, line := range netpolicy.Summary(r.Denied) {
			fmt.Fprintf(env.Stdout, "    ✗ %s\n", line)
		}
	}
	return nil
}

// topKeys returns the busiest destinations, so a long tail of one-request
// hosts does not bury the ones a run actually depended on.
func topKeys(m map[string]int, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		return append(keys[:n:n], fmt.Sprintf("(+%d more)", len(keys)-n))
	}
	return keys
}

func newHistoryHostsCmd(env *Env) *cobra.Command {
	return &cobra.Command{
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
			if len(contacts) == 0 {
				fmt.Fprintln(env.Stdout, "Nothing recorded for this project yet.")
				return nil
			}
			for _, c := range contacts {
				fmt.Fprintf(env.Stdout, "  %-45s  %s  (%d)\n",
					history.Key(c.Host, c.Port), ago(c.Last), c.Count)
			}
			return nil
		},
	}
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
