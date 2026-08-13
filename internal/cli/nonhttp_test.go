package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestNonHTTPPortsAreTheExplicitlyNamedOnes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    []int
	}{
		// A bare hostname carries the default ports, so it is HTTP by
		// construction and there is nothing to say about it.
		{"a bare host", []string{"api.example.com"}, nil},
		{"an explicit http port", []string{"api.example.com:443"}, nil},
		{"an explicit port 80", []string{"api.example.com:80"}, nil},
		{"a wildcard", []string{"*.example.com"}, nil},

		{"postgres", []string{"db.example.com:5432"}, []int{5432}},
		{"ssh", []string{"github.com:22"}, []int{22}},
		{"sorted and deduplicated", []string{
			"a.example.com:6379", "b.example.com:5432", "c.example.com:6379",
		}, []int{5432, 6379}},
		{"mixed with http", []string{"api.example.com", "db.example.com:5432"}, []int{5432}},

		// Nothing usable to say about input that does not parse; the caller
		// has already refused it.
		{"unparseable", []string{"not a host"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nonHTTPPorts(tc.entries)
			if len(got) != len(tc.want) {
				t.Fatalf("nonHTTPPorts(%v) = %v, want %v", tc.entries, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("nonHTTPPorts(%v) = %v, want %v", tc.entries, got, tc.want)
				}
			}
		})
	}
}

// The grant is real — the proxy relays it — but a raw TCP client gets
// "network is unreachable", which names the network rather than the policy
// and sends people looking in the wrong place.
func TestTheNoteSaysWhatIsAndIsNotReachable(t *testing.T) {
	var b bytes.Buffer
	explainNonHTTP(&b, []string{"db.example.com:5432"})
	out := b.String()

	for _, want := range []string{
		"5432",
		"permitted",              // the grant did happen
		"no route out",           // why it still fails
		"HTTP_PROXY",             // what works already
		"psql",                   // what does not
		"network is unreachable", // the error they will actually see
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the note does not mention %q:\n%s", want, out)
		}
	}
}

// A note that appears on ordinary grants is a note that stops being read.
func TestNoNoteForAnOrdinaryGrant(t *testing.T) {
	var b bytes.Buffer
	explainNonHTTP(&b, []string{"api.example.com", "*.githubusercontent.com"})
	if b.Len() != 0 {
		t.Errorf("said something about an ordinary grant:\n%s", b.String())
	}
}

func TestAllowPrintsTheNote(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "allow", "db.example.com:5432"); err != nil {
		t.Fatalf("allow: %v", err)
	}
	if !strings.Contains(h.stdout.String(), "not an HTTP port") {
		t.Errorf("granting a database port explained nothing:\n%s", h.stdout.String())
	}
}
