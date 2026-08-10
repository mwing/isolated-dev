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

// resolveGrants turns configuration into the host access a run may actually
// receive, resolving what the acceptance authorizes against what the host
// really has.
//
// A value the project asked for applies only once this user has accepted it.
// The same value in the user's own global file needs no consent from them:
// it is their machine and nobody else asked. That distinction is the whole
// reason config records provenance.
func resolveGrants(ctx context.Context, env *Env, eng *container.Engine,
	cfg config.Config, store *trust.Store, image string) (project.Grants, error) {
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

	if cfg.MountDockerSocket && granted(cfg, store, "mount_docker_socket", "true") {
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

// gitConfigDenied are the gitconfig settings a filtered copy drops.
//
// A gitconfig is not identity. It can name a signing key the container
// cannot use, a credential helper that hands out tokens, and insteadOf rules
// that silently rewrite a remote to somewhere else — which would route a
// push past the allowlist by rewriting the destination rather than reaching
// it. The user granted "my git identity", so that is what is passed.
var gitConfigDenied = []string{
	"gpgsign",
	"gpg.program",
	"gpg.format",
	"signingkey",
	"credential",
	"helper",
	"insteadof",
	"sshcommand",
	"core.editor",
	"core.pager",
}

// filterGitConfig writes a filtered copy of the user's gitconfig under
// ~/.dev-envs and returns its path, or "" when there is nothing to copy.
//
// Filtering by line rather than by parsing: the goal is to remove settings,
// and a section header whose body is emptied is harmless, whereas a parser
// that rewrites the file could change the meaning of something it kept.
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
	b.WriteString("# Only identity is carried over. A container cannot sign, must not\n" +
		"# be handed tokens, and must not be able to rewrite where a remote points.\n")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		lower := strings.ToLower(line)
		// A section header is kept; only settings are dropped, and a
		// credential or url section becomes an empty one.
		if drop := !strings.HasPrefix(strings.TrimSpace(lower), "[") &&
			matchesAny(lower, gitConfigDenied); drop {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(lower), "[credential") ||
			strings.HasPrefix(strings.TrimSpace(lower), "[url ") {
			continue
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

func matchesAny(line string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(line, n) {
			return true
		}
	}
	return false
}
