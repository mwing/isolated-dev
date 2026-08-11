package netpolicy

import (
	"fmt"
	"strings"
	"unicode"
)

// Non-ASCII hostnames are refused in a grant, and the punycode form is
// asked for instead.
//
// The grant prompt is the one place a user widens policy by reading a
// hostname and deciding it looks right. "аpi.example.com" with a Cyrillic а
// reads identically to the Latin one at every font size, and a workload
// that got a grant for it can be handed a certificate for it. The
// difference is visible only as bytes.
//
// Refusing rather than normalizing silently is the point: converting to
// xn--pi-hmc.example.com behind the scenes would produce a grant the user
// never read. Showing them that what they typed is not what it looks like
// is the whole value.
//
// Punycode itself is accepted. Someone who genuinely needs an
// internationalized domain can name it in the form that is unambiguous, and
// that form is what appears in every later listing.
func checkASCIIHost(host string) error {
	if isASCII(host) {
		return nil
	}
	return fmt.Errorf(
		"%q contains non-ASCII characters, which can look identical to "+
			"ASCII ones: %s.\nUse the punycode form (xn--…) if you meant it",
		host, describeNonASCII(host))
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// describeNonASCII names the offending characters, since the whole problem
// is that they are invisible in the string itself.
func describeNonASCII(host string) string {
	var parts []string
	seen := map[rune]bool{}
	for _, r := range host {
		if r <= unicode.MaxASCII || seen[r] {
			continue
		}
		seen[r] = true
		parts = append(parts, fmt.Sprintf("%q (U+%04X)", r, r))
	}
	return strings.Join(parts, ", ")
}
