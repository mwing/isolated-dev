package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/trust"
)

// projectAsks returns the security-relevant settings this project's own
// .devenv.yaml requested.
//
// Only values that came from the PROJECT file count. The global file is
// the user's own machine and needs no consent from them, and a default is
// nobody's request. This is the whole reason config tracks provenance.
func projectAsks(cfg config.Config, p *project.Project) []trust.Ask {
	var asks []trust.Ask

	// The build source is a request too: a repository supplying its own
	// Dockerfile is asking for an unfiltered build of instructions it wrote.
	if ask := buildSourceAsk(p); ask != nil {
		asks = append(asks, *ask)
	}

	if cfg.Origin("network") == config.OriginProject && cfg.Network == "open" {
		asks = append(asks, trust.Ask{
			Key: "network", Value: "open",
			Effect: "turn OFF egress filtering: the container reaches any host, " +
				"and nothing is reported as blocked because nothing is blocked",
		})
	}
	// Only the direction that weakens protection is a request. A project
	// asking for agent_clone: true is asking for the default it already
	// gets, and prompting for that would train people to accept prompts.
	if cfg.Origin("agent_clone") == config.OriginProject && !cfg.AgentClone {
		asks = append(asks, trust.Ask{
			Key: "agent_clone", Value: "false",
			Effect: "run agents directly in your working tree instead of a private " +
				"clone, so an agent edits the files you are editing — including " +
				"git hooks, npm scripts and Makefiles, which your host runs later",
		})
	}
	if cfg.Origin("mount_git_config") == config.OriginProject && cfg.MountGitConfig {
		asks = append(asks, trust.Ask{
			Key: "mount_git_config", Value: "true",
			Effect: "mount a filtered copy of ~/.gitconfig as the container's " +
				"git configuration; signing, credential helpers and insteadOf " +
				"rules that redirect remotes are removed from the copy",
		})
	}
	if cfg.Origin("mount_docker_socket") == config.OriginProject && cfg.MountDockerSocket {
		asks = append(asks, trust.Ask{
			Key: "mount_docker_socket", Value: "true",
			Effect: "mount the docker socket, which is root on the docker host",
		})
	}
	if cfg.Origin("tools") == config.OriginProject && len(cfg.Tools) > 0 {
		asks = append(asks, trust.Ask{
			Key: "tools", Value: strings.Join(cfg.Tools, " "),
			Effect: "install these packages into the project image: " +
				strings.Join(cfg.Tools, ", ") + " (installed during a build, " +
				"which runs unfiltered)",
		})
	}
	if cfg.Origin("pass_env_vars") == config.OriginProject && !cfg.PassEnvVars.Empty() {
		v := strings.Join(append(append([]string(nil), cfg.PassEnvVars.Patterns...),
			cfg.PassEnvVars.Explicit...), " ")
		asks = append(asks, trust.Ask{
			Key: "pass_env_vars", Value: v,
			Effect: "copy matching host environment variables into the container: " + v,
		})
	}
	return asks
}

// agentAsks are the settings an agent run actually honors.
//
// A subset rather than all of them, because an agent receives none of the
// host grants: asking someone to accept a gitconfig mount before starting
// an agent that will never see it is a prompt about nothing.
func agentAsks(cfg config.Config, p *project.Project) []trust.Ask {
	var out []trust.Ask
	for _, ask := range projectAsks(cfg, p) {
		switch ask.Key {
		case "agent_clone", "build_source":
			out = append(out, ask)
		}
	}
	return out
}

// enforceConsent stops a run when the project asks for something the user
// has not accepted. A project file is a request; running it is not consent.
//
// run carries what a flag on this invocation authorizes outright. Without
// that the break-glass flag would be unreachable: the run would stop for
// the very setting the flag exists to permit.
func enforceConsent(env *Env, cfg config.Config, p *project.Project, store *trust.Store,
	run runGrants) error {
	pol, err := loadPolicy(env)
	if err != nil {
		return err
	}
	// Policy outranks acceptance. Something already accepted can become
	// forbidden later, and a rule that only applied to new decisions would
	// leave the machines that most need it untouched.
	for _, ask := range projectAsks(cfg, p) {
		if verr := pol.CheckSetting(ask.Key); verr != nil {
			return fmt.Errorf("%s requests %s, but %w", env.Paths.Project, ask.Key, verr)
		}
	}
	if err := pol.CheckNetwork(cfg.Network); err != nil {
		return err
	}

	var pending []trust.Ask
	for _, a := range store.PendingSettings(projectAsks(cfg, p)) {
		if run.authorizes(a.Key) {
			continue
		}
		pending = append(pending, a)
	}
	if len(pending) == 0 {
		return nil
	}
	fmt.Fprintf(env.Stderr, "%s requests settings you have not accepted:\n\n", env.Paths.Project)
	for _, a := range pending {
		fmt.Fprintf(env.Stderr, "  %s: %s\n", a.Key, a.Value)
		fmt.Fprintf(env.Stderr, "      %s\n", a.Effect)
	}
	fmt.Fprintf(env.Stderr, "\nReview and accept:  dev accept\n")
	// The break-glass keys have no "accept once and forget" answer, so the
	// hint that works for everything else would be a dead end for them.
	for _, a := range pending {
		if breakGlass[a.Key] {
			fmt.Fprintf(env.Stderr, "For this run only:   %s\n", breakGlassFlag(a.Key))
		}
	}
	fmt.Fprintf(env.Stderr, "Or ignore the file:  --network allowlist\n")
	return fmt.Errorf("unaccepted project settings")
}

// breakGlassFlag names the per-run flag that authorizes a break-glass key.
func breakGlassFlag(key string) string {
	if key == "mount_docker_socket" {
		return "dev run --allow-docker-socket"
	}
	return "dev accept " + key + " --remember"
}

func newAcceptCmd(env *Env) *cobra.Command {
	var all bool
	var agentName string
	var remember bool
	cmd := &cobra.Command{
		Use:   "accept [name...]",
		Short: "Review and accept what this project's .devenv.yaml requests",
		Long: "A project file states what the project needs. It is a request,\n" +
			"not a grant: running the project is not consent. Decisions are\n" +
			"recorded under ~/.dev-envs, never in the repository, so a clone\n" +
			"cannot bring its own approval with it.\n\n" +
			"Settings and network destinations are separate decisions, recorded\n" +
			"separately. They are shown together because they arrive together:\n" +
			"the project asked for both in one file, and reading half of what a\n" +
			"stranger's repository wants is not reviewing it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccept(cmd.Context(), env, args, all, agentName, remember)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "accept everything requested")
	cmd.Flags().StringVar(&agentName, "agent", "default",
		"agent whose requested destinations to review")
	cmd.Flags().BoolVar(&remember, "remember", false,
		"record a break-glass setting for every future run, not just this one")
	return cmd
}

// runAccept presents everything the project's file requests — settings and
// egress destinations — and records what the user names.
//
// One workflow, still two decisions: settings and hosts remain separate
// objects, checked against different policy rules and written to different
// parts of the record. What was merged is the review, because the split was
// never the user's: they cloned one repository and it asked for both.
func runAccept(_ context.Context, env *Env, names []string, all bool, agentName string,
	remember bool) error {
	// Resolved rather than just loaded: the build source is one of the
	// things being accepted, and knowing what it is needs detection.
	cfg, p, err := resolveProject(env)
	if err != nil {
		return err
	}
	store, err := trust.Load(env.Paths.Home, env.Paths.ProjectDir)
	if err != nil {
		return err
	}

	settings := store.PendingSettings(projectAsks(cfg, p))
	hosts := store.Pending(agentName, projectRequest(cfg, agentName))
	if len(settings) == 0 && len(hosts) == 0 {
		fmt.Fprintln(env.Stdout, "Nothing pending: this project requests nothing you have not accepted.")
		return nil
	}

	pol, err := loadPolicy(env)
	if err != nil {
		return err
	}

	if !all && len(names) == 0 {
		showPending(env, pol, settings, hosts, agentName)
		return nil
	}

	wantSettings, wantHosts := settings, hosts
	if !all {
		wantSettings, wantHosts, err = selectPending(names, settings, hosts)
		if err != nil {
			return err
		}
	}

	// Policy first, and for everything named, before anything is written.
	// Accepting half a request and then refusing the rest leaves a record
	// the user did not choose and would have to unpick.
	for _, a := range wantSettings {
		if verr := pol.CheckSetting(a.Key); verr != nil {
			return fmt.Errorf("%s requests %s, but %w", env.Paths.Project, a.Key, verr)
		}
	}
	// A break-glass key is not remembered unless someone says so in as many
	// words. `dev accept --all` is a sentence about the project in front of
	// you; it must not also be the sentence that hands the docker daemon to
	// whatever occupies this path next year.
	if !remember {
		for _, a := range wantSettings {
			if breakGlass[a.Key] {
				return fmt.Errorf(
					"%s is root on the docker host, so it is not remembered by default.\n"+
						"  For one run:      %s\n"+
						"  To remember it:   dev accept %s --remember",
					a.Key, breakGlassFlag(a.Key), a.Key)
			}
		}
	}
	// Accepting is a route in like any other: a project asks, the user says
	// yes, and the destination is granted from then on. Without this the
	// deny list was walked around by writing the host into a .devenv.yaml
	// and accepting it.
	if err := checkHosts(env, wantHosts); err != nil {
		return err
	}

	addedSettings, err := store.AcceptSettings(wantSettings)
	if err != nil {
		return err
	}
	addedHosts, err := store.Accept(agentName, wantHosts)
	if err != nil {
		return err
	}
	if len(addedSettings) == 0 && len(addedHosts) == 0 {
		fmt.Fprintln(env.Stdout, "Nothing new accepted.")
		return nil
	}
	fmt.Fprintln(env.Stdout, "Accepted:")
	for _, a := range addedSettings {
		fmt.Fprintf(env.Stdout, "  + %s: %s\n", a.Key, a.Value)
	}
	for _, h := range addedHosts {
		fmt.Fprintf(env.Stdout, "  + %s\n", h)
	}
	fmt.Fprintf(env.Stdout, "\nRecorded in %s\n", store.Project.Path())
	explainNonHTTP(env.Stdout, addedHosts)
	return nil
}

// showPending prints what is outstanding without recording anything.
//
// A destination that policy forbids is marked here rather than only on the
// way out: offering something for acceptance and refusing it afterwards is
// the prompt that teaches people prompts do not mean much.
func showPending(env *Env, pol policyChecker, settings []trust.Ask, hosts []string, agentName string) {
	fmt.Fprintf(env.Stdout, "%s requests:\n\n", env.Paths.Project)

	if len(settings) > 0 {
		fmt.Fprintln(env.Stdout, "Settings")
		for _, a := range settings {
			fmt.Fprintf(env.Stdout, "  %s: %s\n", a.Key, a.Value)
			if verr := pol.CheckSetting(a.Key); verr != nil {
				fmt.Fprintf(env.Stdout, "      cannot be accepted: %v\n\n", verr)
				continue
			}
			fmt.Fprintf(env.Stdout, "      %s\n\n", a.Effect)
		}
	}

	if len(hosts) > 0 {
		fmt.Fprintln(env.Stdout, "Network")
		for _, h := range hosts {
			if verr := pol.CheckHost(h); verr != nil {
				fmt.Fprintf(env.Stdout, "  %s  (cannot be accepted: %v)\n", h, verr)
				continue
			}
			fmt.Fprintf(env.Stdout, "  %s\n", h)
		}
		fmt.Fprintf(env.Stdout, "\n      Each one is a destination the container may reach, and any\n")
		fmt.Fprintf(env.Stdout, "      host that accepts writes is a place data can go.\n\n")
	}

	fmt.Fprintf(env.Stdout, "Accept all:  dev accept --all%s\n", agentSuffix(agentName))
	fmt.Fprintf(env.Stdout, "Accept one:  dev accept %s\n", firstPendingName(settings, hosts))
}

// policyChecker is the part of the policy showPending needs, named so the
// display can be tested without building one.
type policyChecker interface {
	CheckSetting(key string) error
	CheckHost(host string) error
}

// agentSuffix names the agent in a suggested command, and says nothing when
// the agent is the default one — a flag that repeats the default is noise
// in the one line the user is meant to copy.
func agentSuffix(agentName string) string {
	if agentName == "" || agentName == "default" {
		return ""
	}
	return " --agent " + agentName
}

func firstPendingName(settings []trust.Ask, hosts []string) string {
	if len(settings) > 0 {
		return settings[0].Key
	}
	return hosts[0]
}

// selectPending resolves the names a user typed against what is pending.
//
// A name is a setting key or a destination, and the two cannot collide: a
// setting key is a fixed identifier and a destination is a hostname. An
// unmatched name is an error rather than a silent no-op, because the shape
// of this command is "accept exactly this" and quietly accepting nothing
// looks identical to success.
func selectPending(names []string, settings []trust.Ask, hosts []string) ([]trust.Ask, []string, error) {
	var wantSettings []trust.Ask
	var wantHosts []string
	var unknown []string

	for _, name := range names {
		matched := false
		for _, a := range settings {
			if a.Key == name {
				wantSettings = append(wantSettings, a)
				matched = true
			}
		}
		for _, h := range hosts {
			if h == name {
				wantHosts = append(wantHosts, h)
				matched = true
			}
		}
		if !matched {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return nil, nil, fmt.Errorf("not pending: %s", strings.Join(unknown, ", "))
	}
	return wantSettings, wantHosts, nil
}
