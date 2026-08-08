// Package scan runs vulnerability scanners against a built image.
//
// The scanners run on the host while the image lives in the VM, so the
// image is exported and scanned from a file. That indirection is why v1's
// scan was easy to run and easy to ignore: it reported findings and
// returned success, so nothing downstream could act on it.
package scan

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mwing/isolated-dev/go/internal/runner"
)

// Severity is the lowest level worth failing on.
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// ParseSeverity validates a threshold.
func ParseSeverity(s string) (Severity, error) {
	switch Severity(strings.ToLower(strings.TrimSpace(s))) {
	case "", SeverityHigh:
		return SeverityHigh, nil
	case SeverityLow:
		return SeverityLow, nil
	case SeverityMedium:
		return SeverityMedium, nil
	case SeverityCritical:
		return SeverityCritical, nil
	default:
		return "", fmt.Errorf("scan: unknown severity %q (want low, medium, high or critical)", s)
	}
}

// trivyLevels lists the levels at or above a threshold, which is what
// trivy wants rather than a floor.
func (s Severity) trivyLevels() string {
	order := []Severity{SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical}
	names := map[Severity]string{
		SeverityLow: "LOW", SeverityMedium: "MEDIUM",
		SeverityHigh: "HIGH", SeverityCritical: "CRITICAL",
	}
	var out []string
	include := false
	for _, level := range order {
		if level == s {
			include = true
		}
		if include {
			out = append(out, names[level])
		}
	}
	return strings.Join(out, ",")
}

// Scanner is one tool that can inspect an exported image.
type Scanner struct {
	Name string
	// Command builds the invocation for an image archive.
	Command func(archive string, sev Severity) runner.Command
}

// Scanners are the supported tools, in the order they run.
func Scanners() []Scanner {
	return []Scanner{
		{
			Name: "trivy",
			Command: func(archive string, sev Severity) runner.Command {
				return runner.Command{
					Path: "trivy",
					Args: []string{
						"image", "--input", archive,
						"--severity", sev.trivyLevels(),
						"--scanners", "vuln",
						// A scanner that finds problems and exits zero
						// cannot gate anything.
						"--exit-code", "1",
					},
				}
			},
		},
		{
			Name: "grype",
			Command: func(archive string, sev Severity) runner.Command {
				return runner.Command{
					Path: "grype",
					Args: []string{"docker-archive:" + archive, "--fail-on", string(sev)},
				}
			},
		},
	}
}

// Available returns the scanners present on this machine.
func Available() []Scanner {
	var out []Scanner
	for _, s := range Scanners() {
		if _, ok := runner.LookPath(s.Name); ok {
			out = append(out, s)
		}
	}
	return out
}

// Result is one scanner's outcome.
type Result struct {
	Scanner string
	// Findings reports whether the scanner failed on the threshold.
	Findings bool
	// Err is set when the scanner could not run at all, which is not the
	// same as finding something and must not be reported as clean.
	Err error
}

// Run executes a scanner against an exported image.
func Run(ctx context.Context, r runner.Runner, s Scanner, archive string,
	sev Severity, out runnerWriter) Result {
	cmd := s.Command(archive, sev)
	cmd.Stdout, cmd.Stderr = out, out

	res, err := r.Run(ctx, cmd)
	if err != nil {
		return Result{Scanner: s.Name, Err: err}
	}
	return Result{Scanner: s.Name, Findings: res.ExitCode != 0}
}

type runnerWriter interface{ Write([]byte) (int, error) }

// Summarize reports whether anything failed and why, so a caller can pick
// an exit code without re-deriving it.
func Summarize(results []Result) (failed bool, reasons []string) {
	for _, r := range results {
		switch {
		case r.Err != nil:
			failed = true
			reasons = append(reasons, fmt.Sprintf("%s could not run: %v", r.Scanner, r.Err))
		case r.Findings:
			failed = true
			reasons = append(reasons, fmt.Sprintf("%s found vulnerabilities at or above the threshold", r.Scanner))
		}
	}
	sort.Strings(reasons)
	return failed, reasons
}
