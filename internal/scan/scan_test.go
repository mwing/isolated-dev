package scan

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mwing/isolated-dev/internal/runner"
)

func TestParseSeverity(t *testing.T) {
	for _, in := range []string{"", "low", "MEDIUM", " high ", "critical"} {
		if _, err := ParseSeverity(in); err != nil {
			t.Errorf("ParseSeverity(%q): %v", in, err)
		}
	}
	if _, err := ParseSeverity("catastrophic"); err == nil {
		t.Error("unknown severity accepted")
	}
	// The default must not be the loosest setting: a gate that fails on
	// nothing is not a gate.
	sev, _ := ParseSeverity("")
	if sev != SeverityHigh {
		t.Errorf("default = %q, want high", sev)
	}
}

func TestSeverityIncludesEverythingAbove(t *testing.T) {
	// A threshold is a floor. Passing only "HIGH" to trivy would ignore
	// critical findings, which is the opposite of what was asked for.
	got := SeverityHigh.trivyLevels()
	if !strings.Contains(got, "HIGH") || !strings.Contains(got, "CRITICAL") {
		t.Fatalf("high threshold = %q", got)
	}
	if strings.Contains(got, "MEDIUM") {
		t.Errorf("high threshold included medium: %q", got)
	}
	if low := SeverityLow.trivyLevels(); !strings.Contains(low, "CRITICAL") {
		t.Errorf("low threshold = %q, want everything", low)
	}
}

func TestScannersFailOnFindings(t *testing.T) {
	// The whole point: a scanner that reports problems and exits zero
	// cannot gate anything.
	for _, s := range Scanners() {
		cmd := s.Command("/tmp/img.tar", Options{Severity: SeverityHigh})
		args := strings.Join(cmd.Args, " ")
		switch s.Name {
		case "trivy":
			if !strings.Contains(args, "--exit-code 1") {
				t.Errorf("trivy would not fail: %s", args)
			}
		case "grype":
			if !strings.Contains(args, "--fail-on high") {
				t.Errorf("grype would not fail: %s", args)
			}
		}
		if !strings.Contains(args, "/tmp/img.tar") {
			t.Errorf("%s does not scan the archive: %s", s.Name, args)
		}
	}
}

func TestRunReportsFindings(t *testing.T) {
	f := runner.NewFake()
	f.Default = runner.Result{ExitCode: 1}

	got := Run(context.Background(), f, Scanners()[0], "/tmp/a.tar",
		Options{Severity: SeverityHigh}, io.Discard)
	if !got.Findings || got.Err != nil {
		t.Fatalf("result = %+v, want findings", got)
	}
}

func TestScannerThatCannotRunIsNotClean(t *testing.T) {
	// A missing or broken scanner must not be reported as a pass.
	f := runner.NewFake()
	f.Err["trivy"] = errors.New("not installed")

	got := Run(context.Background(), f, Scanners()[0], "/tmp/a.tar",
		Options{Severity: SeverityHigh}, io.Discard)
	if got.Err == nil {
		t.Fatal("a scanner that could not run reported success")
	}
	failed, reasons := Summarize([]Result{got})
	if !failed || len(reasons) != 1 {
		t.Fatalf("Summarize = %v %v", failed, reasons)
	}
	if !strings.Contains(reasons[0], "could not run") {
		t.Errorf("reason does not distinguish a broken scanner: %q", reasons[0])
	}
}

func TestSummarizeCleanRun(t *testing.T) {
	failed, reasons := Summarize([]Result{
		{Scanner: "trivy"}, {Scanner: "grype"},
	})
	if failed || len(reasons) != 0 {
		t.Fatalf("clean run reported failure: %v %v", failed, reasons)
	}
}

func TestUnfixedAreHiddenByDefault(t *testing.T) {
	// A real image produced 303 findings of which 13 had a fix. A gate
	// that fails on the other 290 cannot be satisfied by any action the
	// user can take, and a report nobody can act on is one they learn to
	// skip — taking the 13 that mattered with it.
	for _, s := range Scanners() {
		def := strings.Join(s.Command("/tmp/a.tar", Options{Severity: SeverityHigh}).Args, " ")
		switch s.Name {
		case "trivy":
			if !strings.Contains(def, "--ignore-unfixed") {
				t.Errorf("trivy reports unfixable findings by default: %s", def)
			}
		case "grype":
			if !strings.Contains(def, "--only-fixed") {
				t.Errorf("grype reports unfixable findings by default: %s", def)
			}
		}

		all := strings.Join(s.Command("/tmp/a.tar",
			Options{Severity: SeverityHigh, IncludeUnfixed: true}).Args, " ")
		if strings.Contains(all, "--ignore-unfixed") || strings.Contains(all, "--only-fixed") {
			t.Errorf("%s still hid unfixed findings when asked for them: %s", s.Name, all)
		}
	}
}
