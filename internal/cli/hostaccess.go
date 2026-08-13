package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/trust"
)

// Host access — the four config keys that reach outside the sandbox.
//
// `mount_git_config`, `mount_docker_socket` and `pass_env_vars` were parsed,
// described to the user, gated behind `dev accept`, and reported by `doctor`
// and `migrate` — while nothing consumed them. The container never received
// any of it. A prompt that authorizes nothing is worse than no prompt: it
// teaches people to click through the one that matters.
//
// `mount_ssh_keys` is the fourth, and it is deliberately NOT implemented. It
// now loads as a dead key with an explanation. ROADMAP 4.1 says ssh access
// is "agent forwarding (socket only, never key files)", and it is right: a
// key inside the container is an exfiltratable secret, whereas a forwarded
// socket lets the container sign without ever reading the key and revoking
// is killing the agent rather than rotating a credential. `--allow-push`
// already does that. Implementing the mount would have meant contradicting a
// security promise to satisfy a config key nobody had honored anyway.

// breakGlass names the settings whose acceptance is not remembered by
// default: the run in front of you gets them, and the next one asks again.
//
// Only the docker socket so far, and for a reason the other keys do not
// share. Mounting it is root on the docker host — the sandbox contains
// nothing that can reach that — and a persistent acceptance is keyed by
// project *path*, so whatever occupies that path later inherits it. A new
// repository cloned over an old one asks for exactly the value already
// accepted, so value-sensitivity does not notice. Per-run closes that
// without needing to decide whose repository it is, which is the question
// B2 could not answer.
var breakGlass = map[string]bool{
	"mount_docker_socket": true,
}

// runGrants is host access authorized for this run alone, by a flag someone
// typed, rather than by a recorded acceptance.
type runGrants struct {
	dockerSocket bool
}

// authorizes reports whether this run was given the key outright.
func (r runGrants) authorizes(key string) bool {
	return key == "mount_docker_socket" && r.dockerSocket
}

// resolveGrants turns configuration into the host access a run may actually
// receive, resolving what the acceptance authorizes against what the host
// really has.
//
// A value the project asked for applies only once this user has accepted it.
// The same value in the user's own global file needs no consent from them:
// it is their machine and nobody else asked. That distinction is the whole
// reason config records provenance.
func resolveGrants(ctx context.Context, env *Env, eng *container.Engine,
	cfg config.Config, store *trust.Store, image string, run runGrants) (project.Grants, error) {
	var g project.Grants

	if granted(cfg, store, "pass_env_vars", passEnvValue(cfg)) {
		g.Env = cfg.PassEnvVars.Resolve(env.Env)
	}

	if cfg.MountGitConfig && granted(cfg, store, "mount_git_config", "true") {
		path, err := filterGitConfig(env)
		if err != nil {
			return g, err
		}
		// No host gitconfig means nothing to mount. Saying so beats mounting
		// an empty file and leaving the user to wonder why their identity did
		// not arrive.
		if path == "" {
			fmt.Fprintf(env.Stderr,
				"⚠  mount_git_config: no readable ~/.gitconfig, so nothing was mounted\n")
		}
		g.GitConfig = path
	}

	if cfg.MountDockerSocket &&
		(run.authorizes("mount_docker_socket") ||
			granted(cfg, store, "mount_docker_socket", "true")) {
		g.DockerSocket = project.DockerSocketPath
		// The socket's group has to be read from inside a container: host
		// file sharing decides the ownership the container sees, so a stat
		// here would report the wrong number. Without the group the mount is
		// present and unusable — the same half-honored grant in a new shape.
		gid, err := eng.StatGroup(ctx, image,
			project.DockerSocketPath, project.DockerSocketPath)
		if err != nil {
			return g, fmt.Errorf("mount_docker_socket: %w", err)
		}
		g.DockerSocketGID = gid
		fmt.Fprintf(env.Stderr,
			"⚠  mount_docker_socket: the container can control the docker daemon,\n"+
				"   which is root on the docker host. The sandbox does not contain\n"+
				"   anything that can reach it.\n")
	}

	return g, nil
}

// granted reports whether a setting may be honored.
func granted(cfg config.Config, store *trust.Store, key, value string) bool {
	if cfg.Origin(key) != config.OriginProject {
		return true
	}
	return store.AcceptedSettings()[key] == value
}

// passEnvValue renders the request the way the consent prompt recorded it, so
// the acceptance is compared against the same string it was given for. A
// widened list is therefore a different value and asks again.
func passEnvValue(cfg config.Config) string {
	return strings.Join(append(append([]string(nil), cfg.PassEnvVars.Patterns...),
		cfg.PassEnvVars.Explicit...), " ")
}

// describeGrants renders what a run was granted, for the run header. A grant
// that is honored silently is only half an improvement on one that is not
// honored at all.
func describeGrants(g project.Grants) []string {
	var out []string
	if g.GitConfig != "" {
		out = append(out, "gitconfig: filtered copy at "+project.SystemGitConfig+" (read-only)")
	}
	if g.DockerSocket != "" {
		out = append(out, "docker socket: "+project.DockerSocketPath+" (root on the docker host)")
	}
	if len(g.Env) > 0 {
		var names []string
		for _, kv := range g.Env {
			names = append(names, strings.SplitN(kv, "=", 2)[0])
		}
		out = append(out, "env: "+strings.Join(names, " "))
	}
	return out
}

// gitConfigCarried are the settings a filtered copy keeps.
//
// An allowlist, because the file said "only identity is carried over" while
// the mechanism was a denylist: everything unnamed passed, including
// settings that run programs — core.fsmonitor, filter.*.clean and .smudge,
// diff.*.textconv, alias.*, protocol.*.allow. The sandbox contains what
// those could do, but a list of what to exclude has to be complete to be
// right, and a list of what to include only has to be useful.
//
// What is deliberately absent: anything that signs (the container has no
// key), anything that hands out credentials, anything that rewrites where a
// remote points — which would route a push past the allowlist by changing
// the destination rather than reaching it — and anything naming a program
// or a path on the host, which is not there.
var gitConfigCarried = map[string]bool{
	"user.name":                 true,
	"user.email":                true,
	"init.defaultbranch":        true,
	"core.autocrlf":             true,
	"core.eol":                  true,
	"core.ignorecase":           true,
	"pull.rebase":               true,
	"pull.ff":                   true,
	"push.default":              true,
	"push.autosetupremote":      true,
	"fetch.prune":               true,
	"rebase.autostash":          true,
	"merge.conflictstyle":       true,
	"diff.algorithm":            true,
	"status.showuntrackedfiles": true,
}

// filterGitConfig writes a filtered copy of the user's gitconfig under
// ~/.dev-envs and returns its path, or "" when there is nothing to copy.
//
// Line-oriented rather than a full parse: what is written is a subset of
// what was read, verbatim, so nothing can acquire a meaning it did not
// have. Section headers are emitted only when something under them
// survives, which keeps the result readable and avoids an empty [url]
// section implying a rule that was dropped.
func filterGitConfig(env *Env) (string, error) {
	home := filepath.Dir(env.Paths.Home) // ~/.dev-envs -> ~
	src := filepath.Join(home, ".gitconfig")
	f, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", src, err)
	}
	defer func() { _ = f.Close() }()

	var b strings.Builder
	b.WriteString("# Filtered copy of " + src + ", written by dev.\n")
	b.WriteString("# Only identity and workflow preferences are carried over: a container\n" +
		"# cannot sign, must not be handed tokens, must not be able to rewrite\n" +
		"# where a remote points, and cannot run programs named on the host.\n")
	sc := bufio.NewScanner(f)
	section := ""
	headerWritten := false
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "[") {
			section = sectionName(trimmed)
			headerWritten = false
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		full := section + "." + strings.ToLower(strings.TrimSpace(key))
		if !gitConfigCarried[full] {
			continue
		}
		if !headerWritten {
			b.WriteString("[" + section + "]\n")
			headerWritten = true
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("reading %s: %w", src, err)
	}

	dir := filepath.Join(env.Paths.Home, "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// One file per project, rewritten each run: a stale copy would carry
	// settings the user has since removed from their own gitconfig.
	out := filepath.Join(dir, "gitconfig-"+projectSlug(env.Paths.ProjectDir))
	if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return out, nil
}

// sectionName normalizes a section header to the prefix a key is looked up
// under. A subsection — [url "https://x/"] — keeps only the section, since
// nothing carried has one and a subsection is how the dangerous rules are
// written.
func sectionName(header string) string {
	inner := strings.Trim(header, "[]")
	if i := strings.IndexAny(inner, " \t\""); i >= 0 {
		inner = inner[:i]
	}
	return strings.ToLower(strings.TrimSpace(inner))
}
