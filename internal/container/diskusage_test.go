package container

import "testing"

// docker computes reclaimable space per type and gets it wrong where layers
// are shared. On a real daemon it reported "-4.034e+09B (-26%)" for images —
// in its own table as well as through a template — and this tool relayed it,
// which made the tool look broken for a fault it does not have.
//
// A negative quantity of reclaimable space is not something a reader can act
// on, so it is left out rather than passed through.
func TestNonsenseReclaimableValuesAreDropped(t *testing.T) {
	for _, v := range []string{
		"-4.034e+09B (-26%)", // the observed case
		"-1.2GB (-5%)",       // plainly negative
		"1.5e+09B (12%)",     // %g fallback, positive but unreadable
		"0B",                 // true but not worth a column
		"",
	} {
		if got := reclaimable(v); got != "" {
			t.Errorf("reclaimable(%q) = %q, want it dropped", v, got)
		}
	}
}

// The ordinary case still reports, since that is the number people came for.
func TestUsefulReclaimableValuesSurvive(t *testing.T) {
	for _, v := range []string{"110.7MB (100%)", "2.412GB (100%)", "512kB (3%)"} {
		if got := reclaimable(v); got != v {
			t.Errorf("reclaimable(%q) = %q, want it kept", v, got)
		}
	}
}
