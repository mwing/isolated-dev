package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mwing/isolated-dev/internal/history"
	"github.com/mwing/isolated-dev/internal/trust"
)

// record puts one run in this project's history.
func record(t *testing.T, h *harness, r history.Run) {
	t.Helper()
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := history.Append(history.Path(store.Project.Path()), r); err != nil {
		t.Fatal(err)
	}
}

// An agent told to relay blocked destinations to the user should not have
// to read prose to find out what to relay. The human output is a rendered
// line — "✗ blocked: telemetry.example.com (DNS) x4" — and scraping it is
// a guess about formatting that will change.
func TestHistoryJSONNamesWhatToAllow(t *testing.T) {
	h := newHarness(t)
	record(t, h, history.Run{
		Start:   time.Now().Add(-time.Minute),
		End:     time.Now(),
		Command: "npm install",
		Network: "allowlist",
		To: []history.Dest{{Host: "registry.npmjs.org", Port: 443,
			Method: history.MethodConnect, Count: 3}},
		Blocked: []history.Dest{
			{Host: "telemetry.example.com", Method: history.MethodDNS, Count: 4},
			{Host: "metrics.example.com", Port: 443, Method: history.MethodConnect, Count: 1},
		},
	})

	if err := h.run(t, "history", "--denied", "--json"); err != nil {
		t.Fatalf("history --json: %v\n%s", err, h.stderr.String())
	}

	var got historyJSON
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, h.stdout.String())
	}
	if len(got.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(got.Runs))
	}

	byHost := map[string]destJSON{}
	for _, d := range got.Runs[0].Denied {
		byHost[d.Host] = d
	}
	// The host is exactly what `dev allow` takes: no port, no "(DNS)"
	// suffix, nothing to strip.
	dns, ok := byHost["telemetry.example.com"]
	if !ok {
		t.Fatalf("the DNS denial is not addressable by host: %+v", got.Runs[0].Denied)
	}
	if dns.Method != "DNS" || dns.Count != 4 || dns.Port != 0 {
		t.Errorf("DNS denial = %+v", dns)
	}
	// And the distinction survives, because it decides what the user is
	// told: only a denial at the proxy could have been held for an answer.
	proxied, ok := byHost["metrics.example.com"]
	if !ok {
		t.Fatalf("the proxy denial is missing: %+v", got.Runs[0].Denied)
	}
	if proxied.Method != "connect" || proxied.Port != 443 {
		t.Errorf("proxy denial = %+v", proxied)
	}
}

// An empty history is an empty list, not a sentence. A caller parsing this
// must never have to tell "nothing happened" apart from "the output
// changed".
func TestHistoryJSONIsStillJSONWithNoRuns(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "history", "--json"); err != nil {
		t.Fatal(err)
	}
	var got historyJSON
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, h.stdout.String())
	}
	if len(got.Runs) != 0 {
		t.Errorf("runs = %+v, want none", got.Runs)
	}
	if strings.Contains(h.stdout.String(), "No runs recorded") {
		t.Error("the prose fallback was printed into the JSON output")
	}
}

// The human output is unchanged: --json is an addition, not a migration.
func TestHistoryStillReadsAsProseWithoutTheFlag(t *testing.T) {
	h := newHarness(t)
	record(t, h, history.Run{
		Start: time.Now().Add(-time.Minute),
		End:   time.Now(),
		Blocked: []history.Dest{{Host: "telemetry.example.com",
			Method: history.MethodDNS, Count: 4}},
	})
	if err := h.run(t, "history", "--denied"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "telemetry.example.com") || !strings.Contains(out, "✗") {
		t.Errorf("the readable output changed:\n%s", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("JSON was printed without --json:\n%s", out)
	}
}

// The host is chosen by the workload and is about to be read by a person
// deciding whether to allow it. internal/textsafe exists for exactly that,
// and this output is the newest place that has to use it: a name that
// renders as one thing and grants another defeats the review the output
// exists to enable.
func TestHistoryJSONDoesNotCarryTerminalEscapesOutOfTheContainer(t *testing.T) {
	h := newHarness(t)
	record(t, h, history.Run{
		Start: time.Now().Add(-time.Minute),
		End:   time.Now(),
		Blocked: []history.Dest{{Host: "evil.example.com\u202e", Port: 443,
			Method: history.MethodConnect, Count: 1}},
	})
	if err := h.run(t, "history", "--json"); err != nil {
		t.Fatal(err)
	}
	var got historyJSON
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	host := got.Runs[0].Denied[0].Host
	if strings.ContainsRune(host, '\u202e') {
		t.Errorf("a bidi override reached the caller: %q", host)
	}
}
