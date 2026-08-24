package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/clone"
	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/runner"
)

// INVARIANT: a project request can never widen access without a user
// decision.
//
// Written over the set of settings rather than one test per setting. The
// failure this exists to catch is not a wrong effect string — it is a
// field added later whose consent wiring nobody remembered, which every
// existing test passes straight through because no test names a field that
// did not exist when it was written.
func TestInvariantEveryWeakeningProjectSettingIsAsked(t *testing.T) {
	// Each entry is a project file that weakens the sandbox, and the key
	// the user has to accept before it takes effect.
	for _, tc := range []struct{ body, key string }{
		{"network: open\n", "network"},
		{"agent_clone: false\n", "agent_clone"},
		{"mount_git_config: true\n", "mount_git_config"},
		{"mount_docker_socket: true\n", "mount_docker_socket"},
		{"tools:\n  - ripgrep\n", "tools"},
		{"pass_env_vars:\n  patterns:\n    - AWS_*\n", "pass_env_vars"},
	} {
		h := newHarness(t)
		h.writeProject(t, tc.body)
		cfg, err := config.Load(h.paths, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}
		var found bool
		for _, ask := range projectAsks(cfg, nil) {
			if ask.Key == tc.key {
				found = true
				if strings.TrimSpace(ask.Effect) == "" {
					t.Errorf("%s asks without saying what it does", tc.key)
				}
			}
		}
		if !found {
			t.Errorf("%q takes effect with no decision: %+v", tc.body, projectAsks(cfg, nil))
		}
	}
}

// And the other end of the same invariant: an unaccepted request stops the
// run rather than being applied, reported, or quietly dropped. One
// representative is enough here — the branch is shared, and the test above
// is what covers the set.
func TestInvariantAnUnacceptedRequestStopsTheRun(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeProject(t, "mount_docker_socket: true\n")

	if err := h.run(t, "run", "--tty", "off", "-c", "true"); err == nil {
		t.Fatal("a run proceeded with an unaccepted request")
	}
	for _, args := range h.dockerRuns() {
		if containsPair(args, "--volume", "/var/run/docker.sock:/var/run/docker.sock") {
			t.Fatalf("the socket was mounted anyway:\n%s", argv(args))
		}
	}
}

// The guard for the field that does not exist yet.
//
// config.SecurityAsks is the tool's own list of what a project can ask for
// that grants the container something. Every member of it has to be
// classified: either the consent path asks about it, or there is a stated
// reason it does not. A new field lands in neither bucket and fails here,
// which is the only way this invariant survives contact with future
// features.
func TestInvariantEverySecurityAskIsClassified(t *testing.T) {
	asked := map[string]string{
		"MountGitConfig":    "mount_git_config",
		"MountDockerSocket": "mount_docker_socket",
		"PassEnvPatterns":   "pass_env_vars",
		"PassEnvExplicit":   "pass_env_vars",
	}
	fields := reflect.TypeOf(config.SecurityAsks{})
	for i := 0; i < fields.NumField(); i++ {
		name := fields.Field(i).Name
		if _, ok := asked[name]; ok {
			continue
		}
		t.Errorf("config.SecurityAsks.%s grants the container something and no "+
			"consent key covers it; give it one, or take the request away as "+
			"forward_ports was", name)
	}

	// And the keys named above have to be the ones the consent path
	// actually uses, or this test drifts into fiction.
	h := newHarness(t)
	h.writeProject(t, "mount_git_config: true\nmount_docker_socket: true\n"+
		"pass_env_vars:\n  patterns:\n    - AWS_*\n  explicit:\n    - CI\n")
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{}
	for _, ask := range projectAsks(cfg, nil) {
		live[ask.Key] = true
	}
	for field, key := range asked {
		if !live[key] {
			t.Errorf("%s is mapped to consent key %q, which the consent path does "+
				"not produce", field, key)
		}
	}
}

// INVARIANT: two simultaneous runs can never write the same git working
// tree — asserted at the command layer, where the lock has to be taken
// before anything touches the clone rather than after it is ready.
//
// The lock existed and was taken last: after Prepare had created the
// clone, after a capture had fetched from it, after the config quarantine
// had renamed .git/config aside and back. A lock that starts once the race
// is over is a comment. `dev run --clone` and `dev shell --clone` took no
// lock at all, which made the serialization a property of `dev agent run`
// rather than of the clone.
func TestInvariantEveryRouteIntoACloneTakesTheLock(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)

	// Another run holds the clone.
	dest := clone.Dir(h.paths.Home, projectSlug(h.paths.ProjectDir))
	release, err := clone.Lock(dest)
	if err != nil {
		t.Fatalf("taking the clone: %v", err)
	}
	defer release()

	// The agent route.
	_, agentRelease, err := prepareCloneDir(t.Context(), h.env, h.paths.ProjectDir, 0, h.stderr)
	if err == nil {
		agentRelease()
		t.Error("an agent run took a clone another run is holding")
	} else if !strings.Contains(err.Error(), "in use") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}

	// And the `dev run --clone` route, which had no lock at all.
	cfg, p, err := resolveProject(h.env)
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg
	spec := container.RunSpec{}
	runRelease, err := useClone(t.Context(), h.env, p, &spec, 0)
	if err == nil {
		runRelease()
		t.Error("`dev run --clone` took a clone another run is holding")
	}
}

// And the lock is not merely taken but taken first: preparing a clone
// renames its config aside and back, which is not a thing two runs may do
// at once.
func TestTheLockIsTakenBeforeTheCloneIsTouched(t *testing.T) {
	h := newHarness(t)
	h.env.Runner = runner.New(false)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)

	dest := clone.Dir(h.paths.Home, projectSlug(h.paths.ProjectDir))
	release, err := clone.Lock(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if _, r, err := prepareCloneDir(t.Context(), h.env, h.paths.ProjectDir, 0, h.stderr); err == nil {
		r()
		t.Fatal("the run proceeded while another held the clone")
	}
	// Nothing was created: the refusal came before any work on the clone.
	if _, statErr := os.Stat(filepath.Join(dest, ".git")); statErr == nil {
		t.Error("a clone was created despite the lock being held elsewhere")
	}
}

// The other way to satisfy the invariant, and the one `forward_ports`
// took: not a consent key, but no request at all.
//
// Publishing a port opens a socket on the machine — reachable by anything
// else on it, including a browser — and the sidecar does the publishing
// because the workload is on an internal network. From the user's own
// config that is them configuring their machine. From a project file it
// was a request with no key, which `dev doctor` announced as a grant and
// no `dev accept` could weigh.
func TestInvariantAProjectCannotPublishAHostPort(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, "forward_ports: \"9999\"\n")
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ForwardPorts != "" {
		t.Errorf("a project file published a host port: %q", cfg.ForwardPorts)
	}
	// And it is not dropped in silence: a config half-honored quietly is
	// worse than one not read at all.
	var said bool
	for _, n := range cfg.Notes {
		if strings.Contains(n.String(), "forward_ports") {
			said = true
		}
	}
	if !said {
		t.Errorf("the setting was ignored without saying so: %v", cfg.Notes)
	}
}

// The user's own config still works, or the change has removed the
// feature rather than moved the decision.
func TestForwardPortsFromYourOwnConfigStillApplies(t *testing.T) {
	h := newHarness(t)
	h.writeGlobal(t, "forward_ports: \"9999\"\n")
	cfg, err := config.Load(h.paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ForwardPorts != "9999" {
		t.Errorf("forward_ports from the global config = %q", cfg.ForwardPorts)
	}
}
