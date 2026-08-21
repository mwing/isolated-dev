package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/trust"
)

// Consent before a repository's own Dockerfile is built.
//
// `dev run` builds before it sandboxes, and a build is not egress-filtered
// (ROADMAP 4.3.1): it fetches whatever its instructions name, over an
// ordinary network, with the project directory as its context. A
// repository's own Dockerfile also takes precedence over the language
// template. So for an unfamiliar repo the first thing that happens is
// arbitrary code fetching arbitrary things — before the sandbox anyone came
// for exists.
//
// The user's model is "dev run means run this safely". Documenting the gap
// does not close it; asking once does, and the answer is remembered.
//
// The language template is offered as the alternative because it is the
// honest other option: it builds a stock image for the detected language
// and ignores what the repository wants, which is exactly right for
// looking at code you do not trust yet.

// buildSourceKey is the consent key. It sits with the other settings so it
// is reviewed by the same `dev accept` and recorded in the same file.
const buildSourceKey = "build_source"

// buildSourceAsk describes the project-supplied Dockerfile a build would
// use, or nil when there is nothing to ask about.
//
// The value carries a digest of the file, so the acceptance mechanism's
// existing value-sensitivity does the right thing: a Dockerfile that
// changes is new build instructions running unfiltered, and asks again.
// That is the same rule `tools` and `pass_env_vars` already follow.
//
// What the digest does not cover, and the wording must therefore not
// imply: the file is rarely the whole program. `COPY . .` then `RUN
// ./build.sh` makes the build context part of what runs, and the context
// changes on every save — hashing it would re-ask constantly, which is a
// prompt nobody reads. So the accepted thing is this repository supplying
// build instructions, and the digest earns its place more narrowly: the
// visible instructions changing is the case worth asking about twice.
func buildSourceAsk(p *project.Project) *trust.Ask {
	if p == nil || p.Dockerfile == "" {
		// A rendered template is this tool's own file, and an image named
		// by a devcontainer is pulled rather than built. Neither is the
		// repository handing instructions to an unfiltered build.
		return nil
	}
	rel, err := filepath.Rel(p.Dir, p.Dockerfile)
	if err != nil {
		rel = p.Dockerfile
	}

	digest := "unreadable"
	if raw, err := os.ReadFile(p.Dockerfile); err == nil {
		sum := sha256.Sum256(raw)
		digest = hex.EncodeToString(sum[:])[:12]
	}

	effect := fmt.Sprintf("trust this repository to supply build "+
		"instructions: %s, and whatever that file runs from this directory. "+
		"A build is NOT egress-filtered — it runs them over an ordinary "+
		"network before the sandbox exists. The alternative is the %s "+
		"template, which ignores this file",
		rel, describeLanguage(p))

	return &trust.Ask{Key: buildSourceKey, Value: rel + "@" + digest, Effect: effect}
}

func describeLanguage(p *project.Project) string {
	if p != nil && p.Detected.Found() {
		return p.Detected.Language.Name
	}
	return "language"
}

// buildSourceAccepted reports whether this exact file has been accepted.
func buildSourceAccepted(p *project.Project, store *trust.Store) bool {
	ask := buildSourceAsk(p)
	if ask == nil {
		return true
	}
	return store.AcceptedSettings()[buildSourceKey] == ask.Value
}

// resolveBuildSource decides what a build will use, applying an explicit
// choice and otherwise requiring consent.
//
// Choosing the template is always allowed without asking: it narrows what
// runs rather than widening it, and a user who wants a stock image for a
// repository they are inspecting should not have to accept anything first.
func resolveBuildSource(env *Env, p *project.Project, store *trust.Store, choice string) error {
	switch choice {
	case "template":
		if !p.Detected.Found() {
			return fmt.Errorf("--build-source template needs a detected language; " +
				"this directory has none")
		}
		p.UseTemplate()
		return nil
	case "project":
		if p.Dockerfile == "" {
			return fmt.Errorf("--build-source project needs a Dockerfile in %s", p.Dir)
		}
		return nil
	case "", "auto":
	default:
		return fmt.Errorf("unknown --build-source %q; use project or template", choice)
	}

	if buildSourceAccepted(p, store) {
		return nil
	}

	ask := buildSourceAsk(p)
	fmt.Fprintf(env.Stderr, "This project supplies its own build instructions.\n\n")
	fmt.Fprintf(env.Stderr, "  %s\n", ask.Effect)
	fmt.Fprintf(env.Stderr, "\nA build runs before the sandbox does, and is not filtered.\n")
	fmt.Fprintf(env.Stderr, "Accepting covers the file and anything it runs from this\n")
	fmt.Fprintf(env.Stderr, "directory; a change to the file itself asks again.\n")
	fmt.Fprintf(env.Stderr, "\nAccept it once:      dev accept %s\n", buildSourceKey)
	if p.Detected.Found() {
		fmt.Fprintf(env.Stderr, "Or ignore the file:  --build-source template\n")
	}
	fmt.Fprintf(env.Stderr, "Read it first:       %s\n", p.Dockerfile)
	return fmt.Errorf("the build source has not been accepted")
}
