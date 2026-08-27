// Package history records what each run of a project reached.
//
// The egress summary a run prints is gone the moment the terminal scrolls,
// which is the wrong lifetime for it. The question that matters — "that
// package I ran last week, what did it try to talk to?" — is asked after
// the fact, and a sandbox that cannot answer it is only a restriction, not
// an account of what happened.
//
// It also makes grants reviewable. An allowlist that only ever grows stops
// meaning anything; knowing which grants have actually been used is what
// makes removing the others safe.
//
// Records live under ~/.dev-envs beside the project's grants, never in the
// repository: this is a record of what a machine did, belonging to the
// person at that machine, and a repository that could read it would learn
// about work that has nothing to do with it.
package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Run is one execution, aggregated.
//
// Per-connection detail is deliberately not kept: a build makes thousands
// of requests to one host, and a file that grows with traffic is a file
// that eventually has to be deleted rather than read. Aggregating per run
// answers both questions this exists for — what did this run reach, and
// when was a destination last reached — at a size that stays readable for
// years.
type Run struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Command string    `json:"command,omitempty"`
	Image   string    `json:"image,omitempty"`
	Network string    `json:"network,omitempty"`
	// Allowed and Denied count connections per rendered "host:port", the
	// shape written before destinations were recorded as fields. Read, and
	// no longer written. See Reached.
	Allowed map[string]int `json:"allowed,omitempty"`
	Denied  map[string]int `json:"denied,omitempty"`
	// To and Blocked are the destinations themselves. Read them through
	// Reached and Refused, which also understand the older shape.
	To      []Dest `json:"reached,omitempty"`
	Blocked []Dest `json:"refused,omitempty"`
}

// Dest is one destination, and how a decision was made about it.
//
// Fields rather than a rendered string, because the string was ambiguous
// in two ways that mattered once anything but a human read it. A host may
// contain colons — an IPv6 literal is mostly colons — so "host:port"
// could not be taken apart again. And the marker that distinguished a
// refused name lookup was the suffix " (DNS)" on the host, which is text
// a container can put in a hostname: a proxy-level refusal could be made
// to read as a name lookup, and two different events could collide into
// one count.
type Dest struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// Method is "connect" for a decision made about a connection and
	// "DNS" for one made about a name lookup. It decides what a reader is
	// told about whether the request could have been held for an answer,
	// which is why it must not be something the workload can choose.
	Method string `json:"method"`
	Count  int    `json:"count"`
}

// MethodConnect and MethodDNS are the two ways a destination is decided.
const (
	MethodConnect = "connect"
	MethodDNS     = "DNS"
)

// Reached and Refused are the run's destinations, whichever shape they
// were recorded in.
//
// A record written before this existed carries only the rendered keys, so
// they are taken apart as well as they can be — which for an unbracketed
// IPv6 address with a port is not very well, because that string genuinely
// cannot be parsed. Old records are read best-effort and new ones are not
// guesswork; the alternative was to keep guessing forever.
func (r Run) Reached() []Dest { return decode(r.To, r.Allowed, false) }

// Refused is Reached for the destinations that were blocked.
func (r Run) Refused() []Dest { return decode(r.Blocked, r.Denied, true) }

func decode(recorded []Dest, legacy map[string]int, denied bool) []Dest {
	if len(recorded) > 0 || len(legacy) == 0 {
		// Sorted on the way out rather than on the way in: a record is
		// written in the order destinations were first seen, and a reader
		// wants the busiest first. Copied, so sorting a read does not
		// reorder the caller's record.
		out := append([]Dest(nil), recorded...)
		sortDests(out)
		return out
	}
	out := make([]Dest, 0, len(legacy))
	for key, n := range legacy {
		d := decodeKey(key, denied)
		d.Count = n
		out = append(out, d)
	}
	sortDests(out)
	return out
}

// decodeKey takes a rendered key apart. Only for records written before
// destinations were fields.
func decodeKey(key string, denied bool) Dest {
	// The DNS marker was only ever written for a denial, so a reached
	// destination whose name happens to end this way is a name, not a
	// method.
	if host, ok := strings.CutSuffix(key, " ("+MethodDNS+")"); ok && denied {
		return Dest{Host: host, Method: MethodDNS}
	}
	host, port, err := net.SplitHostPort(key)
	if err != nil {
		// No port, or an address this shape cannot represent — an
		// unbracketed IPv6 literal with a port among them. The whole key
		// is the host, which is at least a string the reader can see is
		// odd rather than a plausible wrong answer.
		return Dest{Host: key, Method: MethodConnect}
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return Dest{Host: key, Method: MethodConnect}
	}
	return Dest{Host: host, Port: n, Method: MethodConnect}
}

// sortDests orders destinations busiest first, then by name, so two
// readings of one run produce the same bytes.
func sortDests(d []Dest) {
	sort.Slice(d, func(i, j int) bool {
		if d[i].Count != d[j].Count {
			return d[i].Count > d[j].Count
		}
		if d[i].Host != d[j].Host {
			return d[i].Host < d[j].Host
		}
		return d[i].Port < d[j].Port
	})
}

// Duration is how long the run took.
func (r Run) Duration() time.Duration { return r.End.Sub(r.Start) }

// Empty reports whether a run reached nothing at all, which is the normal
// case for an offline run and not worth a record.
func (r Run) Empty() bool {
	return len(r.Allowed) == 0 && len(r.Denied) == 0 &&
		len(r.To) == 0 && len(r.Blocked) == 0
}

// maxRuns caps the file. Old runs answer neither question this exists for:
// a destination's last use is by definition recent, and forensics past a
// few hundred runs is archaeology.
const maxRuns = 300

// Path is the history file for a project, beside its grants.
func Path(projectFile string) string {
	base := strings.TrimSuffix(projectFile, filepath.Ext(projectFile))
	return base + ".history.jsonl"
}

// Append records a run, trimming the file when it grows past the cap.
func Append(path string, r Run) error {
	if r.Empty() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}

	f, err := openAppend(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return trim(path)
}

// openAppend opens the record for appending. 0600: the list of hostnames
// a machine contacted is nobody else's business, including other accounts
// on the same machine.
func openAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

// trim rewrites the file with only the most recent runs.
func trim(path string) error {
	runs, err := Load(path)
	if err != nil || len(runs) <= maxRuns {
		return err
	}
	var b strings.Builder
	for _, r := range runs[len(runs)-maxRuns:] {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// Load reads a project's runs, oldest first. A missing file is no runs
// rather than an error: a project that has never been run is the ordinary
// case, not a fault.
func Load(path string) ([]Run, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var runs []Run
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Run
		// A record this version cannot parse is skipped rather than
		// failing the read: a corrupt line must not make the rest of the
		// history unreadable, which is exactly when it is wanted.
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		runs = append(runs, r)
	}
	return runs, sc.Err()
}

// Contact is when a destination was last reached, and how often.
type Contact struct {
	Host  string
	Port  int
	Last  time.Time
	Count int
}

// Contacts summarizes what was reached across runs, most recent first.
func Contacts(runs []Run) []Contact {
	type agg struct {
		last  time.Time
		count int
	}
	type ident struct {
		host string
		port int
	}
	seen := map[ident]*agg{}
	for _, r := range runs {
		for _, d := range r.Reached() {
			id := ident{d.Host, d.Port}
			a := seen[id]
			if a == nil {
				a = &agg{}
				seen[id] = a
			}
			a.count += d.Count
			if r.Start.After(a.last) {
				a.last = r.Start
			}
		}
	}

	out := make([]Contact, 0, len(seen))
	for id, a := range seen {
		out = append(out, Contact{Host: id.host, Port: id.port, Last: a.last, Count: a.count})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Host < out[j].Host
	})
	return out
}

// Key is how a destination is recorded.
func Key(host string, port int) string {
	if port == 0 {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}
