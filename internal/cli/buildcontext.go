package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// contextWarnBytes is where a build context stops being incidental. Below
// it the transfer is not what anyone is waiting for; above it, it is.
const contextWarnBytes = 200 << 20

// contextFileLimit bounds the walk. A tree large enough to hit it has
// already answered the only question being asked — "is this big?" — and
// the report says it stopped rather than presenting a partial total as a
// whole one.
const contextFileLimit = 200_000

// contextReport is what a build would send to the daemon.
type contextReport struct {
	Bytes int64
	Files int
	// Largest are the top-level entries by size, biggest first.
	Largest []contextEntry
	// Truncated means the walk stopped at the limit, so Bytes and Files are
	// lower bounds rather than totals.
	Truncated bool
}

type contextEntry struct {
	Name  string
	Bytes int64
}

// measureContext walks dir the way docker would, attributing every file to
// the top-level entry it sits under.
//
// The attribution is the actionable half. "This build sends 7.4G" tells
// someone they have a problem; "node_modules 4.1G, .git 2.2G" tells them
// which two lines fix it.
func measureContext(dir string) (contextReport, error) {
	var rep contextReport
	byEntry := map[string]int64{}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		rep.Bytes += info.Size()
		rep.Files++
		if rel, rerr := filepath.Rel(dir, path); rerr == nil {
			byEntry[topLevel(rel)] += info.Size()
		}
		if rep.Files >= contextFileLimit {
			rep.Truncated = true
			return filepath.SkipAll
		}
		return nil
	})

	for name, size := range byEntry {
		rep.Largest = append(rep.Largest, contextEntry{Name: name, Bytes: size})
	}
	sort.Slice(rep.Largest, func(i, j int) bool {
		if rep.Largest[i].Bytes != rep.Largest[j].Bytes {
			return rep.Largest[i].Bytes > rep.Largest[j].Bytes
		}
		return rep.Largest[i].Name < rep.Largest[j].Name
	})
	return rep, err
}

// topLevel is the first path segment, which is the granularity a
// .dockerignore line is written at.
func topLevel(rel string) string {
	if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
		return rel[:i]
	}
	return rel
}

// contextSize keeps the shape the wizard and doctor already ask for.
func contextSize(dir string) (int64, int, error) {
	rep, err := measureContext(dir)
	return rep.Bytes, rep.Files, err
}

// warnBuildContext says what this build is about to ship, and which
// directories are responsible.
//
// `dev doctor` already reported an absent .dockerignore, but nobody runs
// doctor while waiting for a build — and the wait is the symptom. This is
// the moment the cost is actually paid, so it is the moment to mention it.
//
// Only when there is no .dockerignore: once someone has written one, the
// number here would be measuring a tree docker is no longer sending, and a
// warning that cannot be made to go away is one people learn to scroll
// past.
func warnBuildContext(env *Env, dir string) {
	if _, err := os.Stat(filepath.Join(dir, ".dockerignore")); err == nil {
		return
	}
	rep, err := measureContext(dir)
	if err != nil && rep.Files == 0 {
		return
	}
	if rep.Bytes < contextWarnBytes {
		return
	}

	atLeast := ""
	if rep.Truncated {
		atLeast = "at least "
	}
	fmt.Fprintf(env.Stderr, "⚠  No .dockerignore: this build sends %s%s in %s%d files to the daemon.\n",
		atLeast, humanSize(rep.Bytes), atLeast, rep.Files)

	if named := namedEntries(rep.Largest, 3); named != "" {
		fmt.Fprintf(env.Stderr, "   Biggest: %s\n", named)
	}
	// No generic list of patterns here: "biggest" above names the actual
	// lines to add, derived from this tree rather than from a guess about
	// what a project of this language usually contains.
	fmt.Fprintf(env.Stderr, "   None of it is needed to build the image — your working tree is mounted\n")
	fmt.Fprintf(env.Stderr, "   at /workspace when a run starts regardless. `dev` offers to write one.\n\n")
}

// namedEntries renders the biggest offenders, skipping anything too small
// to be worth a line in a .dockerignore.
func namedEntries(entries []contextEntry, n int) string {
	var parts []string
	for _, e := range entries {
		if len(parts) >= n || e.Bytes < 10<<20 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s", e.Name, humanSize(e.Bytes)))
	}
	return strings.Join(parts, ", ")
}
