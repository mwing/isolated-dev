package textsafe

import (
	"strings"
	"testing"
)

func TestNameKeepsOneLine(t *testing.T) {
	if got := Name("evil\nblocked: fake.example\t\x1b[2J"); strings.ContainsAny(got, "\n\t\x1b") {
		t.Errorf("Name kept a control character: %q", got)
	}
}
