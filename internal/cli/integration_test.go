//go:build integration

// Tests that need a real docker daemon.
//
// Every bug the project's review found survived a green suite, because the
// tests stopped above the daemon: a fake runner will happily report that a
// container ran, a network was internal, or an image existed. These assert
// against the daemon's actual answers.
//
// Run them with:
//
//	DEV_BACKEND=docker go test -tags=integration ./internal/cli/ -run Integration
//
// They are behind a build tag rather than a skip, so the ordinary suite
// cannot accidentally depend on a daemon being present.
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/runner"
)

// realEnv builds an Env against a temp home and a real docker daemon.
func realEnv(t *testing.T) (*Env, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	return &Env{
		Stdout: out,
		Stderr: out,
		Env:    append(os.Environ(), "DEV_BACKEND=docker"),
		Paths:  config.DefaultPaths(filepath.Join(dir, "home"), project),
		Runner: runner.New(testing.Verbose()),
	}, out
}

func engineFor(t *testing.T, env *Env) *container.Engine {
	t.Helper()
	return container.New(env.driver(""))
}

// The daemon has to be reachable, or every other test here reports a
// failure that is really an absent prerequisite.
func TestIntegrationDaemonIsReachable(t *testing.T) {
	env, _ := realEnv(t)
	st, err := env.driver("").Probe(context.Background())
	if err != nil {
		t.Fatalf("probing docker: %v", err)
	}
	if !st.DaemonUp {
		t.Fatalf("no daemon: %s", st.Detail)
	}
}

// The check that a fake runner cannot make: docker's own answer for
// whether a network has a route out.
func TestIntegrationInternalNetworkIsReallyInternal(t *testing.T) {
	env, _ := realEnv(t)
	eng := engineFor(t, env)
	ctx := context.Background()
	name := "dev-itest-internal"

	_ = eng.NetworkRemove(ctx, name)
	if err := eng.NetworkCreate(ctx, name, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.NetworkRemove(ctx, name) }()

	// Asking again must succeed: the same run reuses its own network.
	if err := eng.NetworkCreate(ctx, name, true); err != nil {
		t.Fatalf("reusing an internal network was refused: %v", err)
	}
}

// The failure this guard exists for, against the daemon that decides it: a
// network with the right name and no isolation.
func TestIntegrationANonInternalNetworkIsRefused(t *testing.T) {
	env, _ := realEnv(t)
	eng := engineFor(t, env)
	ctx := context.Background()
	name := "dev-itest-open"

	_ = eng.NetworkRemove(ctx, name)
	if err := eng.NetworkCreate(ctx, name, false); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.NetworkRemove(ctx, name) }()

	err := eng.NetworkCreate(ctx, name, true)
	if err == nil {
		t.Fatal("a network with a route out was accepted as internal")
	}
	if !strings.Contains(err.Error(), "not internal") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// A workload on an internal network must have no route out at all — the
// property everything else rests on, asserted by trying it.
func TestIntegrationAnInternalNetworkHasNoRouteOut(t *testing.T) {
	env, _ := realEnv(t)
	eng := engineFor(t, env)
	ctx := context.Background()
	name := "dev-itest-noroute"

	_ = eng.NetworkRemove(ctx, name)
	if err := eng.NetworkCreate(ctx, name, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.NetworkRemove(ctx, name) }()

	var out bytes.Buffer
	res, err := eng.Run(ctx, container.RunSpec{
		Image:   "alpine",
		Network: name,
		Remove:  true,
		Command: []string{"sh", "-c", "wget -T 5 -q -O- http://1.1.1.1/ >/dev/null 2>&1; echo exit=$?"},
	}, nil, &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("the probe container failed: %s", out.String())
	}
	if strings.Contains(out.String(), "exit=0") {
		t.Fatalf("a container on an internal network reached the internet:\n%s", out.String())
	}
}

// The hardening flag that made a pid-limited container survivable. A fake
// runner can only report that --init was passed; this asks the kernel.
func TestIntegrationHardenedRunsReapOrphans(t *testing.T) {
	env, _ := realEnv(t)
	eng := engineFor(t, env)

	spec := container.Hardened()
	spec.Image = "alpine"
	spec.Command = []string{"cat", "/proc/1/comm"}

	var out bytes.Buffer
	if _, err := eng.Run(context.Background(), spec, nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); !strings.Contains(got, "init") {
		t.Fatalf("pid 1 is %q; without an init, orphans are never reaped and "+
			"PidsLimit turns that into fork failures", got)
	}
}
