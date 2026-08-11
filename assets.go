// Package assets carries the parts of this tool that are not the binary.
//
// A release binary used to be unable to produce either of them. The egress
// sidecar could only be built by `make proxy-image` from a checkout, and it
// is mandatory for every filtered run; the language plugins were written
// only by v1's installer, so a machine with just this tool detected nothing
// and scaffolded nothing. Both were invisible during development, because a
// developer always has the repository.
//
// Embedding them means the binary is the whole product. It also makes
// version skew detectable: the sidecar image is labelled with the hash of
// the source it was built from, so an image built by an older binary can be
// recognized rather than trusted.
package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Languages are the language plugins, installed into ~/.dev-envs/languages.
//
//go:embed all:languages
var Languages embed.FS

// ProxySource is everything `docker build` needs to produce the sidecar:
// the module, its source, and the Dockerfile. The build runs inside a
// golang image, so the host needs no toolchain and no checkout.
//
//go:embed all:cmd all:internal go.mod go.sum Dockerfile.proxy
var ProxySource embed.FS

// SourceHash identifies the sidecar source this binary carries.
//
// It is the version marker for the image: two binaries built from the same
// source agree, and a binary whose proxy has changed does not accept an
// image built before the change. Names and contents both feed it, so a
// file that is added or removed counts as a change.
func SourceHash() string {
	sum := sha256.New()
	paths := sortedPaths(ProxySource)
	for _, p := range paths {
		b, err := ProxySource.ReadFile(p)
		if err != nil {
			continue
		}
		sum.Write([]byte(p))
		sum.Write([]byte{0})
		sum.Write(b)
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

// LanguagesHash identifies the plugin set this binary carries, so a stale
// installation can be reported rather than assumed current.
func LanguagesHash() string {
	sum := sha256.New()
	for _, p := range sortedPaths(Languages) {
		b, err := Languages.ReadFile(p)
		if err != nil {
			continue
		}
		sum.Write([]byte(p))
		sum.Write([]byte{0})
		sum.Write(b)
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

func sortedPaths(fsys embed.FS) []string {
	var out []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	return out
}

// WriteProxySource materializes the sidecar's build context into dir.
func WriteProxySource(dir string) error {
	return writeAll(ProxySource, ".", dir)
}

// WriteLanguages installs the plugin set into dir.
//
// Existing files are left alone unless force is set: a user may have edited
// a plugin, and silently reverting that would be the tool overwriting work
// it did not create. The returned list is what was written.
func WriteLanguages(dir string, force bool) ([]string, error) {
	var written []string
	err := fs.WalkDir(Languages, "languages", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "languages/")
		target := filepath.Join(dir, rel)
		if !force {
			if _, statErr := os.Stat(target); statErr == nil {
				return nil
			}
		}
		b, err := Languages.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, b, 0o644); err != nil {
			return err
		}
		written = append(written, rel)
		return nil
	})
	return written, err
}

func writeAll(fsys embed.FS, root, dir string) error {
	return fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fsys.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
