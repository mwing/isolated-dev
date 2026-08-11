package cli

import (
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/project"
	"github.com/mwing/isolated-dev/internal/trust"
)

// An agent acts on instructions from a model, so it does not inherit the
// human's decision to trust this project (ROADMAP 4.1). The grants a plain
// run may receive — a filtered gitconfig, the docker socket, host
// environment variables — must therefore never reach it, however thoroughly
// they were accepted.
//
// The invariant holds structurally today: the agent path never resolves
// those grants. That is exactly why it is worth asserting. Nothing fails if
// a future edit passes them through, and the failure would be host
// credentials in the one container that is never trusted, under a suite
// that stayed green.
func TestAnAgentRunGetsNoneOfTheHostGrants(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.statSocket("999")
	h.writeHostGitConfig(t, "[user]\n\tname = Real Person\n\temail = real@example.com\n")

	// Everything a project can ask for, requested and accepted, so the run
	// is as entitled as this tool can make it.
	h.writeProject(t, `mount_git_config: true
mount_docker_socket: true
pass_env_vars:
  explicit:
    - SECRET_TOKEN
`)
	h.acceptSettings(t,
		trust.Ask{Key: "mount_git_config", Value: "true"},
		trust.Ask{Key: "mount_docker_socket", Value: "true"},
		trust.Ask{Key: "pass_env_vars", Value: "SECRET_TOKEN"},
	)
	h.env.Env = append(h.env.Env, "SECRET_TOKEN=hunter2")

	if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
		t.Fatalf("agent run: %v", err)
	}

	argv := argv(h.workloadRun(t))
	for _, forbidden := range []string{
		project.SystemGitConfig,  // the filtered gitconfig mount
		project.DockerSocketPath, // root on the docker host
		"SECRET_TOKEN",           // a host environment variable
		"hunter2",                // and its value
		"Real Person",            // anything out of the host gitconfig
	} {
		if strings.Contains(argv, forbidden) {
			t.Fatalf("an agent run received %q, which is a host grant:\n%s", forbidden, argv)
		}
	}
}

// The same grants, on a plain run, do arrive — otherwise the test above
// would pass for the wrong reason: a resolver that grants nothing to
// anybody proves nothing about agents.
func TestAPlainRunDoesReceiveTheSameGrants(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.statSocket("999")
	h.writeHostGitConfig(t, "[user]\n\tname = Real Person\n\temail = real@example.com\n")
	h.writeProject(t, `mount_git_config: true
pass_env_vars:
  explicit:
    - SECRET_TOKEN
`)
	h.acceptSettings(t,
		trust.Ask{Key: "mount_git_config", Value: "true"},
		trust.Ask{Key: "pass_env_vars", Value: "SECRET_TOKEN"},
	)
	h.env.Env = append(h.env.Env, "SECRET_TOKEN=hunter2")

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("run: %v", err)
	}
	argv := argv(h.workloadRun(t))
	for _, want := range []string{project.SystemGitConfig, "SECRET_TOKEN"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("a plain run did not receive %q, so the agent assertion "+
				"proves nothing:\n%s", want, argv)
		}
	}
}
