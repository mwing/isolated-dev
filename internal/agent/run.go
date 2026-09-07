package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/project"
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
	// Pins map an image reference to the digest it was pinned to. The
	// overlay is built FROM upstream images like any other Dockerfile, so
	// it honours the project's pins rather than being the one build the
	// tool exempts from its own rule.
	Pins map[string]string
	// Memory and CPUs bound the container.
	Memory string
	CPUs   string
	// AllowMCP turns the agent's MCP connectors back on for this run: its
	// MCPHosts are added to the allowlist and its MCPOffArgs are not passed.
	// Off by default, because a connector reaches an account outside the
	// sandbox with a live token, which is the reach the sandbox withholds.
	AllowMCP bool
}

// Allowlist is the effective policy for a run: the agent's defaults plus
// anything the user allowed for this invocation.
func (o Options) Allowlist() []string {
	out := append([]string(nil), o.Agent.AllowHosts...)
	out = append(out, o.ExtraHosts...)
	// The connector hosts join only when the run asked for MCP. But this is
	// not the whole gate: the final allowlist is assembled from more than
	// this in both run paths — a `dev allow`, a `dev accept` of a project
	// request, a `--allow-host` — so the guarantee is enforced by GateMCP
	// over the finished set, not here.
	if o.AllowMCP {
		out = append(out, o.Agent.MCPHosts...)
	}
	return out
}

// The connector hosts are gated out of the final allowlist unless a run
// enabled MCP — but that gate lives in the CLI glue layer, not here,
// because the final allowlist is assembled there from grants, project
// accepts and flags, and it has to match by the sidecar's own rules so a
// port suffix or a wildcard cannot slip a connector host past a string
// compare. See gateConnectorHosts in internal/cli.

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
	// The config volume mounts here, so the directory has to exist and
	// belong to the run's account: a mount point docker creates itself is
	// created owned by root, and an agent that cannot write its own config
	// directory completes the OAuth exchange, says it logged in, and has
	// nowhere to put the credential. Last, after the install: the install
	// may create it as root.
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
		// The agent's config directory is a per-project named volume, so
		// settings, hooks and MCP state do not cross between projects. The
		// login is shared separately (AuthVolume), copied in before the run
		// and back after; nothing here mounts it, so a hook one project
		// wrote is never seen by another.
		{Source: a.ConfigVolume(o.Project), Target: a.ConfigDir, Volume: true},
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
	// Told where its config directory is, an agent keeps the state it means
	// to keep — the credential, the onboarding marker — in the one place
	// that persists. Without this an agent that writes beside its config
	// directory rather than inside it would ask to log in again every run.
	if a.ConfigEnv != "" {
		spec.Env = append(spec.Env, a.ConfigEnv+"="+a.ConfigDir)
	}

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
	// The one thing this actually closes is a hostile repository's own MCP
	// server, shipped in a `.mcp.json` the clone carries: --strict-mcp-config
	// makes the agent ignore local MCP config. It does NOT disable a cloud
	// connector (Gmail, Linear) — those are fetched server-side and connect
	// on their own, so the connector threat is carried by the egress block
	// alone, see Options.GateMCP. Not defence in depth for that; a separate
	// defence for a separate vector.
	if !o.AllowMCP {
		spec.Command = append(spec.Command, a.MCPOffArgs...)
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

// SourceLabel records the instructions an overlay was built from, so a
// later run can tell whether the tag still means what it says.
//
// The tag is a function of the agent's name, base, version and uid — not of
// its pins, its install command or anything else in the file. So a pin
// recorded after the image was built never reached it: `dev pin` said to
// use --rebuild, and an instruction to remember a flag is not a mechanism.
// The project image carries the same label for the same reason.
const SourceLabel = "dev.image.source"

// overlayDockerfile is the exact text a build would run, so the staleness
// check and the build cannot disagree.
func overlayDockerfile(o Options) string {
	// The overlay is a build like any other, so it gets the same treatment
	// the tool asks of every project: a tag says which image you meant, a
	// digest says which image you got.
	return project.ApplyPins(Dockerfile(o.Agent, o.BaseImage()), o.Pins)
}

// sourceMarker identifies what an overlay was built from: its
// instructions, and the image they were built on top of.
//
// The base has to be in it. The overlay's instructions name the base by
// tag, so rebuilding the project image under the same tag leaves the text
// identical while the thing underneath has changed — and the overlay would
// have read as current forever, running the agent on a base the project
// replaced. A tag says what you meant, an id says what you got, which is
// the argument this tool already makes about every other image.
//
// An empty baseID means the base is not here yet, which happens once on a
// machine that has never pulled it: the build pulls it, and the next run
// computes a different marker and rebuilds once against a warm layer
// cache. One extra rebuild, ever, in exchange for never running on the
// wrong base.
func sourceMarker(dockerfile, baseID string) string {
	sum := sha256.Sum256([]byte(dockerfile + "\x00" + baseID))
	return hex.EncodeToString(sum[:8])
}

// EnsureImage builds the overlay image if it is missing or was built from
// different instructions.
func (r *Runner) EnsureImage(ctx context.Context, o Options, force bool) (string, error) {
	tag := o.Agent.ImageTag(o.BaseImage())
	df := overlayDockerfile(o)
	baseID, err := r.Engine.ImageID(ctx, o.BaseImage())
	if err != nil {
		return "", err
	}
	marker := sourceMarker(df, baseID)
	if !force {
		exists, err := r.Engine.ImageExists(ctx, tag)
		if err != nil {
			return "", err
		}
		if exists {
			got, lerr := r.Engine.ImageLabel(ctx, tag, SourceLabel)
			if lerr != nil {
				return "", lerr
			}
			if got == marker {
				return tag, nil
			}
			fmt.Fprintf(r.Out, "Rebuilding %s: it was built from different "+
				"instructions than this run needs.\n", tag)
		}
	}

	fmt.Fprintf(r.Out, "Building agent image %s...\n", tag)
	// Context "-" with the Dockerfile on stdin and no --file: the overlay
	// adds only the agent, so it needs no build context at all. Passing
	// --file - as well is what docker rejects.
	err = r.Engine.Build(ctx, container.BuildSpec{
		Tag:       tag,
		Context:   "-",
		Labels:    map[string]string{SourceLabel: marker},
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

// EnsureVolume prepares the two volumes a run needs: the shared auth
// volume holding the login, and this project's own config volume.
//
// The login is copied from the auth volume into the project's config
// volume so the agent starts authenticated; SyncAuthBack copies a refreshed
// login back afterward. A fresh install has neither volume and the first
// run logs in; an upgrade from the single shared-config volume seeds the
// auth volume from its credential once, so the login carries over while its
// settings and hooks — the crossing this split closes — do not.
func (r *Runner) EnsureVolume(ctx context.Context, a *Agent, projectDir string) error {
	if err := r.ensureAuthVolume(ctx, a); err != nil {
		return err
	}

	configVol := a.ConfigVolume(projectDir)
	exists, err := r.Engine.VolumeExists(ctx, configVol)
	if err != nil {
		return err
	}
	if exists {
		if err := r.repairVolumeOwner(ctx, a, configVol); err != nil {
			return err
		}
	} else if err := r.Engine.VolumeCreate(ctx, configVol); err != nil {
		return err
	}
	// Seed the login into this project's config from the shared auth volume.
	// Done every run, not only on creation: the auth volume is the source of
	// truth for the credential, and another project's run may have refreshed
	// it since this project last ran.
	return r.seedConfigFromAuth(ctx, a, configVol)
}

// seedConfigFromAuth copies the shared login into a project's config at the
// start of a run.
//
// Three properties, each learned from a review:
//   - It never errors on a missing login. A fresh install has an empty auth
//     volume, and the first run has to reach the container to log in — an
//     error here would deadlock: no run because no login, no login because
//     no run.
//   - It copies only when the shared login is strictly newer than the
//     project's, or the project has none. Copying unconditionally would
//     overwrite a token this project just refreshed but has not synced back
//     yet, losing the refresh.
//   - It holds the same lock SyncAuthBack takes, so a copy-in and another
//     project's copy-back cannot interleave and read a half-written file.
func (r *Runner) seedConfigFromAuth(ctx context.Context, a *Agent, configVol string) error {
	cred := a.CredentialFile()
	script := fmt.Sprintf(
		"[ -f /auth/%s ] || exit 0; "+
			"flock /auth/.synclock sh -c '"+
			"if [ /auth/%s -nt /config/%s ] || [ ! -f /config/%s ]; then "+
			"cp -p /auth/%s /config/%s && chown %d:%d /config/%s; fi'",
		cred, cred, cred, cred, cred, cred,
		container.HostUID(), container.HostGID(), cred)
	spec := container.RunSpec{
		Image:   "alpine",
		Remove:  true,
		User:    "0:0",
		Command: []string{"sh", "-c", script},
		Mounts: []container.Mount{
			{Source: a.AuthVolume(), Target: "/auth", Volume: true},
			{Source: configVol, Target: "/config", Volume: true},
		},
	}
	if _, err := r.Engine.Run(ctx, spec, nil, io.Discard, io.Discard); err != nil {
		// Not fatal: the worst case is a login the run performs itself.
		fmt.Fprintf(r.Out, "⚠  could not seed the shared login into this project: %v\n", err)
	}
	return nil
}

// ensureAuthVolume makes sure the shared auth volume exists, seeding it from
// a pre-split config volume the first time so an upgrade does not force a
// re-login.
func (r *Runner) ensureAuthVolume(ctx context.Context, a *Agent) error {
	exists, err := r.Engine.VolumeExists(ctx, a.AuthVolume())
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := r.Engine.VolumeCreate(ctx, a.AuthVolume()); err != nil {
		return err
	}
	// Carry the login out of whichever pre-split volume has one — the
	// single shared config, or an older home volume. Only the credential:
	// the settings and hooks beside it are exactly what this split stops
	// sharing. A fresh install has none of these, and the first run logs in.
	for _, old := range append([]string{sharedConfigVolume(a)}, legacyHomeVolumes(a)...) {
		has, verr := r.Engine.VolumeExists(ctx, old)
		if verr != nil || !has {
			continue
		}
		if err := r.copyCredential(ctx, a, old, a.AuthVolume(), false); err == nil {
			fmt.Fprintf(r.Out, "Carried the %s login into a shared login volume; "+
				"its per-project settings now start fresh in each project.\n", a.Name)
			return nil
		}
	}
	return nil
}

// legacyHomeVolumes are the volumes this one replaced, newest first.
func legacyHomeVolumes(a *Agent) []string {
	return []string{homeVolumeName(a), legacyVolumeName(a)}
}

// legacyVolumeName is what a volume was called before the tool was renamed.
func legacyVolumeName(a *Agent) string { return "dev2-agent-" + a.Name }

// copyCredential copies the login file between two volumes, chowning it to
// the run's account when it lands in a config volume (toConfig). The source
// path is relative to how the volume stored it: a config or auth volume
// keeps the file at its top level; a pre-split home volume keeps it under
// the config subdirectory.
//
// Best-effort: a missing source is not an error (nothing to carry, or a
// first login still to come), and only a genuine copy failure is reported.
// Used only to seed the auth volume once from a pre-split volume; the
// per-run copy-in is seedConfigFromAuth, which is conditional and locked.
func (r *Runner) copyCredential(ctx context.Context, a *Agent, from, to string, toConfig bool) error {
	cred := a.CredentialFile()
	fromPath := "/from/" + cred
	// A pre-split home volume held the credential under the config dir.
	if from == homeVolumeName(a) || from == legacyVolumeName(a) {
		fromPath = "/from/" + strings.TrimPrefix(a.ConfigDir, HomePath+"/") + "/" + cred
	}
	chown := ""
	if toConfig {
		chown = fmt.Sprintf(" && chown %d:%d /to/%s",
			container.HostUID(), container.HostGID(), cred)
	}
	spec := container.RunSpec{
		Image:  "alpine",
		Remove: true,
		User:   "0:0",
		Command: []string{"sh", "-c", fmt.Sprintf(
			"if [ ! -f %s ]; then exit 3; fi; cp -p %s /to/%s%s",
			fromPath, fromPath, cred, chown)},
		Mounts: []container.Mount{
			{Source: from, Target: "/from", Volume: true, ReadOnly: true},
			{Source: to, Target: "/to", Volume: true},
		},
	}
	res, err := r.Engine.Run(ctx, spec, nil, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode == 3 {
		return errNoCredential
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("copying the login: exit %d", res.ExitCode)
	}
	return nil
}

var errNoCredential = fmt.Errorf("no login to copy")

// SyncAuthBack copies a refreshed login from this project's config volume
// back to the shared auth volume, so a login or token refresh in one
// project reaches the others. Only when the config copy is newer, and under
// a lock on the auth volume so two projects finishing at once serialize
// rather than clobbering.
//
// Best-effort and quiet: the run has already happened, and a failure here
// costs at most one extra login later, never the run.
func (r *Runner) SyncAuthBack(ctx context.Context, a *Agent, projectDir string) {
	cred := a.CredentialFile()
	// -nt is true when /config's credential is strictly newer, or when the
	// auth volume has none yet (a first login). flock serializes writers.
	script := fmt.Sprintf(
		"[ -f /config/%s ] || exit 0; "+
			"flock /auth/.synclock sh -c '"+
			"if [ /config/%s -nt /auth/%s ] || [ ! -f /auth/%s ]; then "+
			"cp -p /config/%s /auth/%s; fi'",
		cred, cred, cred, cred, cred, cred)
	spec := container.RunSpec{
		Image:   "alpine",
		Remove:  true,
		User:    "0:0",
		Command: []string{"sh", "-c", script},
		Mounts: []container.Mount{
			{Source: a.ConfigVolume(projectDir), Target: "/config", Volume: true, ReadOnly: true},
			{Source: a.AuthVolume(), Target: "/auth", Volume: true},
		},
	}
	if _, err := r.Engine.Run(ctx, spec, nil, io.Discard, io.Discard); err != nil {
		fmt.Fprintf(r.Out, "⚠  could not sync the login back to the shared volume: %v\n", err)
	}
}

// repairVolumeOwner makes an existing config volume belong to the uid the
// agent now runs as.
//
// The volume outlives the image, and docker only seeds one when it is
// created — an existing volume keeps whatever ownership it was populated
// with. So when runs stopped being a fixed uid 1000 and became the host's,
// every already-logged-in agent found a config directory it could not write:
// the OAuth exchange succeeded, "Logged in as ..." was printed, and the
// credential could not be saved, so the next command was logged out again.
// A failure that reports success is the worst shape available, and this is
// the migration that stops it.
//
// Checked before changing anything, because doing it every run would be a
// chown nobody asked for.
func (r *Runner) repairVolumeOwner(ctx context.Context, a *Agent, configVol string) error {
	want := fmt.Sprintf("%d:%d", container.HostUID(), container.HostGID())
	target := a.ConfigDir

	var out bytes.Buffer
	probe := container.RunSpec{
		Image:   "alpine",
		Remove:  true,
		User:    "0:0",
		Command: []string{"stat", "-c", "%u:%g", target},
		Mounts: []container.Mount{
			{Source: configVol, Target: target, Volume: true},
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
	fix.Command = []string{"chown", "-R", want, target}
	if _, err := r.Engine.Run(ctx, fix, nil, io.Discard, io.Discard); err != nil {
		fmt.Fprintf(r.Out, "⚠  %s belongs to %s and this run is %s, so the agent may not be\n"+
			"   able to save its login. Repair it with:\n"+
			"     docker run --rm -u 0 -v %s:%s alpine chown -R %s %s\n",
			configVol, strings.TrimSpace(out.String()), want,
			configVol, target, want, target)
		return nil
	}
	fmt.Fprintf(r.Out, "Adjusted %s to uid %s, which this run uses.\n", configVol, want)
	return nil
}

// Logout discards the agent's stored login: the config volume, and every
// volume this one was migrated out of.
//
// The legacy volumes are the point. They are kept after a migration so the
// user can satisfy themselves before deleting them, and EnsureVolume copies
// a login out of them whenever the config volume is missing — which is
// exactly the state logout leaves behind. So logout, then run, and the
// credential came back: a logout that logs nobody out.
//
// Removing them is the honest reading of the command. What they hold is a
// superseded copy of the thing being discarded, and a home directory this
// version no longer keeps; leaving either behind after "discard my login"
// would be the tool deciding it knows better.
func (r *Runner) Logout(ctx context.Context, a *Agent) error {
	// The shared login, and every project's config volume: logout discards
	// the agent's state, and its state is now spread across one volume per
	// project. Found by prefix, since the projects are not known ahead of
	// time. The pre-split and legacy volumes go too, so nothing is left
	// holding an old copy of the login.
	if err := r.Engine.VolumeRemove(ctx, a.AuthVolume()); err != nil {
		return err
	}
	listed, err := r.Engine.VolumeList(ctx, a.configVolumePrefix())
	if err != nil {
		return err
	}
	// The prefix `dev-agent-<name>-` also matches another agent whose name
	// begins with this one — `claude` matches `claude-pro` — since agent
	// names may contain hyphens. So each listed volume is confirmed to be
	// this agent's own config before removal.
	var configVols []string
	for _, v := range listed {
		if a.isOwnConfigVolume(v) {
			configVols = append(configVols, v)
		}
	}
	remove := append(configVols, sharedConfigVolume(a))
	remove = append(remove, legacyHomeVolumes(a)...)
	for _, v := range remove {
		if v == a.AuthVolume() {
			continue // already removed
		}
		exists, verr := r.Engine.VolumeExists(ctx, v)
		if verr != nil || !exists {
			continue
		}
		if rerr := r.Engine.VolumeRemove(ctx, v); rerr != nil {
			fmt.Fprintf(r.Out, "⚠  %s could not be removed: %v\n   Remove it with: "+
				"docker volume rm %s\n", v, rerr, v)
		}
	}
	return nil
}
