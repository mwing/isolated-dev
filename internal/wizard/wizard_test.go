package wizard

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func labels(items []Action) string {
	var b strings.Builder
	for _, a := range items {
		b.WriteString(a.Label + "\n")
	}
	return b.String()
}

func checkText(cs []Check) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.String() + "\n")
	}
	return b.String()
}

// The first thing a newcomer needs is the acceptance step, because until
// it happens a run stops with a message they did not ask for.
func TestPendingRequestsComeFirst(t *testing.T) {
	items := Menu(Facts{
		BackendReady:  true,
		PendingGrants: []string{"api.example.com"},
	})
	if len(items) == 0 || !strings.Contains(items[0].Label, "requests") {
		t.Fatalf("first action = %q, want the acceptance review\n%s",
			items[0].Label, labels(items))
	}
}

func TestBuildIsOfferedOnlyWhenTheImageIsMissing(t *testing.T) {
	missing := Menu(Facts{BackendReady: true})
	if !strings.Contains(labels(missing), "Build this project's image") {
		t.Fatalf("no build action offered:\n%s", labels(missing))
	}
	built := Menu(Facts{BackendReady: true, ImageBuilt: true})
	if strings.Contains(labels(built), "Build this project's image") {
		t.Fatalf("offered to build an image that exists:\n%s", labels(built))
	}
}

// Scanning an image that does not exist fails with a message about the
// image rather than about the vulnerabilities someone asked for.
func TestScanAndUpdateNeedAnImage(t *testing.T) {
	items := labels(Menu(Facts{BackendReady: true}))
	if strings.Contains(items, "Scan the image") || strings.Contains(items, "Update base image") {
		t.Fatalf("offered image actions with no image:\n%s", items)
	}
}

func TestNothingNeedingADaemonIsOfferedWithoutOne(t *testing.T) {
	items := labels(Menu(Facts{BackendReady: false}))
	for _, unwanted := range []string{"Build this project's image", "Scan the image"} {
		if strings.Contains(items, unwanted) {
			t.Fatalf("offered %q with no backend:\n%s", unwanted, items)
		}
	}
	// Doctor is exactly what to run when the backend is the problem.
	if !strings.Contains(items, "Check the setup") {
		t.Fatalf("doctor not offered when the backend is down:\n%s", items)
	}
}

func TestAssessReportsABrokenBackendAsBroken(t *testing.T) {
	text := checkText(Assess(Facts{Detected: "python", BackendReady: false,
		BackendDetail: "VM not running"}))
	if !strings.Contains(text, "✗ container backend — VM not running") {
		t.Fatalf("backend not reported as broken:\n%s", text)
	}
	// With no daemon, the image state is unknown rather than absent.
	if !strings.Contains(text, "not checked") {
		t.Fatalf("claimed to know the image state without a daemon:\n%s", text)
	}
}

func TestAssessNamesTheBuildContextWhenItIsLarge(t *testing.T) {
	text := checkText(Assess(Facts{
		Detected: "node", BackendReady: true,
		ContextBytes: 7 << 30, ContextFiles: 90000,
	}))
	if !strings.Contains(text, "7.0G") {
		t.Fatalf("context size not reported:\n%s", text)
	}
}

// Every menu entry must be a command the user could have typed, or an
// internal action that says so. An entry that is neither would run nothing.
func TestEveryActionIsRunnable(t *testing.T) {
	f := Facts{BackendReady: true, ImageBuilt: true, Runs: 3,
		Agents: []string{"claude"}, PendingGrants: []string{"x.example"}}
	for _, a := range Menu(f) {
		if len(a.Args) == 0 && a.Internal == "" {
			t.Fatalf("action %q does nothing", a.Label)
		}
		if a.Explain == "" {
			t.Fatalf("action %q explains nothing", a.Label)
		}
	}
}

func TestDockerignoreIsLanguageAware(t *testing.T) {
	node := Dockerignore("node")
	if !strings.Contains(node, "node_modules") || !strings.Contains(node, ".git") {
		t.Fatalf("node ignore missing entries:\n%s", node)
	}
	py := Dockerignore("python")
	if !strings.Contains(py, "__pycache__") || strings.Contains(py, "node_modules") {
		t.Fatalf("python ignore wrong:\n%s", py)
	}
	// It says what it does not do: these files are still in the container.
	if !strings.Contains(node, "mounted") {
		t.Fatalf("no explanation in the generated file:\n%s", node)
	}
}

func TestMenuNavigationAndSelection(t *testing.T) {
	items := []Action{
		{Label: "first", Args: []string{"a"}, Explain: "x"},
		{Label: "second", Args: []string{"b"}, Explain: "y"},
	}
	m := New("t", nil, items)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Chosen() == nil || m.Chosen().Label != "second" {
		t.Fatalf("chosen = %v, want second", m.Chosen())
	}
}

func TestQuittingChoosesNothing(t *testing.T) {
	m := New("t", nil, []Action{{Label: "first", Args: []string{"a"}, Explain: "x"}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m.Chosen() != nil {
		t.Fatalf("quitting selected %v", m.Chosen())
	}
}

// The cursor must not walk off either end of a short list.
func TestCursorStaysInRange(t *testing.T) {
	m := New("t", nil, []Action{{Label: "only", Args: []string{"a"}, Explain: "x"}})
	for i := 0; i < 5; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	for i := 0; i < 5; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Chosen() == nil || m.Chosen().Label != "only" {
		t.Fatalf("chosen = %v", m.Chosen())
	}
}

func TestViewShowsChecksAndTheHighlightedExplanation(t *testing.T) {
	m := New("dev — demo",
		[]Check{{OK, "language detected", "python 3.14"}},
		[]Action{
			{Label: "Open a shell", Args: []string{"shell"}, Explain: "a shell in the container"},
			{Label: "Other", Args: []string{"doctor"}, Explain: "not this one"},
		})
	view := m.View()
	for _, want := range []string{"dev — demo", "python 3.14", "Open a shell",
		"a shell in the container", "runs: dev shell"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "not this one") {
		t.Fatal("explained an entry that is not selected")
	}
}
