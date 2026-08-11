package netpolicy

import (
	"fmt"
	"testing"
)

// A retrying client picking generated names grows these maps inside the two
// processes that have to survive a whole session: the sidecar, and the
// user's own run watching it.
func TestTheDenialTallyIsBounded(t *testing.T) {
	p := NewProxy(mustParse(t))
	for i := 0; i < maxTrackedDestinations*2; i++ {
		p.emit(Event{Action: "deny", Host: fmt.Sprintf("h%d.example", i), Port: 443})
	}

	got := p.Denials()
	if len(got) > maxTrackedDestinations+1 {
		t.Fatalf("tally holds %d entries, cap is %d", len(got), maxTrackedDestinations)
	}
	// The overflow has to be visible: a summary that silently stops
	// counting is a summary that is wrong.
	if got[overflowKey] == 0 {
		t.Fatalf("nothing recorded that the tally is incomplete: %d entries", len(got))
	}
}

// A destination already being counted keeps counting past the cap. The
// point is to stop learning new names, not to stop reporting known ones.
func TestKnownDestinationsKeepCountingPastTheCap(t *testing.T) {
	p := NewProxy(mustParse(t))
	p.emit(Event{Action: "deny", Host: "first.example", Port: 443})
	for i := 0; i < maxTrackedDestinations*2; i++ {
		p.emit(Event{Action: "deny", Host: fmt.Sprintf("h%d.example", i), Port: 443})
	}
	p.emit(Event{Action: "deny", Host: "first.example", Port: 443})

	if n := p.Denials()["first.example:443"]; n != 2 {
		t.Fatalf("first.example:443 counted %d times, want 2", n)
	}
}

func TestTheWatcherIsBounded(t *testing.T) {
	var notices int
	w := NewWatcher(func(Notice) { notices++ })
	for i := 0; i < maxTrackedDestinations*2; i++ {
		w.Observe(Event{Action: "deny", Host: fmt.Sprintf("h%d.example", i), Port: 443})
	}
	if len(w.seen) > maxTrackedDestinations {
		t.Fatalf("watcher holds %d entries, cap is %d", len(w.seen), maxTrackedDestinations)
	}
	if notices > maxTrackedDestinations {
		t.Fatalf("emitted %d notices; past the cap nothing new should be reported", notices)
	}
}
