// Package detect identifies a project's language, version and ports from
// what is on disk.
//
// Every decision comes from the language plugin's data, never from a table
// in this package. Adding a language must mean dropping in a directory, or
// the plugin format is decoration — which is what it was in v1, where
// detection markers lived in a bash case statement while every
// language.yaml declared them and was ignored.
package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mwing/isolated-dev/go/internal/langs"
)

// Result is what was found in a project directory.
type Result struct {
	Language *langs.Language
	// Version is the version markers named, or the plugin's default.
	Version string
	// VersionFrom names the file the version came from, empty when it fell
	// back to the plugin default.
	VersionFrom string
	// Markers are the files that identified the language, for explaining
	// the decision rather than asserting it.
	Markers []string
	// Ports are the plugin's declared ports.
	Ports []int
}

// Found reports whether a language was identified.
func (r Result) Found() bool { return r.Language != nil }

// Explain describes how the conclusion was reached, so `dev2 doctor` and
// `--verbose` can answer "why this image?" without the user guessing.
func (r Result) Explain() string {
	if !r.Found() {
		return "no language detected"
	}
	var b strings.Builder
	b.WriteString(r.Language.Name)
	if r.Version != "" {
		b.WriteString(" " + r.Version)
	}
	if len(r.Markers) > 0 {
		b.WriteString(" (from " + strings.Join(r.Markers, ", ") + ")")
	}
	if r.VersionFrom != "" {
		b.WriteString(", version from " + r.VersionFrom)
	} else if r.Version != "" {
		b.WriteString(", version is the plugin default")
	}
	return b.String()
}

// Detect identifies the project in dir.
//
// When several languages match, the one with the most matching markers
// wins, ties broken by name so the result is stable. v1 took whichever
// language its directory listing happened to reach first, which made the
// answer depend on filesystem order.
func Detect(dir string, set *langs.Set) Result {
	type candidate struct {
		lang    *langs.Language
		markers []string
	}
	var found []candidate

	for _, l := range set.All() {
		var markers []string
		for _, pattern := range l.Detection.Files {
			matches, err := matchMarker(dir, pattern)
			if err != nil || !matches {
				continue
			}
			markers = append(markers, pattern)
		}
		if len(markers) > 0 {
			found = append(found, candidate{lang: l, markers: markers})
		}
	}
	if len(found) == 0 {
		return Result{}
	}

	sort.Slice(found, func(i, j int) bool {
		if len(found[i].markers) != len(found[j].markers) {
			return len(found[i].markers) > len(found[j].markers)
		}
		return found[i].lang.Name < found[j].lang.Name
	})

	win := found[0]
	res := Result{
		Language: win.lang,
		Markers:  win.markers,
		Ports:    append([]int(nil), win.lang.Ports...),
	}
	res.Version, res.VersionFrom = detectVersion(dir, win.lang)
	if res.Version == "" {
		res.Version = win.lang.DefaultVersion()
	}
	return res
}

// matchMarker reports whether a marker pattern matches something in dir.
func matchMarker(dir, pattern string) (bool, error) {
	if strings.ContainsAny(pattern, "*?[") {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		return len(matches) > 0, err
	}
	info, err := os.Stat(filepath.Join(dir, pattern))
	if err != nil {
		return false, nil
	}
	return !info.IsDir(), nil
}

// detectVersion applies the plugin's version_files rules in order.
func detectVersion(dir string, l *langs.Language) (version, from string) {
	for _, vf := range l.Detection.VersionFiles {
		if vf.File == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, vf.File))
		if err != nil {
			continue
		}
		content := string(raw)

		if vf.Extract == "" {
			// No pattern: the file holds the version, possibly with a
			// leading v and trailing junk (.nvmrc, .python-version).
			if v := firstLine(content); v != "" {
				return normalizeVersion(v), vf.File
			}
			continue
		}
		re, err := regexp.Compile(vf.Extract)
		if err != nil {
			continue
		}
		m := re.FindStringSubmatch(content)
		if len(m) > 1 && m[1] != "" {
			return normalizeVersion(m[1]), vf.File
		}
		// A pattern with no capture group: take the whole match rather
		// than silently reporting no version.
		if len(m) == 1 && m[0] != "" {
			return normalizeVersion(m[0]), vf.File
		}
	}
	return "", ""
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// normalizeVersion strips a leading v and any range operators a manifest
// may carry, so ">=3.11" and "v20.1.0" become versions a tag can use.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimLeft(v, "^~>=< ")
	v = strings.TrimPrefix(v, "v")
	return strings.TrimSpace(v)
}

// Ports returns the ports to forward: the project's explicit list when
// given, otherwise the detected language's declared ports.
func Ports(explicit string, r Result) []int {
	if explicit == "" {
		return r.Ports
	}
	var out []int
	for _, part := range strings.Split(explicit, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var n int
		var neg bool
		for _, c := range part {
			if c < '0' || c > '9' {
				neg = true
				break
			}
			n = n*10 + int(c-'0')
		}
		if neg || n < 1 || n > 65535 {
			continue
		}
		out = append(out, n)
	}
	return out
}
