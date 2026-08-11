package netpolicy

import (
	"fmt"
	"strings"
)

// Limits on the shape of a name the resolver will forward.
//
// These exist because of what a wildcard grant permits. Filtering by name
// stops a workload resolving a destination nobody allowed; it does not stop
// it encoding data into a name that is allowed. With `*.example.com`
// granted, `<base32-payload>.example.com` is a legal query, needs no answer
// to have delivered its content, and is recorded as an allow.
//
// The limits are generous on purpose. Refusing a name a real service uses
// is a broken build with a confusing cause, which costs more than the
// narrow leak it would close — a tunnel that has to fit inside these is
// slow and loud enough to be a poor choice for an attacker who already has
// an allowlisted HTTPS host.
const (
	// maxQueryLength is the whole name. DNS permits 253; real names are far
	// shorter, and an encoder wants every byte it can get.
	maxQueryLength = 128
	// maxLabelLength is one dot-separated part. The protocol limit is 63,
	// which is exactly what an encoder fills.
	maxLabelLength = 48
	// maxLabels is the depth. Four or five is ordinary; a dozen is someone
	// spreading a payload across labels to dodge a length limit.
	maxLabels = 8
)

// suspiciousQuery reports why a name looks like a carrier rather than a
// destination, or "" when it looks ordinary.
func suspiciousQuery(name string) string {
	if len(name) > maxQueryLength {
		return fmt.Sprintf("name is %d characters, over the %d-character limit "+
			"for a forwarded query", len(name), maxQueryLength)
	}
	labels := strings.Split(name, ".")
	if len(labels) > maxLabels {
		return fmt.Sprintf("name has %d labels, over the limit of %d",
			len(labels), maxLabels)
	}
	for _, l := range labels {
		if len(l) > maxLabelLength {
			return fmt.Sprintf("label %q is %d characters, over the limit of %d",
				truncateLabel(l), len(l), maxLabelLength)
		}
	}
	return ""
}

// truncateLabel keeps a refusal message readable when the label is the
// problem: printing 63 characters of base32 tells the reader nothing the
// count did not.
func truncateLabel(l string) string {
	if len(l) <= 16 {
		return l
	}
	return l[:16] + "…"
}
