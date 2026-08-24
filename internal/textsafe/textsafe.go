// Package textsafe makes text that came from a container, a clone or a
// name off the wire safe to print on a terminal.
//
// Terminals execute some of what they are sent. An escape sequence in a
// commit subject, a denied hostname or a filename can erase the line above
// it, redraw a refusal as an approval, or leave the terminal in a state
// where the next command is not the one that was typed. Everything this
// tool prints about a sandboxed workload is chosen by that workload, and
// the review step is exactly where a reader has to be able to trust what
// they read.
//
// It lives in its own package because the three places that need it —
// clone output, console events, egress notices — have no other reason to
// depend on each other, and a copy in each is three chances to fix one of
// them and forget the rest.
package textsafe

import "strings"

// replacement stands in for a character that was removed.
const replacement = '\uFFFD'

// Sanitize replaces every character a terminal would act on rather than
// display.
//
// Replaced rather than dropped, so a name that tried something looks odd
// instead of merely shorter. Newlines and tabs survive: they are the
// formatting of the output this is embedded in.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			b.WriteRune(replacement)
		case r >= 0x80 && r <= 0x9f:
			b.WriteRune(replacement)
		// Line and paragraph separators: a terminal may or may not break on
		// them, which is enough to make one line look like two.
		case r == '\u2028' || r == '\u2029':
			b.WriteRune(replacement)
		// The bidirectional controls: they reorder what is displayed
		// without changing what is stored, so "fix typo" can render as
		// something else entirely. The marks (LRM, RLM, ALM) reorder in
		// most terminals just as the overrides do, so they are the same
		// case rather than a milder one.
		case r == '\u200e' || r == '\u200f' || r == '\u061c':
			b.WriteRune(replacement)
		case r >= '\u202a' && r <= '\u202e', r >= '\u2066' && r <= '\u2069':
			b.WriteRune(replacement)
		// Zero-width characters: invisible, and enough to make two
		// different names render identically.
		case r >= '\u200b' && r <= '\u200d', r == '\ufeff':
			b.WriteRune(replacement)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Lines applies Sanitize to text meant to be printed as a block, dropping a
// trailing newline so callers can format around it.
func Lines(s string) string {
	return strings.TrimRight(Sanitize(s), "\n")
}
