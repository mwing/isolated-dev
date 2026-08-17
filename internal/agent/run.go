package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/netpolicy"
)

// workspaceSource is the host directory to mount.
func (o Options) workspaceSource() string {
	if o.Workspace != "" {
		return o.Workspace
	}
	return o.Project
}

// WorkspacePath is where the project is mounted inside the container.
const WorkspacePath = "/workspace"

// HomePath is the agent's home directory, backed by a named volume.
const HomePath = "/home/dev"

// Options configure one agent run.
type Options struct {
	Agent   *Agent
	Project string // host path the run belongs to
	// Workspace overrides what is mounted at /workspace. It is set for a
	// clone run, where the container works in a private copy while the
	// run still belongs to Project — grants, history and image are keyed
	// by the project, not by whatever directory is mounted.
	Workspace string
	// ExtraHosts are per-run additions from --allow-host.
	ExtraHosts []string
	// AuthMode is "volume" (default, OAuth persists in a named volume) or
	// "env" (an API key by name, for CI).
	AuthMode string
	// AuthEnv carries NAME=VALUE pairs when AuthMode is "env". The value
	// is read from the caller's environment only for names the user named
	// explicitly; nothing is passed implicitly.
	AuthEnv []string
	// Args are passed to the agent after its own default arguments, so a
	// prompt or a flag adds to the invocation rather than replacing it.
	Args []string
	// Interactive attaches a TTY.
	Interactive bool
	// Safe drops the agent's auto-approve arguments, restoring its own
	// per-action prompts on top of the sandbox.
	Safe bool
	// SSHAuthSock is the host path of an ssh-agent socket to forward when
	// push is granted. The socket only, never a key file: the key stays on
	// the host, the grant dies when the agent does, and nothing
	// exfiltratable enters the container.
	SSHAuthSock string
	// GitIdentity is the name and email for commits made in the container.
	GitIdentity [2]string
	// SSHSockGID is the group that owns the forwarded socket as the
	// container sees it. The host's file sharing decides that group, so it
	// is discovered rather than assumed.
	SSHSockGID string
	// Image is the project image to overlay. Empty uses the agent's base.
	Image string
	// Memory and CPUs bound the container.
	Memory string
	CPUs   string
}

// Allowlist is the effective policy for a run: the agent's defaults plus
// anything the user allowed for this invocation.
func (o Options) Allowlist() []string {
	out := append([]string(nil), o.Agent.AllowHosts...)
	out = append(out, o.ExtraHosts...)
	return out
}

// BaseImage returns the image the overlay is built on.
func (o Options) BaseImage() string {
	if o.Image != "" {
		return o.Image
	}
	return o.Agent.Base
}

// SSHSockPath is where a forwarded ssh-agent socket appears.
const SSHSockPath = "/run/ssh-agent.sock"

// RuntimePath is where a copied runtime is installed. It is deliberately
// not /usr/local: that would clobber toolchains the base image keeps there,
// notably Go.
const RuntimePath = "/opt/dev-runtime"

// Dockerfile renders the overlay layer. The project's own Dockerfile is
// never modified: the agent is added on top, so removing the agent is a
// matter of not using the overlay tag.
func Dockerfile(a *Agent, base string) string {
	var b strings.Builder
	if a.Runtime == "node" {
		fmt.Fprintf(&b, "FROM %s AS runtime\n", a.runtimeImage())
	}
	fmt.Fprintf(&b, "FROM %s\n", base)
	// A fixed uid/gid matching the --user the tool passes, so files the
	// agent writes into the workspace belong to the invoking user.
	b.WriteString("USER root\n")
	// git is not optional: the agent commits inside the container and the
	// human reviews and pushes from the host. Without it the
	// review boundary has nothing to review. ca-certificates is needed to
	// verify TLS through the proxy, which does not terminate it.
	b.WriteString("RUN (command -v apt-get >/dev/null && apt-get update && " +
		"DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends " +
		"git curl ca-certificates netcat-openbsd && rm -rf /var/lib/apt/lists/*) || " +
		"(command -v apk >/dev/null && apk add --no-cache git curl ca-certificates netcat-openbsd) || true\n")
	// The account matches the host's, for the same reason the project image
	// does: the agent works in a clone on a bind mount, and on Linux a
	// container running as anyone else cannot write to it. The overlay may
	// sit on the project image, which already created this uid, so both
	// steps tolerate it existing.
	b.WriteString("ARG DEV_UID=1000\n")
	b.WriteString("ARG DEV_GID=1000\n")
	b.WriteString("RUN (getent group \"$DEV_GID\" || groupadd -g \"$DEV_GID\" dev) >/dev/null 2>&1 || true\n")
	b.WriteString("RUN (getent passwd \"$DEV_UID\" >/dev/null 2>&1) || " +
		"useradd -u \"$DEV_UID\" -g \"$DEV_GID\" -m -d " + HomePath + " -s /bin/bash dev\n")
	b.WriteString("RUN mkdir -p " + HomePath + " && chown -R \"$DEV_UID\":\"$DEV_GID\" " + HomePath + "\n")
	if a.Runtime == "node" {
		// Copied into its own prefix rather than /usr/local, so a base
		// image that keeps a toolchain there (golang) survives intact.
		// npm derives its global prefix from node's location, so global
		// installs land here too.
		fmt.Fprintf(&b, "COPY --from=runtime /usr/local/bin/node %s/bin/node\n", RuntimePath)
		fmt.Fprintf(&b, "COPY --from=runtime /usr/local/lib/node_modules/npm %s/lib/node_modules/npm\n", RuntimePath)
		fmt.Fprintf(&b, "RUN ln -sf %s/lib/node_modules/npm/bin/npm-cli.js %s/bin/npm && "+
			"ln -sf %s/lib/node_modules/npm/bin/npx-cli.js %s/bin/npx\n",
			RuntimePath, RuntimePath, RuntimePath, RuntimePath)
		fmt.Fprintf(&b, "ENV PATH=%s/bin:$PATH\n", RuntimePath)
	}
	if a.Install != "" {
		// The version reaches the install command, which it did not before:
		// Version was declared, warned about when unpinned, and baked into
		// the image tag, while `npm install -g pkg` fetched whatever npm
		// felt like. So the tag named a version nothing had installed, and
		// two builds of the same "pinned" agent could differ. A pin that
		// does not reach the fetch is decoration.
		fmt.Fprintf(&b, "RUN %s\n", a.InstallCommand())
	}
	fmt.Fprintf(&b, "RUN mkdir -p %s && chown -R \"$DEV_UID\":\"$DEV_GID\" %s\n", a.ConfigDir, a.ConfigDir)
	b.WriteString("USER $DEV_UID:$DEV_GID\n")
	fmt.Fprintf(&b, "WORKDIR %s\n", WorkspacePath)
	return b.String()
}

// Spec builds the RunSpec for the agent container.
//
// The posture is fixed at untrusted regardless of the project's trust
// level (ROADMAP 4.1): an agent acts on instructions from a model, so it
// does not inherit a human's decision to trust this project.
func Spec(o Options, topo netpolicy.Topology) container.RunSpec {
	a := o.Agent

	spec := container.Hardened()
	spec.Image = a.ImageTag(o.BaseImage())
	spec.Network = topo.InternalNetwork
	spec.DNS = []string{topo.SidecarIP}
	spec.WorkDir = WorkspacePath
	spec.Interactive = true
	spec.TTY = o.Interactive
	spec.Remove = true
	spec.Memory = o.Memory
	spec.CPUs = o.CPUs
	spec.Labels = map[string]string{
		"dev.role":  "agent",
		"dev.agent": a.Name,
	}

	spec.Mounts = []container.Mount{
		{Source: o.workspaceSource(), Target: WorkspacePath},
		// The agent's home is a named volume, so an OAuth login survives
		// across runs without any credential touching the project tree.
		{Source: a.VolumeName(), Target: HomePath, Volume: true},
	}

	// Agent defaults first, sandbox variables second: docker takes the
	// last --env for a name, so the topology's proxy settings win over
	// anything an agent definition declares.
	spec.Env = append(spec.Env, a.Env...)
	spec.Env = append(spec.Env, topo.Env()...)
	spec.Env = append(spec.Env,
		"HOME="+HomePath,
		"DEV2_SANDBOX=1",
	)

	// Commits are always possible; pushing is not. The identity is
	// generated rather than copied from the host gitconfig, which carries
	// more than a name (signing keys, credential helpers, insteadOf rules
	// that redirect remotes).
	if o.GitIdentity[0] != "" {
		spec.Env = append(spec.Env,
			"GIT_AUTHOR_NAME="+o.GitIdentity[0],
			"GIT_COMMITTER_NAME="+o.GitIdentity[0])
	}
	if o.GitIdentity[1] != "" {
		spec.Env = append(spec.Env,
			"GIT_AUTHOR_EMAIL="+o.GitIdentity[1],
			"GIT_COMMITTER_EMAIL="+o.GitIdentity[1])
	}

	if o.SSHAuthSock != "" {
		spec.Mounts = append(spec.Mounts, container.Mount{
			Source: o.SSHAuthSock, Target: SSHSockPath,
		})
		spec.Env = append(spec.Env, "SSH_AUTH_SOCK="+SSHSockPath)
		// Reaching the socket through a supplementary group keeps the
		// fixed uid intact. Running as the socket's owner instead would
		// hand the container the host user's identity.
		if o.SSHSockGID != "" {
			spec.GroupAdd = append(spec.GroupAdd, o.SSHSockGID)
		}
		// ssh does not speak HTTP proxying, and the container has no other
		// route out, so a forwarded agent alone gets "network unreachable".
		// Routing ssh through the same CONNECT proxy keeps git subject to
		// the allowlist instead of needing a hole punched for it — and a
		// hole would be invisible to the policy, since traffic that never
		// reaches the proxy is never reported as blocked.
		if topo.SidecarIP != "" {
			// accept-new records the host key on first use and refuses a
			// CHANGED key thereafter, unlike StrictHostKeyChecking=no which
			// would accept a substituted key silently. The key persists in
			// the agent's home volume.
			spec.Env = append(spec.Env, fmt.Sprintf(
				`GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=accept-new `+
					`-o UserKnownHostsFile=%s/.dev_known_hosts `+
					`-o ProxyCommand="nc -X connect -x %s:%d %%h %%p"`,
				HomePath, topo.SidecarIP, proxyPortOr(topo.ProxyPort)))
		}
	}
	if o.AuthMode == "env" {
		spec.Env = append(spec.Env, o.AuthEnv...)
	}

	spec.Command = []string{a.Binary}
	// The agent's default args are its auto-approve flags. They are
	// only defensible because the sandbox is the boundary: no host
	// credentials, no route out beyond the allowlist, no host path
	// but the workspace. --safe drops them for anyone who wants the
	// in-agent prompts as a second layer.
	if !o.Safe {
		spec.Command = append(spec.Command, a.Args...)
	}
	// Trailing arguments go to the agent, they do not replace it.
	// `dev agent run claude -- "fix the retry logic"` reads as a prompt
	// in every other tool that takes one, and replacing the command made
	// it exec the prompt: "executable file not found", status 127, for
	// what is the most obvious way to use this command.
	spec.Command = append(spec.Command, o.Args...)
	return spec
}

func proxyPortOr(p int) int {
	if p == 0 {
		return 3128
	}
	return p
}

// runtimeImage returns the pinned image a runtime is copied from.
func (a *Agent) runtimeImage() string {
	if a.RuntimeImage != "" {
		return a.RuntimeImage
	}
	return "node:22-bookworm-slim"
}

// Runner orchestrates a full agent run.
type Runner struct {
	Engine  *container.Engine
	Sidecar *netpolicy.Sidecar
	// Out receives progress messages.
	Out io.Writer
}

// EnsureImage builds the overlay image if it is missing.
func (r *Runner) EnsureImage(ctx context.Context, o Options, force bool) (string, error) {
	tag := o.Agent.ImageTag(o.BaseImage())
	if !force {
		exists, err := r.Engine.ImageExists(ctx, tag)
		if err != nil {
			return "", err
		}
		if exists {
			return tag, nil
		}
	}

	fmt.Fprintf(r.Out, "Building agent image %s...\n", tag)
	df := Dockerfile(o.Agent, o.BaseImage())
	// Context "-" with the Dockerfile on stdin and no --file: the overlay
	// adds only the agent, so it needs no build context at all. Passing
	// --file - as well is what docker rejects.
	err := r.Engine.Build(ctx, container.BuildSpec{
		Tag:       tag,
		Context:   "-",
		BuildArgs: container.UIDBuildArgs(),
	}, strings.NewReader(df), r.Out)
	if err != nil {
		return "", err
	}
	return tag, nil
}

// SocketGID reports the group owning a forwarded socket as the container
// sees it. Host file sharing remaps ownership, so the value cannot be
// derived from a stat on the host.
func (r *Runner) SocketGID(ctx context.Context, image, hostSock string) (string, error) {
	return r.Engine.StatGroup(ctx, image, hostSock, SSHSockPath)
}

// EnsureVolume creates the agent's home volume if absent, adopting the one
// the tool used under its old name.
//
// The rename from dev2 to dev moved this volume, and the volume holds an
// OAuth login. Creating a fresh empty one would look like a bug — the
// agent simply asks you to log in again, with nothing to explain why — so
// the contents are carried over once and the old volume is left in place
// for anyone who wants to be sure before deleting it.
func (r *Runner) EnsureVolume(ctx context.Context, a *Agent) error {
	exists, err := r.Engine.VolumeExists(ctx, a.VolumeName())
	if err != nil {
		return err
	}
	if exists {
		return r.repairVolumeOwner(ctx, a)
	}
	if err := r.Engine.VolumeCreate(ctx, a.VolumeName()); err != nil {
		return err
	}
	// No ownership repair on this path. A new volume is seeded from the
	// image, and the image tag carries the uid, so the one that seeds it
	// was built for the uid this run uses. Probing here would be a
	// container started to confirm something already true.
	return r.adoptLegacyVolume(ctx, a)
}

// repairVolumeOwner makes an existing home volume belong to the uid the
// agent now runs as.
//
// The volume outlives the image, and docker only seeds one when it is
// created — an existing volume keeps whatever ownership it was populated
// with. So when runs stopped being a fixed uid 1000 and became the host's,
// every already-logged-in agent found a home directory it could not write:
// the OAuth exchange succeeded, "Logged in as ..." was printed, and the
// credential could not be saved, so the next command was logged out again.
// A failure that reports success is the worst shape available, and this is
// the migration that stops it.
//
// Checked before changing anything, because -R over a home directory that
// has accumulated caches is not free, and because doing it every run would
// be a chown nobody asked for.
func (r *Runner) repairVolumeOwner(ctx context.Context, a *Agent) error {
	want := fmt.Sprintf("%d:%d", container.HostUID(), container.HostGID())

	var out bytes.Buffer
	probe := container.RunSpec{
		Image:   "alpine",
		Remove:  true,
		User:    "0:0",
		Command: []string{"stat", "-c", "%u:%g", HomePath},
		Mounts: []container.Mount{
			{Source: a.VolumeName(), Target: HomePath, Volume: true},
		},
	}
	if _, err := r.Engine.Run(ctx, probe, nil, &out, io.Discard); err != nil {
		// Not fatal: the agent may still work, and refusing to start it
		// over a check would be worse than the thing being checked.
		return nil
	}
	if strings.TrimSpace(out.String()) == want {
		return nil
	}

	fix := probe
	fix.Command = []string{"chown", "-R", want, HomePath}
	if _, err := r.Engine.Run(ctx, fix, nil, io.Discard, io.Discard); err != nil {
		fmt.Fprintf(r.Out, "⚠  %s belongs to %s and this run is %s, so the agent may not be\n"+
			"   able to save its login. Repair it with:\n"+
			"     docker run --rm -u 0 -v %s:%s alpine chown -R %s %s\n",
			a.VolumeName(), strings.TrimSpace(out.String()), want,
			a.VolumeName(), HomePath, want, HomePath)
		return nil
	}
	fmt.Fprintf(r.Out, "Adjusted %s to uid %s, which this run uses.\n", a.VolumeName(), want)
	return nil
}

// legacyVolumeName is what this volume was called before the tool was
// renamed. It can be deleted once nobody is upgrading across that change.
func legacyVolumeName(a *Agent) string { return "dev2-agent-" + a.Name }

func (r *Runner) adoptLegacyVolume(ctx context.Context, a *Agent) error {
	old := legacyVolumeName(a)
	exists, err := r.Engine.VolumeExists(ctx, old)
	if err != nil || !exists {
		return nil
	}

	// Copied inside a container because the volumes live in the VM, not on
	// the host. Failure is reported and not fatal: the worst case is one
	// login, and refusing to run the agent over it would be worse.
	spec := container.RunSpec{
		Image:   "alpine",
		Remove:  true,
		Command: []string{"sh", "-c", "cp -a /from/. /to/ 2>/dev/null || true"},
		Mounts: []container.Mount{
			{Source: old, Target: "/from", Volume: true, ReadOnly: true},
			{Source: a.VolumeName(), Target: "/to", Volume: true},
		},
	}
	if _, err := r.Engine.Run(ctx, spec, nil, io.Discard, io.Discard); err != nil {
		fmt.Fprintf(r.Out, "⚠  could not carry %s over to %s: %v\n", old, a.VolumeName(), err)
		return nil
	}
	fmt.Fprintf(r.Out, "Carried the stored login from %s into %s.\n", old, a.VolumeName())
	fmt.Fprintf(r.Out, "  The old volume is left alone; remove it with:\n")
	fmt.Fprintf(r.Out, "    docker volume rm %s\n", old)
	return nil
}

// Logout removes the agent's home volume, discarding its stored login.
func (r *Runner) Logout(ctx context.Context, a *Agent) error {
	return r.Engine.VolumeRemove(ctx, a.VolumeName())
}
