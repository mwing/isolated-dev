//go:build integration

// Live containment: does the hardening actually take, and does the sandbox
// still work once it has?
//
// The unit tests assert the flags the tool emits. These ask the kernel
// whether the flags did anything — a fake runner cannot tell a dropped
// capability from a claimed one — and they run on the plain-docker Linux
// path, which is the one where a container escape lands directly on the
// host rather than in a VM.
//
//	DEV_BACKEND=docker go test -tags=integration ./internal/cli/ -run IntegrationContainment
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/container"
)

// probeInsideHardened runs one alpine container built from the real
// hardening and returns its report as key=value pairs. One container start
// covers the whole escape corpus.
func probeInsideHardened(t *testing.T, script string) map[string]string {
	t.Helper()
	env, _ := realEnv(t)
	eng := engineFor(t, env)

	spec := container.Hardened()
	spec.Image = "alpine"
	spec.Command = []string{"sh", "-c", script}

	var out bytes.Buffer
	if _, err := eng.Run(context.Background(), spec, nil, &out, &out); err != nil {
		t.Fatalf("running the probe: %v\n%s", err, out.String())
	}
	report := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			report[k] = v
		}
	}
	return report
}

const containmentProbe = `
echo "uid=$(id -u)"
echo "capeff=$(awk '/^CapEff:/{print $2}' /proc/self/status)"
echo "capbnd=$(awk '/^CapBnd:/{print $2}' /proc/self/status)"
echo "nnp=$(awk '/^NoNewPrivs:/{print $2}' /proc/self/status)"
echo "seccomp=$(awk '/^Seccomp:/{print $2}' /proc/self/status)"
if echo x > /proc/sys/kernel/core_pattern 2>/dev/null; then echo "procsys_writable=yes"; else echo "procsys_writable=no"; fi
touch /tmp/cap_probe 2>/dev/null
if chown 0:0 /tmp/cap_probe 2>/dev/null; then echo "chown_to_root=yes"; else echo "chown_to_root=no"; fi
sysopts=$(awk '$2=="/sys"{print $4; exit}' /proc/mounts)
case ",$sysopts," in *,ro,*) echo "sys_ro=yes";; *) echo "sys_ro=no";; esac
cgopts=$(awk '$2=="/sys/fs/cgroup"{print $4; exit}' /proc/mounts)
case ",$cgopts," in *,ro,*) echo "cgroup_ro=yes";; *) echo "cgroup_ro=no";; esac
if [ -e /var/run/docker.sock ] || [ -e /run/docker.sock ]; then echo "docker_sock=present"; else echo "docker_sock=absent"; fi
`

// The escape corpus, asked of the kernel. Each is a technique that would
// work in a container missing one piece of the hardening; each must fail.
func TestIntegrationContainmentBlocksTheKnownEscapes(t *testing.T) {
	r := probeInsideHardened(t, containmentProbe)

	if r["uid"] == "0" {
		t.Skip("the probe ran as root, so its capabilities describe a different " +
			"posture than a normal invocation; run the suite as a non-root user")
	}

	for _, tc := range []struct{ key, want, why string }{
		{"capeff", "0000000000000000",
			"a non-empty effective set means the container holds a capability"},
		// The mount, not a probe of one file in it. A review caught the
		// first version writing /sys/fs/cgroup/release_agent and reading
		// its failure as "blocked" — but that file is cgroup v1, absent
		// under the cgroup v2 this runs on, so the write failed because it
		// was not there. Absent is not read-only, and a container with a
		// *writable* cgroup2 tree would have passed. The mount option is
		// the property both the release_agent and uevent_helper escapes
		// actually need, and it is version-independent.
		{"sys_ro", "yes",
			"a writable /sys is the release_agent and uevent_helper escape"},
		{"cgroup_ro", "yes",
			"a writable cgroup tree is the release_agent escape"},
		{"procsys_writable", "no",
			"a writable /proc/sys is core_pattern tampering (the parent always " +
				"exists, so this write fails for the read-only reason)"},
		// An external review read the four added-back capabilities (CHOWN,
		// DAC_OVERRIDE, SETGID, SETUID) as a way for a non-root workload to
		// regain root. They are in the bounding set, not the effective set:
		// running as non-root clears the effective and permitted sets, so
		// the caps are inert. A chown to a different uid needs CAP_CHOWN to
		// be effective; that it fails is that capability proven inert, and
		// stands for the rest. See capeff above.
		{"chown_to_root", "no",
			"a non-root workload changed a file's owner to root, which needs " +
				"CAP_CHOWN effective — the added-back capabilities are not inert"},
		{"docker_sock", "absent",
			"the docker socket is root on the host by another name"},
	} {
		if got := r[tc.key]; got != tc.want {
			t.Errorf("%s = %q, want %q — %s", tc.key, got, tc.want, tc.why)
		}
	}
}

// INVARIANT, pinned: the effective security posture is the known default,
// and stays there.
//
// This is `dev pin` turned on the sandbox itself. The values below are
// docker's default posture for a hardened run, recorded so that the day
// docker or the kernel changes a default under us — seccomp switched off,
// a capability added to the bounding set — this test fails instead of a
// probe session noticing by luck a year later. A failure here is not
// necessarily a hole; it is the ground moving, and the fix is to re-audit
// and re-pin deliberately, which is the whole argument the tool makes
// about image tags.
func TestIntegrationContainmentPostureMatchesTheKnownDefault(t *testing.T) {
	r := probeInsideHardened(t, containmentProbe)
	if r["uid"] == "0" {
		t.Skip("posture is pinned for a non-root invocation, the normal case")
	}

	// capbnd 0xc3 = bits 0,1,6,7 = CHOWN, DAC_OVERRIDE, SETGID, SETUID, and
	// nothing else. SYS_ADMIN is bit 21; its absence is the point.
	pinned := map[string]struct{ want, note string }{
		"capeff":  {"0000000000000000", "no effective capabilities"},
		"capbnd":  {"00000000000000c3", "exactly CHOWN, DAC_OVERRIDE, SETGID, SETUID"},
		"nnp":     {"1", "no-new-privileges is on"},
		"seccomp": {"2", "seccomp filtering is active (SECCOMP_MODE_FILTER); 0 would mean docker shipped, or we passed, seccomp=unconfined"},
	}
	for key, p := range pinned {
		if got := r[key]; got != p.want {
			t.Errorf("posture drift: %s = %q, want %q (%s).\n"+
				"docker or the kernel changed a default. Re-audit, then update "+
				"the pin here on purpose.", key, got, p.want, p.note)
		}
	}
}

// Hardening that breaks the feature is not hardening. A run under the full
// posture must still do the ordinary work a run exists for: execute as the
// invoking account, and write a bind-mounted directory. This is the clone
// package's lesson, applied to the container flags.
func TestIntegrationContainmentDoesNotBreakAWorkingRun(t *testing.T) {
	env, _ := realEnv(t)
	eng := engineFor(t, env)

	// The same guard the sibling tests carry: Hardened() runs as HostUser(),
	// which is 0:0 when the suite is invoked by root, and the final check
	// below is about that uid. Without this, a rootful `go test` fails on
	// the runner's identity rather than on a defect.
	if container.HostUID() == 0 {
		t.Skip("invoked as root, so the run's account is root; the non-root " +
			"write assertion describes the normal invocation")
	}

	work := t.TempDir()
	spec := container.Hardened()
	spec.Image = "alpine"
	spec.Mounts = []container.Mount{{Source: work, Target: "/work"}}
	spec.Command = []string{"sh", "-c", "id -u > /work/uid && echo ok > /work/wrote"}

	var out bytes.Buffer
	res, err := eng.Run(context.Background(), spec, nil, &out, &out)
	if err != nil {
		t.Fatalf("a hardened run failed to run at all: %v\n%s", err, out.String())
	}
	if res.ExitCode != 0 {
		t.Fatalf("a hardened run could not do ordinary work (exit %d):\n%s",
			res.ExitCode, out.String())
	}
	if _, err := os.Stat(filepath.Join(work, "wrote")); err != nil {
		t.Errorf("the hardened run could not write its bind mount: %v", err)
	}
	// And it wrote as the invoking account, not as root, which is what
	// makes the mount usable back on the host without a chown.
	uid, _ := os.ReadFile(filepath.Join(work, "uid"))
	if strings.TrimSpace(string(uid)) == "0" {
		t.Errorf("the run wrote its mount as root; files would be unowned by the user")
	}
}
