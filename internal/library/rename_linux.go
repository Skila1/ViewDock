//go:build linux

package library

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames atomically and fails with os.ErrExist rather than
// replacing an existing destination, closing the gap between checking the
// destination and renaming onto it.
func renameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrExist}
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EOPNOTSUPP):
		// Filesystems without RENAME_NOREPLACE (some FUSE and network mounts).
		return renameChecked(src, dst)
	}
	return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
}
