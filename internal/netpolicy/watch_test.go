package netpolicy

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type notices struct {
	mu   sync.Mutex
	list []Notice
}

func (n *notices) add(x Notice) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.list = append(n.list, x)
}

func (n *notices) all() []Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Notice(nil), n.list...)
}

func TestWatcherNotifiesOnFirstDenial(t *testing.T) {
	got := &notices{}
	w := NewWatcher(got.add)

	logs := `dev-proxy: proxy listening on :3128
{"action":"allow","host":"api.anthropic.com","port":443,"method":"CONNECT"}
{"action":"deny","host":"evil.example.com","port":443,"method":"CONNECT","reason":"not in allowlist"}
`
	if err := w.Run(strings.NewReader(logs)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	list := got.all()
	if len(list) != 1 {
		t.Fatalf("notices = %+v", list)
	}
	if list[0].Destination != "evil.example.com:443" {
		t.Errorf("destination = %q", list[0].Destination)
	}
	if !strings.Contains(list[0].String(), "blocked") {
		t.Errorf("String() = %q", list[0].String())
	}
}

func TestWatcherIgnoresAllowEvents(t *testing.T) {
	got := &notices{}
	w := NewWatcher(got.add)
	logs := `{"action":"allow","host":"api.anthropic.com","port":443,"method":"CONNECT"}` + "\n"
	_ = w.Run(strings.NewReader(logs))
	if len(got.all()) != 0 {
		t.Fatalf("allow events produced notices: %+v", got.all())
	}
}

func TestWatcherSuppressesRepeatsButCountsThem(t *testing.T) {
	// A retrying client can produce hundreds of identical denials. A
	// notifier that prints all of them is one the user turns off.
	got := &notices{}
	w := NewWatcher(got.add)
	now := time.Unix(0, 0)
	w.Now = func() time.Time { return now }

	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString(`{"action":"deny","host":"evil.example.com","port":443,"method":"CONNECT"}` + "\n")
	}
	_ = w.Run(strings.NewReader(b.String()))

	if n := len(got.all()); n != 1 {
		t.Fatalf("got %d notices for 50 identical denials, want 1", n)
	}
	if total := w.Totals()["evil.example.com:443"]; total != 50 {
		t.Errorf("total = %d, want 50", total)
	}
}

func TestWatcherRenotifiesAfterInterval(t *testing.T) {
	// Silence forever is wrong too: a destination still being hit minutes
	// later is new information.
	got := &notices{}
	w := NewWatcher(got.add)
	w.RepeatAfter = time.Minute
	now := time.Unix(0, 0)
	w.Now = func() time.Time { return now }

	w.Observe(Event{Action: "deny", Host: "evil.example.com", Port: 443})
	w.Observe(Event{Action: "deny", Host: "evil.example.com", Port: 443})
	now = now.Add(2 * time.Minute)
	w.Observe(Event{Action: "deny", Host: "evil.example.com", Port: 443})

	list := got.all()
	if len(list) != 2 {
		t.Fatalf("got %d notices, want 2 (first + repeat)", len(list))
	}
	if list[1].Count != 3 {
		t.Errorf("repeat notice count = %d, want the running total 3", list[1].Count)
	}
}

func TestWatcherDistinguishesDestinations(t *testing.T) {
	got := &notices{}
	w := NewWatcher(got.add)

	w.Observe(Event{Action: "deny", Host: "a.example.com", Port: 443})
	w.Observe(Event{Action: "deny", Host: "b.example.com", Port: 443})
	w.Observe(Event{Action: "deny", Host: "a.example.com", Port: 80})

	if n := len(got.all()); n != 3 {
		t.Fatalf("got %d notices, want 3 distinct destinations", n)
	}
}

func TestWatcherLabelsDNSDenials(t *testing.T) {
	got := &notices{}
	w := NewWatcher(got.add)
	w.Observe(Event{Action: "deny", Host: "exfil.example.com", Method: "DNS"})

	list := got.all()
	if len(list) != 1 {
		t.Fatalf("notices = %+v", list)
	}
	if !strings.Contains(list[0].String(), "DNS") {
		t.Errorf("DNS denial not labelled: %q", list[0].String())
	}
}

func TestWatcherToleratesGarbageLines(t *testing.T) {
	got := &notices{}
	w := NewWatcher(got.add)
	logs := "not json\n{broken\n" +
		`{"action":"deny","host":"evil.example.com","port":443}` + "\n"
	if err := w.Run(strings.NewReader(logs)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.all()) != 1 {
		t.Fatalf("notices = %+v", got.all())
	}
}

// The review's finding, and the reason destinations are recorded as
// fields: the method used to be carried in-band, as the suffix " (DNS)"
// on the host. A hostname is text the workload chooses, and the proxy
// emits a denial with the raw request-line target when it cannot parse one
// — so a container could describe its own refusal as a name lookup, which
// is the one thing a reader uses to decide whether the request could have
// been held for an answer.
func TestAWorkloadCannotChooseHowItsDenialIsDescribed(t *testing.T) {
	logs := `{"action":"deny","host":"evil.example.com (DNS)","method":"CONNECT","reason":"bad target"}
{"action":"deny","host":"real.example.com","method":"DNS","reason":"not in allowlist"}`

	byHost := map[string]Destination{}
	for _, d := range Destinations(logs) {
		byHost[d.Host] = d
	}

	forged, ok := byHost["evil.example.com (DNS)"]
	if !ok {
		t.Fatalf("the forged host was not recorded as itself: %+v", byHost)
	}
	if forged.Method != "connect" {
		t.Errorf("a proxy refusal was described as %q because the host said so",
			forged.Method)
	}
	if real, ok := byHost["real.example.com"]; !ok || real.Method != "DNS" {
		t.Errorf("a real name refusal lost its method: %+v", real)
	}
}

// A name resolved is not a destination reached: counting an allowed lookup
// as contact would be exactly the claim a grant review must not get wrong.
func TestAnAllowedLookupIsNotAContact(t *testing.T) {
	logs := `{"action":"allow","host":"example.com","method":"DNS"}
{"action":"allow","host":"example.com","port":443,"method":"CONNECT"}`
	var reached []Destination
	for _, d := range Destinations(logs) {
		if !d.Denied {
			reached = append(reached, d)
		}
	}
	if len(reached) != 1 || reached[0].Port != 443 {
		t.Errorf("reached = %+v, want one connection on 443", reached)
	}
}
