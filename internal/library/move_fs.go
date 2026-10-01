package library

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// ErrDestinationExists is returned instead of ever replacing an existing
// file or folder at a move destination.
var ErrDestinationExists = errors.New("destination already exists")

// fsOp is one rename in a move: a whole title folder, or one file.
type fsOp struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
	Dir bool   `json:"dir,omitempty"`
}

// renameEntry is renameNoReplace; tests swap it to force the
// cross-filesystem copy path.
var renameEntry = renameNoReplace

// tmpName is where a cross-filesystem copy is assembled before it is
// renamed into place. It is derived from the journal row so recovery after
// a crash knows which partial copy to delete.
func tmpName(dst, itemID string, idx int) string {
	return filepath.Join(filepath.Dir(dst), fmt.Sprintf(".vd-move-%s-%d.partial", itemID, idx))
}

// moveEntry moves src (a file or folder) to dst without ever replacing an
// existing dst. On one filesystem it is a single rename. Across filesystems
// it copies to tmp next to dst, verifies the copy, renames it into place and
// only then removes src. A failed copy leaves src untouched and removes tmp.
func (s *Service) moveEntry(src, dst, tmp string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrDestinationExists, dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	err := renameEntry(src, dst)
	if err == nil {
		syncDir(filepath.Dir(dst))
		syncDir(filepath.Dir(src))
		return nil
	}
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %s", ErrDestinationExists, dst)
	}
	if !isCrossDevice(err) {
		return err
	}
	_ = os.RemoveAll(tmp)
	if err := s.copyTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("copy to the other drive failed: %w", err)
	}
	if err := verifyCopy(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := renameNoReplace(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", ErrDestinationExists, dst)
		}
		return err
	}
	syncDir(filepath.Dir(dst))
	if err := os.RemoveAll(src); err != nil {
		return &leftoverError{Path: src, Err: err}
	}
	return nil
}

// leftoverError means the move itself succeeded but the original could not
// be fully removed afterwards.
type leftoverError struct {
	Path string
	Err  error
}

func (e *leftoverError) Error() string {
	return fmt.Sprintf("moved, but the original at %s could not be removed: %v", e.Path, e.Err)
}

func isCrossDevice(err error) bool {
	var le *os.LinkError
	if errors.As(err, &le) {
		err = le.Err
	}
	return errors.Is(err, syscall.EXDEV)
}

// copyTree copies a regular file or a folder of regular files and folders.
// Links and special files are refused; the planner never selects them.
func (s *Service) copyTree(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if st.Mode().IsRegular() {
		return s.copyFile(src, dst, st)
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a regular file or folder", src)
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := os.Mkdir(target, 0o755); err != nil {
				return err
			}
			if err := s.Storage.Adopt(target); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			return s.copyFile(path, target, info)
		default:
			return fmt.Errorf("%s is not a regular file or folder", path)
		}
	})
}

func (s *Service) copyFile(src, dst string, st os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, st.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyBuffer(out, in, make([]byte, 1<<20))
	syncErr := out.Sync()
	closeErr := out.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if runtime.GOOS != "windows" && os.Geteuid() == 0 && s.Storage.UID > 0 {
		_ = os.Lchown(dst, s.Storage.UID, s.Storage.GID)
	}
	_ = os.Chtimes(dst, st.ModTime(), st.ModTime())
	return nil
}

// verifyCopy checks that every file under src exists under dst with the
// same size before the original is deleted.
func verifyCopy(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		a, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil || !b.Mode().IsRegular() || a.Size() != b.Size() {
			return fmt.Errorf("copy of %s did not verify", path)
		}
		return nil
	})
}

func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	_ = f.Close()
}

// pruneEmptyDirs removes dir and then its parents while they are empty,
// stopping at (and never removing) stop.
func pruneEmptyDirs(dir, stop string) {
	stop = filepath.Clean(stop)
	for dir = filepath.Clean(dir); dir != stop && within(stop, dir); dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			return
		}
	}
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != "." && rel != ".." && !startsWithDotDot(rel)
}

func startsWithDotDot(rel string) bool {
	return len(rel) >= 3 && rel[:2] == ".." && os.IsPathSeparator(rel[2])
}
