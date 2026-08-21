package clone

import (
	"fmt"
	"os"
	"syscall"
)

// Lock takes a clone for the lifetime of a run, and returns the release.
//
// A clone is one mutable working tree per project and nothing used to
// serialize access to it, so two runs of the same project mounted the same
// tree, index and refs at once. Neither run is doing anything wrong; the
// pair is the defect — and the loser is whichever container was writing
// when the other one moved HEAD out from under it.
//
// flock rather than a lock file this package creates and removes: the
// kernel releases it when the process dies, so a killed run does not leave
// a clone locked forever. A crash is the case that matters, since it is
// the one nobody cleans up after.
//
// The lock file sits beside the clone rather than inside it, for the same
// reason the provenance does: inside is inside the bind mount, and a lock
// an agent can delete is not a lock.
func Lock(clonePath string) (func(), error) {
	path := clonePath + ".lock"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("clone: opening the lock for %s: %w", clonePath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("clone: %s is in use by another run — "+
			"two runs writing one working tree would overwrite each other's work",
			clonePath)
	}

	var released bool
	return func() {
		if released {
			return
		}
		released = true
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
