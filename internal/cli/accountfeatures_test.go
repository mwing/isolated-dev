package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/agent"
	"github.com/mwing/isolated-dev/internal/container"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/trust"
)

const trafficSwitch = "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"

func agentEnvHasSwitch(t *testing.T, h *harness) bool {
	t.Helper()
	runs := h.runsWithRole("agent")
	if len(runs) == 0 {
		t.Fatalf("no agent container was started:\n%s", h.stderr.String())
	}
	for _, run := range runs {
		if containsPair(run, "--env", trafficSwitch) || containsPair(run, "-e", trafficSwitch) {
			return true
		}
		for _, a := range run {
			if a == "--env="+trafficSwitch {
				return true
			}
		}
	}
	return false
}

func accountFeaturesRun(t *testing.T, global, project string, args ...string) *harness {
	t.Helper()
	h := newHarness(t)
	h.readyBackend()
	h.readySidecar()
	gitProject(t, h)
	h.writeGlobal(t, "agent_clone: false\n"+global)
	if project != "" {
		h.writeProject(t, project)
	}
	if err := h.run(t, args...); err != nil {
		t.Logf("%v: %v", args, err)
	}
	return h
}

func TestTheTrafficSwitchIsOnByDefault(t *testing.T) {
	h := accountFeaturesRun(t, "", "", "agent", "run", "claude", "--tty", "off")
	if !agentEnvHasSwitch(t, h) {
		t.Error("the agent fetches feature flags without being asked")
	}
}

func TestAccountFeaturesDropTheSwitch(t *testing.T) {
	h := accountFeaturesRun(t, "", "", "agent", "run", "claude", "--account-features", "--tty", "off")
	if agentEnvHasSwitch(t, h) {
		t.Error("--account-features left the switch set")
	}
	if !strings.Contains(h.stdout.String()+h.stderr.String(), "telemetry") {
		t.Error("the run did not say telemetry is on")
	}
}

func TestAccountFeaturesFromYourOwnConfig(t *testing.T) {
	h := accountFeaturesRun(t, "account_features: true\n", "", "agent", "run", "claude", "--tty", "off")
	if agentEnvHasSwitch(t, h) {
		t.Error("account_features: true in your config was not honoured")
	}
}

// It sends the agent's telemetry to its vendor, which is not a repository's
// to decide for the person running it.
func TestARepositoryCannotTurnOnAccountFeatures(t *testing.T) {
	h := accountFeaturesRun(t, "", "account_features: true\n", "agent", "run", "claude", "--tty", "off")
	if !agentEnvHasSwitch(t, h) {
		t.Error("a repository's .devenv.yaml turned on account features")
	}
	if !strings.Contains(h.stderr.String(), "ignored from your project file") {
		t.Errorf("the project file's setting was not reported as ignored:\n%s", h.stderr.String())
	}
}

// The console cannot start an agent without a terminal, so its agent is
// checked where the console builds it.
func TestTheConsoleAgentHonoursAccountFeatures(t *testing.T) {
	for _, want := range []bool{false, true} {
		h := newHarness(t)
		h.readyBackend()
		h.readySidecar()
		gitProject(t, h)
		cfg, p, err := resolveProject(h.env)
		if err != nil {
			t.Fatal(err)
		}
		store, err := trust.Load(h.paths.Home, p.Dir)
		if err != nil {
			t.Fatal(err)
		}
		eng := container.New(h.env.driver(cfg.VMName))
		opts, _, _, err := prepareAgent(context.Background(), h.env, eng, p, store, cfg,
			"claude", nil, false, want, false)
		if err != nil {
			t.Fatal(err)
		}
		spec := agent.Spec(*opts, netpolicy.Topology{SidecarIP: "10.0.0.2"})
		has := false
		for _, e := range spec.Env {
			if e == trafficSwitch {
				has = true
			}
		}
		if has == want {
			t.Errorf("account features %v: traffic switch set = %v", want, has)
		}
	}
}

func TestTheFlagOverridesAConfiguredDefault(t *testing.T) {
	h := accountFeaturesRun(t, "account_features: true\n", "",
		"agent", "run", "claude", "--account-features=false", "--tty", "off")
	if !agentEnvHasSwitch(t, h) {
		t.Error("--account-features=false did not override account_features: true")
	}
}

// The console stops at the terminal check before starting an agent, after
// it has built the agent's options and announced them.
func TestTheConsoleAnnouncesAccountFeatures(t *testing.T) {
	for _, c := range []struct {
		global string
		args   []string
		want   bool
	}{
		{"", []string{"console", "--agent", "claude"}, false},
		{"", []string{"console", "--agent", "claude", "--account-features"}, true},
		{"account_features: true\n", []string{"console", "--agent", "claude"}, true},
		{"account_features: true\n", []string{"console", "--agent", "claude", "--account-features=false"}, false},
	} {
		h := accountFeaturesRun(t, c.global, "", c.args...)
		got := strings.Contains(h.stderr.String(), "feature flags on")
		if got != c.want {
			t.Errorf("config %q, %v: announced = %v, want %v\n%s", c.global, c.args, got, c.want, h.stderr.String())
		}
	}
}

func TestAccountFeaturesDenyTheTelemetryHosts(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"agent", "run", "claude", "--tty", "off"}, false},
		{[]string{"agent", "run", "claude", "--account-features", "--tty", "off"}, true},
		{[]string{"console", "--agent", "claude", "--account-features"}, true},
	} {
		h := accountFeaturesRun(t, "", "", c.args...)
		deny, _ := h.sidecarFlag(t, "--deny")
		got := contains(strings.Split(deny, ","), "http-intake.logs.us5.datadoghq.com")
		if got != c.want {
			t.Errorf("%v: telemetry host denied = %v, want %v (deny = %q)", c.args, got, c.want, deny)
		}
	}
}
