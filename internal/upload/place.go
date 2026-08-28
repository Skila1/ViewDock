package upload

import (
	"io"
	"os"
	"path/filepath"
)

func atomicPlace(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dest); err == nil {
		return syncDir(filepath.Dir(dest))
	}
	if err := copyFile(src, dest); err != nil {
		_ = os.Remove(dest)
		return err
	}
	_ = os.Remove(src)
	return syncDir(filepath.Dir(dest))
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyBuffer(out, in, make([]byte, CopyBuf))
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func fsyncPath(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	_ = f.Close()
	return err
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	_ = f.Sync()
	return f.Close()
}
