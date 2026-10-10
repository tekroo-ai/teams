package agenttools

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// APFS shares blocks, not writable inodes. Test writes cannot poison the seed.
// A filesystem without cloning falls back to the independent byte-copy path.
func cloneCacheFile(source, destination string) (bool, error) {
	err := unix.Clonefile(source, destination, unix.CLONE_NOFOLLOW)
	if err == nil {
		return true, os.Chmod(destination, 0600)
	}
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOSYS) {
		return false, nil
	}
	return false, err
}
