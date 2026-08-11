package netpolicy

import (
	"strings"
	"testing"
)

// The grant prompt is where a user widens policy by reading a hostname and
// deciding it looks right. These two look identical and are not.
func TestAHomoglyphHostIsRefused(t *testing.T) {
	latin := "api.example.com"
	cyrillic := "аpi.example.com" // U+0430 CYRILLIC SMALL LETTER A

	if _, err := Parse([]string{latin}); err != nil {
		t.Fatalf("the ordinary host was refused: %v", err)
	}
	_, err := Parse([]string{cyrillic})
	if err == nil {
		t.Fatal("a host with a Cyrillic 'a' was accepted as if it were the Latin one")
	}
	// The refusal must point at the character, because the string itself
	// gives nothing away.
	if !strings.Contains(err.Error(), "U+0430") {
		t.Fatalf("refusal does not name the character: %v", err)
	}
}

// Someone who genuinely needs an internationalized domain can name it in
// the form that is unambiguous.
func TestPunycodeIsAccepted(t *testing.T) {
	a, err := Parse([]string{"xn--bcher-kva.example.com"})
	if err != nil {
		t.Fatalf("punycode was refused: %v", err)
	}
	if !a.Allows("xn--bcher-kva.example.com", 443) {
		t.Fatal("the punycode host does not match itself")
	}
}
