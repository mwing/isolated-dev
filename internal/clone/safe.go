package clone

import (
	"fmt"
	"strings"

	"github.com/mwing/isolated-dev/internal/textsafe"
)

// Everything in this file exists because a clone's contents are the
// agent's, and two of the three ways that data leaves the clone are not
// obviously dangerous until they are:
//
//   - As an argument to git. A branch or a ref name may begin with `-`, and
//     git reads that as an option. A value read out of a clone and passed
//     along positionally is an argument injection with a friendly name.
//   - As text on a terminal. A commit subject or an author name is a field
//     the committer chose, and terminals execute some of what they are
//     sent: an escape sequence can erase the line above, redraw a refusal
//     as an approval, or leave the terminal in a state where the next
//     command is not the one that was typed. The review step is exactly
//     where a reader must be able to trust what they read.
//
// The third way — patch bodies — is deliberately not sanitized. A diff is
// the content, byte for byte, and rewriting it would corrupt what the user
// asked to see.

// Sanitize and SanitizeLines make text that came out of a clone safe to
// print. They forward to internal/textsafe, which the console and the
// egress notices use for the same reason on names that came off the wire:
// one implementation, so a character added to one of them is added to all.
func Sanitize(s string) string { return textsafe.Sanitize(s) }

// SanitizeLines is Sanitize for a block of output, without the trailing
// newline.
func SanitizeLines(s string) string { return textsafe.Lines(s) }

// safeArg refuses a clone-derived string that git would read as an option
// or that is not a single line.
//
// Refuses rather than escapes. There is no quoting that makes `-P` not an
// option, `--` placement varies by subcommand, and a value that fails this
// is a value no legitimate clone produced — so the honest answer is to
// stop, named, rather than to run a command that means something else.
func safeArg(kind, s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("clone: empty %s", kind)
	}
	if strings.HasPrefix(s, "-") {
		return "", fmt.Errorf("clone: refusing %s %q: git would read it as an option", kind, s)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("clone: refusing %s %q: it contains a control character",
				kind, s)
		}
	}
	return s, nil
}

// safeSHA refuses anything that is not a plain object name.
//
// Every caller of this got its value from git's own `%H` or `rev-parse`, so
// a failure here means the value did not come from where the code thinks it
// did — which is worth an error rather than a shrug.
func safeSHA(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 4 || len(s) > 64 {
		return "", fmt.Errorf("clone: %q is not an object name", s)
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return "", fmt.Errorf("clone: %q is not an object name", s)
		}
	}
	return s, nil
}
