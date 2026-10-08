package library

import (
	"errors"
	"os"
)

// renameChecked refuses an existing destination, then renames. It is the
// fallback where the kernel cannot refuse replacement itself.
func renameChecked(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrExist}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(src, dst)
}
