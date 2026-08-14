package container

import (
	"fmt"
	"os"
)

// FallbackUID is used where the host has no meaningful uid to copy —
// Windows, or a platform where Getuid reports -1. It is the value every
// image used to bake unconditionally.
const FallbackUID = 1000

// HostUser is the uid:gid a workload runs as: the uid of the person who ran
// dev.
//
// Not a fixed 1000. A bind mount on Linux carries host ownership through
// unchanged, so a container running as anyone else cannot write to the
// workspace — `sh: can't create file: Permission denied` on the very first
// write — and anything it did write would not be editable afterwards.
// macOS remaps ownership in its file-sharing layer and hides both halves,
// which is how this survived: the platform most of the development happens
// on is the one platform that cannot show it.
//
// The image is built for the same uid (see the DEV_UID build arg), so the
// account exists inside the container and owns its own home and caches.
// Passing a uid the image does not know is worse than the original bug: the
// workspace works and everything else stops.
func HostUser() string {
	uid, gid := os.Getuid(), os.Getgid()
	if uid < 0 || gid < 0 {
		return fmt.Sprintf("%d:%d", FallbackUID, FallbackUID)
	}
	return fmt.Sprintf("%d:%d", uid, gid)
}

// HostUID is the uid alone, for the build argument and the image tag.
func HostUID() int {
	if uid := os.Getuid(); uid >= 0 {
		return uid
	}
	return FallbackUID
}

// HostGID is the gid alone.
func HostGID() int {
	if gid := os.Getgid(); gid >= 0 {
		return gid
	}
	return FallbackUID
}

// UIDBuildArgs are the build arguments every project image takes, so the
// account inside it matches the one the run will use.
func UIDBuildArgs() map[string]string {
	return map[string]string{
		"DEV_UID": fmt.Sprint(HostUID()),
		"DEV_GID": fmt.Sprint(HostGID()),
	}
}
