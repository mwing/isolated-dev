// Package scaffold creates a new project from a language plugin.
//
// The files come from the plugin rather than from this code, so adding a
// language means adding a directory — the same property that makes
// detection and Dockerfile templates data rather than code.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mwing/isolated-dev/go/internal/langs"
)

// File is one file to create.
type File struct {
	// Path is relative to the project directory.
	Path string
	Body string
}

// Plan is what a scaffold would do, computed before anything is written
// so a conflict stops the whole operation rather than half of it.
type Plan struct {
	Dir      string
	Language string
	Version  string
	Files    []File
	// Missing are files the plugin declares but does not ship. Reported
	// rather than invented: content this tool made up would be a surprise
	// attributed to the plugin.
	Missing []string
	// Conflicts are paths that already exist.
	Conflicts []string
}

// Vars are the substitutions applied to scaffolding files.
type Vars struct {
	ProjectName string
	Version     string
}

// Build computes the plan for a language in a directory.
func Build(l *langs.Language, dir string, vars Vars) (*Plan, error) {
	p := &Plan{Dir: dir, Language: l.Name, Version: vars.Version}

	names := l.Files.Scaffolding
	if len(names) == 0 {
		return nil, fmt.Errorf("scaffold: %s declares no scaffolding files", l.Name)
	}

	for _, name := range names {
		src := filepath.Join(l.Dir, name)
		raw, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				p.Missing = append(p.Missing, name)
				continue
			}
			return nil, fmt.Errorf("scaffold: reading %s: %w", src, err)
		}
		body := langs.RenderDockerfile(string(raw), langs.TemplateVars{
			Version:     vars.Version,
			ProjectName: vars.ProjectName,
		})
		p.Files = append(p.Files, File{Path: name, Body: body})

		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			p.Conflicts = append(p.Conflicts, name)
		}
	}
	sort.Strings(p.Conflicts)
	sort.Strings(p.Missing)
	return p, nil
}

// Apply writes the planned files.
//
// It refuses to overwrite unless told to: scaffolding into a directory
// that already has work in it is far more often a mistake than an
// intention, and the mistake is unrecoverable.
func (p *Plan) Apply(force bool) error {
	if len(p.Conflicts) > 0 && !force {
		return fmt.Errorf("scaffold: %s already exists in %s; "+
			"use --force to overwrite", strings.Join(p.Conflicts, ", "), p.Dir)
	}
	for _, f := range p.Files {
		target := filepath.Join(p.Dir, f.Path)
		// Scaffolding may nest, e.g. src/main.rs.
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(f.Body), 0o644); err != nil {
			return fmt.Errorf("scaffold: writing %s: %w", target, err)
		}
	}
	return nil
}

// Paths lists what the plan creates, for display.
func (p *Plan) Paths() []string {
	out := make([]string, 0, len(p.Files))
	for _, f := range p.Files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}
