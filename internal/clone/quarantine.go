package clone

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mwing/isolated-dev/internal/runner"
)

// Config quarantine: the part of B24 that flags cannot do.
//
// `filter.<driver>.clean` is named by an in-tree `.gitattributes`, so the
// driver name is the attacker's to choose and there is no finite set of
// keys to blank with -c. The only robust answer is for the repository
// config not to be there while host git reads the clone.
//
// The whole design is about what a crash leaves behind. The original is
// renamed aside, never rewritten and never deleted, so the worst outcome
// is a clone whose real config sits next to it under a known name — and
// the next call restores it before doing anything else. That makes the
// recovery path ordinary rather than exceptional: it runs on every use,
// so it is exercised constantly instead of only after the crash nobody
// planned for.

// quarantinedName is where the clone's own config waits. Inside .git,
// which the tool already owns, and named so a human who finds it after a
// crash can see what it is.
const quarantinedName = "config.dev-quarantined"

// minimalConfig is what host git reads instead. Only the settings a
// repository needs to be a repository: nothing that names a program, a
// path, a remote or a filter.
//
// The comment is for whoever finds this file after a crash, in a clone
// that has apparently lost its remote.
const minimalConfig = `# Written by dev while reading this clone from the host.
# The clone's own config is alongside as ` + quarantinedName + ` and is
# restored on the next dev command. To restore it by hand:
#   mv .git/` + quarantinedName + ` .git/config
[core]
	repositoryformatversion = 0
	filemode = true
	bare = false
	logallrefupdates = true
`

// withQuarantinedConfig runs fn while the clone's repository config is set
// aside, restoring it afterwards.
//
// Nested calls are safe: if the config is already quarantined, this is a
// no-op that leaves the outer call to restore.
func withQuarantinedConfig(dir string, fn func() error) (err error) {
	// The layout is checked before anything else, and an unexpected one is
	// refused rather than worked around. Measured: with `.git` replaced by
	// a `gitdir:` pointer, `<clone>/.git/config` does not stat while git
	// itself works perfectly through the pointer — so a version of this
	// that treated "no config here" as "nothing to quarantine" ran host
	// git against a config of the agent's choosing. Failing open in the
	// one function whose whole job is to fail closed.
	if err := checkCloneLayout(dir); err != nil {
		return err
	}

	// Recover from a previous crash before anything else. Doing this on
	// every call, rather than at startup, means the repair happens whoever
	// gets here first and however the tool was entered.
	if err := restoreQuarantine(dir); err != nil {
		return err
	}

	cfg := filepath.Join(dir, ".git", "config")
	saved := filepath.Join(dir, ".git", quarantinedName)

	if fi, serr := os.Lstat(cfg); serr != nil {
		// Genuinely no config file, in a .git that is otherwise a plain
		// directory: there is nothing attacker-controlled to set aside.
		if os.IsNotExist(serr) {
			return fn()
		}
		return fmt.Errorf("reading the clone's config at %s: %w", cfg, serr)
	} else if !fi.Mode().IsRegular() {
		return fmt.Errorf("the clone's config at %s is not a regular file", cfg)
	}
	if err := os.Rename(cfg, saved); err != nil {
		return fmt.Errorf("quarantining the clone's config: %w", err)
	}
	// Restore on the way out however fn returns, including a panic: the
	// alternative is leaving a clone that looks like it lost its remote.
	// The error is reported rather than dropped — a command that succeeded
	// while leaving the clone quarantined has not succeeded.
	defer func() {
		if rerr := restoreQuarantine(dir); rerr != nil && err == nil {
			err = rerr
		}
	}()

	if err := os.WriteFile(cfg, []byte(minimalConfig), 0o600); err != nil {
		return fmt.Errorf("writing a safe config for the clone: %w", err)
	}
	return fn()
}

// restoreQuarantine puts a set-aside config back, and is a no-op when
// there is none. It is idempotent by construction: rename over whatever
// the tool wrote.
func restoreQuarantine(dir string) error {
	saved := filepath.Join(dir, ".git", quarantinedName)
	if _, err := os.Stat(saved); err != nil {
		return nil
	}
	cfg := filepath.Join(dir, ".git", "config")
	// Only ever overwrite the config this package wrote. Anything else is
	// a config someone put there while the original was aside, and losing
	// it would be the data loss this whole mechanism exists to avoid.
	if body, err := os.ReadFile(cfg); err == nil && !isToolWritten(body) {
		return fmt.Errorf(
			"the clone at %s has both a config and a set-aside %s, and the config "+
				"is not one dev wrote — refusing to choose between them", dir, quarantinedName)
	}
	return os.Rename(saved, cfg)
}

func isToolWritten(body []byte) bool {
	return strings.HasPrefix(string(body), "# Written by dev while reading this clone")
}

// checkCloneLayout refuses anything but a plain .git directory.
//
// A symlink or a `gitdir:` pointer file means the repository this command
// is about to read is somewhere the tool did not put it, chosen by whoever
// wrote to the clone. That is a finding to report, not a shape to
// accommodate.
func checkCloneLayout(dir string) error {
	gitDir := filepath.Join(dir, ".git")
	fi, err := os.Lstat(gitDir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", gitDir, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, not a repository directory", gitDir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory — a `gitdir:` pointer names a "+
			"repository somewhere this tool did not put it", gitDir)
	}
	return nil
}

// Read runs git inside a clone with the package's hardening and the
// clone's own config set aside, and returns its output.
//
// Exported because `internal/cli` had grown its own `cloneGit` that
// forwarded straight to git: `dev clone diff` ran unhardened against a
// repository an agent had been writing to. Two ways to read a clone is one
// way too many, which is the argument this package already makes about
// everything else.
func Read(ctx context.Context, run runner.Runner, clonePath string,
	args ...string) (string, error) {
	return cloneGitOutput(ctx, run, clonePath, args...)
}

// WhileQuarantined runs fn with the clone's config set aside, for
// operations that read the clone from outside it.
//
// `git fetch <clone>` is the case: it runs in the project, so none of the
// flags this package adds apply to it, and it starts `upload-pack` inside
// the clone — which reads the clone's config, where
// `uploadpack.packObjectsHook` names a program to run on the host. The
// hardening has to follow the repository being read, not the directory the
// command runs in.
func WhileQuarantined(clonePath string, fn func() error) error {
	return withQuarantinedConfig(clonePath, fn)
}
