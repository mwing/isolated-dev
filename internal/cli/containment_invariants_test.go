package cli

import (
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/container"
)

// INVARIANT: every sandboxed run the tool starts is hardened, and none is
// weakened.
//
// container.Hardened() is asserted directly in its own package. This is the
// other half: that every path from a command to a `docker run` actually
// starts there. A run built from a bare RunSpec would pass every unit test
// and drop the whole sandbox, because the flags live in the argv and
// nothing downstream re-checks them. So the argv is what this reads.
//
// Walked as a set rather than one test per command, for the reason B28
// gives: the failure worth catching is a run path added later whose author
// did not know the hardening was a rule.

// requiredFlags must appear in every workload run.
var requiredFlags = [][2]string{
	{"--cap-drop", "ALL"},
	{"--security-opt", "no-new-privileges:true"},
}

// forbiddenFlagValues must appear in none.
var forbiddenFlagValues = [][2]string{
	{"--privileged", ""},
	{"--network", "host"},
	{"--pid", "host"},
	{"--ipc", "host"},
	{"--userns", "host"},
	{"--cap-add", "SYS_ADMIN"},
	{"--cap-add", "SYS_PTRACE"},
	{"--cap-add", "NET_ADMIN"},
	{"--cap-add", "DAC_READ_SEARCH"},
	{"--security-opt", "seccomp=unconfined"},
}

func TestInvariantEveryWorkloadRunIsHardened(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, h *harness)
	}{
		{"dev run", func(t *testing.T, h *harness) {
			if err := h.run(t, "run", "--tty", "off", "-c", "true"); err != nil {
				t.Fatalf("run: %v\n%s", err, h.stderr.String())
			}
		}},
		{"dev shell", func(t *testing.T, h *harness) {
			if err := h.run(t, "shell", "--tty", "off", "-c", "true"); err != nil {
				t.Fatalf("shell: %v\n%s", err, h.stderr.String())
			}
		}},
		{"dev agent run", func(t *testing.T, h *harness) {
			gitProject(t, h)
			h.writeGlobal(t, "agent_clone: false\n")
			if err := h.run(t, "agent", "run", "claude", "--tty", "off"); err != nil {
				t.Fatalf("agent run: %v\n%s", err, h.stderr.String())
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.readyBackend()
			h.readySidecar()
			tc.run(t, h)

			run := h.workloadRun(t)
			for _, want := range requiredFlags {
				if !containsPair(run, want[0], want[1]) {
					t.Errorf("%s does not carry %s %s:\n%s",
						tc.name, want[0], want[1], argv(run))
				}
			}
			for _, never := range forbiddenFlagValues {
				if never[1] == "" {
					if contains(run, never[0]) {
						t.Errorf("%s carries %s, which undoes the sandbox:\n%s",
							tc.name, never[0], argv(run))
					}
					continue
				}
				if containsPair(run, never[0], never[1]) {
					t.Errorf("%s carries %s %s, which undoes the sandbox:\n%s",
						tc.name, never[0], never[1], argv(run))
				}
			}
			// The account is not the image's own: an empty --user lets an
			// image run as root, the one uid the cap-drop does not fully
			// disarm. Empty is always wrong. Root is only wrong when the
			// invoker is not themselves root — a run started by root
			// legitimately is root, and asserting otherwise would fail on
			// who ran the test rather than on the code.
			u := flagValue(run, "--user")
			if u == "" {
				t.Errorf("%s sets no --user, so the image's own USER wins:\n%s",
					tc.name, argv(run))
			} else if container.HostUID() != 0 && (strings.HasPrefix(u, "0:") || u == "0") {
				t.Errorf("%s runs as root %q though the invoker is not:\n%s",
					tc.name, u, argv(run))
			}
		})
	}
}

// Not walked here, and why. `dev console` builds its workload from the
// same p.RunSpec and agent.Spec that run/shell/agent prove hardened, then
// mutates the spec — network, DNS, env, ports — without touching the
// capability, no-new-privileges or user fields. Driving it needs a TTY,
// which the fake harness does not give, so it is covered transitively
// rather than directly: the residual it does not catch is a future console
// edit that mutates a hardening field after building. `dev agent update`
// builds straight from container.Hardened(), which its own package
// asserts.

// INVARIANT: break-glass widens exactly one thing, and nothing else.
//
// `--allow-docker-socket` is the deliberate exception — it mounts the
// socket, which is root on the host — and its whole defence is that it is
// the *only* thing that changes. A version that also quietly dropped
// no-new-privileges or ran as root would turn the most dangerous mode into
// the least examined one. So the blast radius is pinned: the socket
// appears, and the rest of the hardening still does.
func TestInvariantBreakGlassKeepsEveryOtherGuard(t *testing.T) {
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	// The mount is driven by the project's request; the flag is only the
	// consent that lets it through. Without the request there is nothing to
	// mount, and the test would prove nothing.
	h.writeProject(t, "mount_docker_socket: true\n")

	if err := h.run(t, "run", "--allow-docker-socket", "--tty", "off", "-c", "true"); err != nil {
		t.Fatalf("break-glass run: %v\n%s", err, h.stderr.String())
	}
	run := h.workloadRun(t)

	if !strings.Contains(argv(run), "docker.sock") {
		t.Fatalf("the break-glass run did not mount the socket, so this test "+
			"proves nothing:\n%s", argv(run))
	}
	// Everything else the default posture has, it still has.
	for _, want := range requiredFlags {
		if !containsPair(run, want[0], want[1]) {
			t.Errorf("break-glass dropped %s %s:\n%s", want[0], want[1], argv(run))
		}
	}
	for _, never := range forbiddenFlagValues {
		if never[1] == "" {
			continue
		}
		if containsPair(run, never[0], never[1]) {
			t.Errorf("break-glass added %s %s beyond the socket:\n%s",
				never[0], never[1], argv(run))
		}
	}
	if u := flagValue(run, "--user"); u == "" ||
		(container.HostUID() != 0 && (strings.HasPrefix(u, "0:") || u == "0")) {
		t.Errorf("break-glass runs as %q, weakening the account too:\n%s", u, argv(run))
	}
}

// flagValue returns the argument following flag, or "".
func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
