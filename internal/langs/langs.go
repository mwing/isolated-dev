// Package langs loads language plugins from disk.
//
// The on-disk format is v1's, unchanged: languages/<name>/language.yaml
// plus a Dockerfile.template and scaffolding files. What changes is that
// v2 actually reads it.
//
// In v1 the format was largely decorative. detection.files was declared in
// every language.yaml and then ignored: the detection files lived in a
// hardcoded bash case statement, so dropping in a new plugin directory did
// nothing until someone edited the script. Version extraction and ports
// were hardcoded the same way. Reading the data means a plugin is a plugin.
package langs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// VersionFile describes where a project records its language version.
type VersionFile struct {
	File string `yaml:"file"`
	// Extract is a regexp whose first capture group is the version.
	Extract string `yaml:"extract"`
}

// Detection is how a project of this language is recognized.
type Detection struct {
	// Files are marker filenames; any one present identifies the language.
	// Entries may be globs, e.g. *.sh.
	Files        []string      `yaml:"files"`
	VersionFiles []VersionFile `yaml:"version_files"`
}

// Files names the plugin's own files.
type Files struct {
	Dockerfile  string   `yaml:"dockerfile"`
	Scaffolding []string `yaml:"scaffolding"`
}

// Language is one plugin.
type Language struct {
	Name        string    `yaml:"name"`
	DisplayName string    `yaml:"display_name"`
	Versions    []string  `yaml:"versions"`
	Detection   Detection `yaml:"detection"`
	// Ports are the ports a project of this language typically serves on.
	Ports []int `yaml:"ports"`
	// Registries are the egress destinations a build or dependency
	// install of this language needs. They exist so allowlist mode has
	// real data per language instead of one global list that is either
	// too wide for everyone or too narrow for someone.
	Registries []string `yaml:"registries"`
	Files      Files    `yaml:"files"`

	// Dir is where the plugin was loaded from.
	Dir string `yaml:"-"`
}

// DockerfileTemplate returns the path to the plugin's Dockerfile template.
func (l *Language) DockerfileTemplate() string {
	name := l.Files.Dockerfile
	if name == "" {
		name = "Dockerfile.template"
	}
	return filepath.Join(l.Dir, name)
}

// DefaultVersion returns the newest declared version, which is what a
// project with no version marker gets.
func (l *Language) DefaultVersion() string {
	if len(l.Versions) == 0 {
		return ""
	}
	return l.Versions[len(l.Versions)-1]
}

// HasVersion reports whether v is declared.
func (l *Language) HasVersion(v string) bool {
	for _, have := range l.Versions {
		if have == v {
			return true
		}
	}
	return false
}

// Validate reports whether the plugin is usable.
func (l *Language) Validate() error {
	if l.Name == "" {
		return fmt.Errorf("langs: plugin in %s has no name", l.Dir)
	}
	if len(l.Detection.Files) == 0 {
		// Without markers the language can never be detected, which in v1
		// was invisible because detection ignored this field anyway.
		return fmt.Errorf("langs: %s declares no detection.files, so it can never be detected", l.Name)
	}
	for _, vf := range l.Detection.VersionFiles {
		if vf.Extract == "" {
			continue
		}
		if _, err := regexp.Compile(vf.Extract); err != nil {
			return fmt.Errorf("langs: %s: version_files entry for %s has an invalid regexp: %w",
				l.Name, vf.File, err)
		}
	}
	return nil
}

// Set is a loaded collection of plugins.
type Set struct {
	langs map[string]*Language
	// Notes records plugins that failed to load, so a broken one is
	// reported rather than silently absent.
	Notes []string
}

// Load reads every plugin under dir. A missing directory yields an empty
// set; a malformed plugin is skipped with a note rather than failing every
// command that touches languages.
func Load(dir string) (*Set, error) {
	s := &Set{langs: map[string]*Language{}}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("langs: reading %s: %w", dir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "language.yaml")
		raw, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				s.Notes = append(s.Notes, fmt.Sprintf("%s: %v", path, err))
			}
			continue
		}
		var l Language
		if err := yaml.Unmarshal(raw, &l); err != nil {
			s.Notes = append(s.Notes, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if l.Name == "" {
			l.Name = e.Name()
		}
		l.Dir = filepath.Join(dir, e.Name())
		if err := l.Validate(); err != nil {
			s.Notes = append(s.Notes, err.Error())
			continue
		}
		s.langs[l.Name] = &l
	}
	return s, nil
}

// Get returns a language by name.
func (s *Set) Get(name string) (*Language, bool) {
	l, ok := s.langs[name]
	return l, ok
}

// All returns every language, sorted by name.
func (s *Set) All() []*Language {
	out := make([]*Language, 0, len(s.langs))
	for _, l := range s.langs {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Len reports how many plugins loaded.
func (s *Set) Len() int { return len(s.langs) }

// Names returns the loaded plugin names, sorted.
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.langs))
	for n := range s.langs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// TemplateVars are the substitutions a template or scaffolding file may
// use. Scaffolding shares the substitution set with Dockerfiles so a
// plugin author has one thing to learn rather than two.
type TemplateVars struct {
	Version     string
	ProjectName string
}

// RenderDockerfile substitutes placeholders in a plugin's Dockerfile
// template. v1 substituted exactly {{VERSION}} with sed; the same set is
// kept so existing templates render identically. {{PROJECT_NAME}} is
// accepted because v1's scaffolding files use it, and a template that
// borrows the placeholder should not silently keep the literal text.
func RenderDockerfile(template string, vars TemplateVars) string {
	return strings.NewReplacer(
		"{{VERSION}}", vars.Version,
		"{{PROJECT_NAME}}", vars.ProjectName,
	).Replace(template)
}
