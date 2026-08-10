package history

import (
	"path/filepath"
	"testing"
	"time"
)

func tempFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "p.history.jsonl")
}

func TestAppendAndLoadRoundTrip(t *testing.T) {
	path := tempFile(t)
	now := time.Now().Truncate(time.Second)
	in := Run{
		Start:   now,
		End:     now.Add(time.Minute),
		Command: "go test ./...",
		Allowed: map[string]int{"proxy.golang.org:443": 12},
		Denied:  map[string]int{"example.com:443": 1},
	}
	if err := Append(path, in); err != nil {
		t.Fatal(err)
	}
	runs, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs", len(runs))
	}
	if runs[0].Command != in.Command || runs[0].Allowed["proxy.golang.org:443"] != 12 {
		t.Fatalf("round trip lost data: %+v", runs[0])
	}
	if runs[0].Duration() != time.Minute {
		t.Fatalf("duration = %v", runs[0].Duration())
	}
}

// A run that reached nothing says nothing, and recording it would push the
// runs that matter out of a capped file.
func TestAppendSkipsEmptyRuns(t *testing.T) {
	path := tempFile(t)
	if err := Append(path, Run{Start: time.Now(), Command: "true"}); err != nil {
		t.Fatal(err)
	}
	runs, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("recorded an empty run: %+v", runs)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	runs, err := Load(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil || runs != nil {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
}

// One unreadable line must not cost the whole history, which is wanted
// precisely when something has gone wrong.
func TestLoadSkipsCorruptLines(t *testing.T) {
	path := tempFile(t)
	good := Run{Start: time.Now(), Allowed: map[string]int{"a:443": 1}}
	if err := Append(path, good); err != nil {
		t.Fatal(err)
	}
	if err := appendRaw(path, "{not json\n"); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, good); err != nil {
		t.Fatal(err)
	}
	runs, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
}

func TestAppendTrimsToTheCap(t *testing.T) {
	path := tempFile(t)
	for i := 0; i < maxRuns+25; i++ {
		if err := Append(path, Run{
			Start:   time.Now(),
			Command: "run",
			Allowed: map[string]int{"a:443": 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != maxRuns {
		t.Fatalf("kept %d runs, want %d", len(runs), maxRuns)
	}
}

func TestContactsRanksMostRecentFirst(t *testing.T) {
	old := time.Now().Add(-72 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	runs := []Run{
		{Start: old, Allowed: map[string]int{"old.example:443": 3}},
		{Start: recent, Allowed: map[string]int{"new.example:443": 1}},
		{Start: old, Allowed: map[string]int{"new.example:443": 5}},
	}
	got := Contacts(runs)
	if len(got) != 2 {
		t.Fatalf("got %d contacts", len(got))
	}
	if got[0].Host != "new.example" {
		t.Fatalf("most recent first failed: %+v", got)
	}
	// Counts accumulate across runs, and the newest run wins the date.
	if got[0].Count != 6 || !got[0].Last.Equal(recent) {
		t.Fatalf("aggregation wrong: %+v", got[0])
	}
	if got[1].Port != 443 {
		t.Fatalf("port not parsed: %+v", got[1])
	}
}

func appendRaw(path, line string) error {
	f, err := openAppend(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(line)
	return err
}
