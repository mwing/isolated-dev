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

	logs := `dev2-proxy: proxy listening on :3128
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
