package container

import "testing"

// The hardening is a promise made in one place — Hardened() — and every
// sandboxed run is built from it. These assert the promise directly, so a
// change that loosens it fails here rather than in a probe session someone
// happens to run months later.
//
// Deterministic and daemon-free: this is the floor. The live version, which
// asks the kernel whether the flags actually took, is in the integration
// suite.

// forbiddenCaps are the capabilities whose presence would hand a container
// a known escape or a way around the egress boundary. Listed by the thing
// they grant, because "why is this dangerous" is the question a future
// reader will have when they are tempted to add one.
var forbiddenCaps = map[string]string{
	"SYS_ADMIN":       "mount, and the cgroup release_agent escape",
	"SYS_PTRACE":      "inspect and inject into processes outside the container",
	"SYS_MODULE":      "load a kernel module, i.e. become the kernel",
	"SYS_RAWIO":       "raw device I/O",
	"SYS_BOOT":        "reboot the host",
	"DAC_READ_SEARCH": "open_by_handle_at, the shocker escape, reads any host file",
	"NET_ADMIN":       "rewrite routing and firewall rules, i.e. around the egress proxy",
	"NET_RAW":         "craft raw packets, e.g. to spoof past the sidecar",
	"MKNOD":           "create device nodes",
}

func TestHardenedDropsAllCapabilities(t *testing.T) {
	h := Hardened()
	if len(h.CapDrop) != 1 || h.CapDrop[0] != "ALL" {
		t.Fatalf("CapDrop = %v, want [ALL] — the baseline is no capabilities, "+
			"and everything kept is added back by name", h.CapDrop)
	}
}

// The added-back set must stay small and boring. Each of these is needed
// only to set up the run's own account and file ownership; none of them is
// on the escape surface. A future addition from the forbidden list fails
// here with the reason attached.
func TestHardenedAddsBackOnlyHarmlessCapabilities(t *testing.T) {
	allowed := map[string]bool{
		"CHOWN": true, "DAC_OVERRIDE": true, "SETGID": true, "SETUID": true,
	}
	// Ranging over an empty slice would pass this test while saying nothing.
	// The four are load-bearing — an account and its file ownership — so
	// their absence is itself a regression worth catching here.
	if len(Hardened().CapAdd) == 0 {
		t.Fatal("CapAdd is empty; the run cannot create its account or own its files")
	}
	for _, c := range Hardened().CapAdd {
		if why, forbidden := forbiddenCaps[c]; forbidden {
			t.Errorf("Hardened adds %s, which grants: %s", c, why)
		}
		if !allowed[c] {
			t.Errorf("Hardened adds %s, which is not in the small known-harmless "+
				"set; if it is genuinely needed, add it to the allowed set here "+
				"with the reason", c)
		}
	}
}

func TestHardenedForbidsPrivilegeEscalation(t *testing.T) {
	var found bool
	for _, o := range Hardened().SecurityOpt {
		if o == "no-new-privileges:true" {
			found = true
		}
	}
	if !found {
		t.Errorf("no-new-privileges is not set, so a setuid binary could raise "+
			"privileges the cap-drop just removed: %v", Hardened().SecurityOpt)
	}
}

// The account is set explicitly. An empty User means the image's own USER
// wins, which for most images is root — the one uid whose capabilities are
// not empty even after the cap-drop.
func TestHardenedRunsAsAnExplicitAccount(t *testing.T) {
	if Hardened().User == "" {
		t.Error("Hardened sets no User, so the container runs as whatever the " +
			"image chose, which is usually root")
	}
}

func TestHardenedBoundsProcessesAndReapsThem(t *testing.T) {
	h := Hardened()
	if h.PidsLimit <= 0 {
		t.Errorf("PidsLimit = %d; without a bound a fork bomb takes the daemon "+
			"down with it", h.PidsLimit)
	}
	if !h.Init {
		t.Error("Init is not set; a pids-limited container with no reaper turns " +
			"leaked zombies into fork failures")
	}
}

// The rendered argv is the actual interface to docker, so it is asserted
// too: a field that never reaches Args() is a promise made and not kept.
// And the negatives — the flags that would undo the whole thing — must
// never appear.
func TestHardenedArgvCarriesTheHardeningAndNoneOfTheEscapes(t *testing.T) {
	h := Hardened()
	h.Image = "alpine"
	line := " " + join(h.Args()) + " "

	for _, want := range []string{
		" --cap-drop ALL ",
		" --security-opt no-new-privileges:true ",
		" --user ",
		" --pids-limit ",
		" --init ",
	} {
		if !contains(line, want) {
			t.Errorf("the rendered run is missing %q:\n%s", want, line)
		}
	}
	for _, never := range []string{
		" --privileged ",
		" --network host ",
		" --pid host ",
		" --ipc host ",
		" --userns host ",
		" --security-opt seccomp=unconfined ",
		" --security-opt apparmor=unconfined ",
		"/var/run/docker.sock",
	} {
		if contains(line, never) {
			t.Errorf("the rendered run contains %q, which undoes the sandbox:\n%s",
				never, line)
		}
	}
	for cap, why := range forbiddenCaps {
		if contains(line, " --cap-add "+cap+" ") {
			t.Errorf("the rendered run adds %s (%s)", cap, why)
		}
	}
}

func join(a []string) string {
	out := ""
	for i, s := range a {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
