package cli

import (
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/trust"
)

const requestsSocket = "mount_docker_socket: true\n"

// The acceptance is keyed by project path, so remembering it hands the
// docker daemon to whatever occupies that path later — and the new
// repository asks for exactly the value already accepted, so nothing
// notices. `dev accept --all` is a sentence about the project in front of
// you; it must not also be that.
func TestAcceptWillNotQuietlyRememberTheDockerSocket(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, requestsSocket)

	err := h.run(t, "accept", "--all")
	if err == nil {
		t.Fatal("`dev accept --all` remembered the docker socket")
	}
	for _, want := range []string{"root on the docker host", "--allow-docker-socket", "--remember"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}

	store, err2 := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err2 != nil {
		t.Fatal(err2)
	}
	if got := store.AcceptedSettings()["mount_docker_socket"]; got != "" {
		t.Errorf("it was recorded anyway: %q", got)
	}
}

// Refusing has to leave a way through, or the setting is simply broken.
func TestRememberIsTheDeliberateWayIn(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, requestsSocket)

	if err := h.run(t, "accept", "mount_docker_socket", "--remember"); err != nil {
		t.Fatalf("accept --remember: %v\n%s", err, h.stderr.String())
	}
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.AcceptedSettings()["mount_docker_socket"]; got != "true" {
		t.Errorf("--remember recorded nothing: %q", got)
	}
}

// The other settings are unaffected: only the break-glass ones change.
func TestOrdinarySettingsAreStillRememberedByAcceptAll(t *testing.T) {
	h := newHarness(t)
	h.writeProject(t, "mount_git_config: true\n")

	if err := h.run(t, "accept", "--all"); err != nil {
		t.Fatalf("accept --all: %v\n%s", err, h.stderr.String())
	}
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.AcceptedSettings()["mount_git_config"]; got != "true" {
		t.Errorf("an ordinary setting stopped being remembered: %q", got)
	}
}

// A run stops on the request, and the hint that works for every other
// setting — "accept it" — is a dead end here, so the per-run way is named.
func TestABlockedRunNamesThePerRunFlag(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeProject(t, requestsSocket)

	err := h.run(t, "run", "--tty", "off", "-c", "true")
	if err == nil {
		t.Fatal("the run proceeded without the socket being authorized")
	}
	if !strings.Contains(h.stderr.String(), "--allow-docker-socket") {
		t.Errorf("the block does not name the per-run flag:\n%s", h.stderr.String())
	}
}

// The flag has to satisfy the consent check as well as the grant, or it
// would be unreachable: the run would stop for the very setting it permits.
func TestThePerRunFlagGetsPastConsent(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	h.writeProject(t, requestsSocket)

	if err := h.run(t, "run", "--tty", "off", "--allow-docker-socket", "-c", "true"); err != nil {
		t.Fatalf("the break-glass flag did not get past consent: %v\n%s", err, h.stderr.String())
	}
	// And it stays per-run: nothing is written down.
	store, err := trust.Load(h.paths.Home, h.paths.ProjectDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.AcceptedSettings()["mount_docker_socket"]; got != "" {
		t.Errorf("a per-run grant was recorded: %q", got)
	}
}

// Every command that can receive host grants needs the flag; one without it
// is a command where the setting has no per-run answer at all.
func TestEveryGrantingCommandHasTheFlag(t *testing.T) {
	h := newHarness(t)
	root := NewRootCmd(h.env)
	for _, name := range []string{"run", "shell", "console"} {
		c, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if c.Flags().Lookup("allow-docker-socket") == nil {
			t.Errorf("`dev %s` has no --allow-docker-socket", name)
		}
	}
}
