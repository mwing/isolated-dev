package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mwing/isolated-dev/internal/history"
)

func blockedHistory(t *testing.T, path string, hosts ...string) {
	t.Helper()
	run := history.Run{
		Start:   time.Now().Add(-time.Minute),
		End:     time.Now(),
		Network: "allowlist",
	}
	for _, h := range hosts {
		run.Blocked = append(run.Blocked, history.Dest{
			Host: h, Port: 443, Method: history.MethodConnect, Count: 8,
		})
	}
	if err := history.Append(path, run); err != nil {
		t.Fatal(err)
	}
}

func TestAGrantThatMisspellsABlockedHostIsNamed(t *testing.T) {
	h := newHarness(t)
	path := history.Path(h.paths.Home + "/projects/p.yaml")
	blockedHistory(t, path, "docs.typesafe.ai")

	warnLikelyTypos(h.env, path, "default",
		[]string{"docs.typesage.ai:443"}, []string{"docs.typesage.ai:443"})

	out := h.stderr.String()
	if !strings.Contains(out, "docs.typesafe.ai") {
		t.Errorf("the blocked host was not suggested:\n%s", out)
	}
	// Revoke matches the stored string exactly, so the suggestion has to be
	// the entry as typed rather than the parsed host.
	if !strings.Contains(out, "dev revoke docs.typesage.ai:443") {
		t.Errorf("revoke does not name what was stored:\n%s", out)
	}
	if !strings.Contains(out, "dev allow docs.typesafe.ai:443") {
		t.Errorf("the fix does not keep the port:\n%s", out)
	}
}

func TestAnUnrelatedGrantIsNotQuestioned(t *testing.T) {
	h := newHarness(t)
	path := history.Path(h.paths.Home + "/projects/p.yaml")
	blockedHistory(t, path, "docs.typesafe.ai")

	warnLikelyTypos(h.env, path, "default",
		[]string{"api.mycompany.com"}, []string{"api.mycompany.com"})

	if out := h.stderr.String(); out != "" {
		t.Errorf("warned about an unrelated grant:\n%s", out)
	}
}

func TestAWildcardCoveringTheBlockedHostIsNotATypo(t *testing.T) {
	h := newHarness(t)
	path := history.Path(h.paths.Home + "/projects/p.yaml")
	blockedHistory(t, path, "docs.typesafe.ai")

	warnLikelyTypos(h.env, path, "default",
		[]string{"*.typesafe.ai"}, []string{"*.typesafe.ai"})

	if out := h.stderr.String(); out != "" {
		t.Errorf("warned about a wildcard that already covers it:\n%s", out)
	}
}

// Neighbours, not misspellings: another region, shard, version or vendor is
// deliberately granted ahead of the run that needs it. Each of these differs
// by two deliberate substitutions, which is what separates them from a slip.
func TestDeliberateNeighboursAreNotTypos(t *testing.T) {
	for _, c := range []struct{ granted, blocked string }{
		{"s3.us-east-2.amazonaws.com", "s3.us-east-1.amazonaws.com"},
		{"s3.us-west-1.amazonaws.com", "s3.us-east-1.amazonaws.com"},
		{"eu-north-1.amazonaws.com", "eu-south-1.amazonaws.com"},
		{"gitlab.com", "github.com"},
		{"api.gitlab.com", "api.github.com"},
		{"objects.gitlabusercontent.com", "objects.githubusercontent.com"},
		{"internal.corp.net", "external.corp.net"},
		{"eu.api.example.com", "us.api.example.com"},
		{"node2.cluster.example", "node1.cluster.example"},
		{"api.example.com", "cdn.example.com"},
		{"v2.api.example.com", "v1.api.example.com"},
	} {
		if got, ok := nearestName(c.granted, []string{c.blocked}); ok {
			t.Errorf("%s matched %s as a typo (got %s)", c.granted, c.blocked, got)
		}
	}
}

// A real slip still has to be caught, including one inside a longer name.
//
// A swapped TLD is not among them: com and org are three characters and
// three edits apart, so they fall outside both bounds. Catching those needs
// a different test than "looks like a misspelling".
func TestAMisspelledLabelIsStillFound(t *testing.T) {
	for _, c := range []struct{ granted, blocked string }{
		{"docs.typesafe.ai", "docs.typesage.ai"},   // one substitution
		{"api.anthropic.com", "api.anthropci.com"}, // neighbours swapped
		{"storage.googelapis.com", "storage.googleapis.com"},
		{"storage.googleapi.com", "storage.googleapis.com"}, // one deletion
		{"docs.example.com", "dcos.example.com"},            // a short label, swapped
	} {
		if _, ok := nearestName(c.granted, []string{c.blocked}); !ok {
			t.Errorf("%s did not match %s", c.granted, c.blocked)
		}
	}
}

// Different depths are different destinations, whatever the edit count.
func TestNamesOfDifferentDepthDoNotMatch(t *testing.T) {
	if _, ok := nearestName("typesafe.ai", []string{"docs.typesafe.ai"}); ok {
		t.Error("a parent matched its own subdomain")
	}
}
