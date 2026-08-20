package clone

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
func withQuarantinedConfig(dir string, fn func() error) error {
	// Recover from a previous crash before anything else. Doing this on
	// every call, rather than at startup, means the repair happens whoever
	// gets here first and however the tool was entered.
	if err := restoreQuarantine(dir); err != nil {
		return err
	}

	cfg := filepath.Join(dir, ".git", "config")
	saved := filepath.Join(dir, ".git", quarantinedName)

	if _, err := os.Stat(cfg); err != nil {
		// No config to quarantine — including the case where .git is a
		// file rather than a directory, which is an anomaly the caller
		// reports rather than something to work around here.
		return fn()
	}
	if err := os.Rename(cfg, saved); err != nil {
		return fmt.Errorf("quarantining the clone's config: %w", err)
	}
	// Restore on the way out however fn returns, including a panic: the
	// alternative is leaving a clone that looks like it lost its remote.
	defer func() { _ = restoreQuarantine(dir) }()

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
