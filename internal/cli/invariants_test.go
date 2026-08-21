package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/config"
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
	// Known, undecided, and recorded as such rather than passing silently.
	// `forward_ports` publishes a host port through the sidecar (127.0.0.1
	// only, and printed in the run header) with no key to accept it, while
	// `dev doctor` announces it as a requested grant. The same port list is
	// also filled by language detection, which is the tool's own doing and
	// nobody's request — so whether this is a request at all is the open
	// question. BACKLOG B28.
	undecided := map[string]bool{"ForwardPorts": true}

	fields := reflect.TypeOf(config.SecurityAsks{})
	for i := 0; i < fields.NumField(); i++ {
		name := fields.Field(i).Name
		if _, ok := asked[name]; ok {
			continue
		}
		if undecided[name] {
			continue
		}
		t.Errorf("config.SecurityAsks.%s grants the container something and no "+
			"consent key covers it; add one, or add it to the undecided list "+
			"with the reason", name)
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
