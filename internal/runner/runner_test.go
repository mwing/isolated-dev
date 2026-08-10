package runner

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestCommandStringQuotesArgumentsWithSpaces(t *testing.T) {
	// The v1 bug class: a value with a space must stay one argument, and
	// must be shown as one argument.
	c := Command{Path: "docker", Args: []string{"run", "-e", "MSG=hello world", "img"}}
	got := c.String()
	want := `docker run -e 'MSG=hello world' img`
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestCommandStringQuotesEmptyAndQuoted(t *testing.T) {
	c := Command{Path: "sh", Args: []string{"", "it's"}}
	got := c.String()
	if !strings.Contains(got, `''`) {
		t.Errorf("empty arg not quoted: %q", got)
	}
	if !strings.Contains(got, `'it'\''s'`) {
		t.Errorf("single quote not escaped: %q", got)
	}
}

func TestExecRunCapturesOutputAndExitCode(t *testing.T) {
	r := New(false)
	ctx := context.Background()

	res, err := r.Run(ctx, Command{Path: "printf", Args: []string{"hi"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "hi" || res.ExitCode != 0 {
		t.Fatalf("got %+v, want stdout=hi exit=0", res)
	}
}

func TestExecRunNonZeroExitIsNotAnError(t *testing.T) {
	// doctor probes things that are expected to fail; a non-zero status is
	// data, not an error.
	r := New(false)
	res, err := r.Run(context.Background(), Command{Path: "false"})
	if err != nil {
		t.Fatalf("Run returned error for non-zero exit: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code, got %d", res.ExitCode)
	}
}

func TestExecRunArgumentsAreNotShellSplit(t *testing.T) {
	// If args were ever joined into a shell string, this would print two
	// lines instead of one argument containing a space.
	r := New(false)
	res, err := r.Run(context.Background(), Command{
		Path: "printf", Args: []string{"%s\n", "one two"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "one two\n" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "one two\n")
	}
}

func TestExecRunMissingBinaryIsAnError(t *testing.T) {
	r := New(false)
	if _, err := r.Run(context.Background(), Command{Path: "definitely-not-a-real-binary-xyz"}); err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestExecRunEmptyPathRejected(t *testing.T) {
	r := New(false)
	if _, err := r.Run(context.Background(), Command{}); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestVerboseLogsExactArgv(t *testing.T) {
	var log strings.Builder
	r := &Exec{Verbose: true, Log: &log}
	_, err := r.Run(context.Background(), Command{Path: "printf", Args: []string{"x y"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(log.String(), `+ printf 'x y'`) {
		t.Fatalf("verbose log = %q", log.String())
	}
}

func TestFakeRecordsAndResponds(t *testing.T) {
	f := NewFake()
	f.Response["orb list"] = Result{Stdout: "dev-vm-docker-host running\n"}

	res, err := f.Run(context.Background(), Command{Path: "orb", Args: []string{"list"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stdout, "docker-host") {
		t.Fatalf("unexpected response %+v", res)
	}
	if got := f.Lines(); len(got) != 1 || got[0] != "orb list" {
		t.Fatalf("Lines() = %v", got)
	}
}

func TestAFailureThatIsNotAnExitStatusIsNotSuccess(t *testing.T) {
	// The default branch of the Wait switch reported Result{} with no
	// error, which reads as exit 0. A workload that never produced a status
	// is not a workload that succeeded, and a PTY run said so for years.
	code, err := exitStatusOf("docker", errors.New("pty: read: input/output error"))
	if err == nil {
		t.Fatal("a failure with no exit status was reported as success")
	}
	if code == 0 {
		t.Fatal("exit code 0 for a process that never exited")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Errorf("the error does not say what failed: %v", err)
	}
}

func TestASignalIsReportedTheWayAShellReportsIt(t *testing.T) {
	// exec reports -1 for a signalled process, which is neither a status
	// nor something a caller can propagate. 128+N is what a shell says.
	cmd := exec.Command("sh", "-c", "kill -TERM $$")
	if err := cmd.Run(); err == nil {
		t.Fatal("the shell did not kill itself")
	} else if code, cerr := exitStatusOf("sh", err); code != 128+int(syscall.SIGTERM) || cerr != nil {
		t.Fatalf("code = %d, err = %v; want %d and no error",
			code, cerr, 128+int(syscall.SIGTERM))
	}
}

func TestAnOrdinaryExitStatusSurvives(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 7")
	code, err := exitStatusOf("sh", cmd.Run())
	if err != nil {
		t.Fatalf("an ordinary non-zero exit became an error: %v", err)
	}
	if code != 7 {
		t.Fatalf("code = %d, want 7", code)
	}
}
