package library

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	ErrNotContained = errors.New("path is not inside library root")
	ErrNotDirectory = errors.New("root path is not a directory")
)

// ResolveRoot returns the absolute, symlink-evaluated directory path.
func looksLikeHostWindowsPath(p string) bool {
	p = strings.TrimSpace(p)
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		c := p[1]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	return false
}

func ResolveRoot(rootPath string) (string, error) {
	if runtime.GOOS != "windows" && looksLikeHostWindowsPath(rootPath) {
		return "", errors.New("that is a Windows path; inside Docker use the mounted folder /media (leave the field empty to use it automatically)")
	}
	abs, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", ErrNotDirectory
	}
	return resolved, nil
}

// ContainsPath reports whether target lives under root using EvalSymlinks
// and filepath.Rel — not a string prefix. Rejects any Rel that escapes (`..`).
func ContainsPath(root, target string) error {
	rootEval, err := evalExisting(root)
	if err != nil {
		return err
	}
	targetEval, err := evalBest(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootEval, targetEval)
	if err != nil {
		return ErrNotContained
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ErrNotContained
	}
	if filepath.IsAbs(rel) {
		return ErrNotContained
	}
	return nil
}

func evalExisting(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// evalBest evaluates symlinks when the path exists; otherwise evaluates the
// nearest existing ancestor and joins the remainder (for upload destinations).
func evalBest(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		return ev, nil
	}
	rest := filepath.Base(abs)
	dir := filepath.Dir(abs)
	for dir != filepath.Dir(dir) {
		if ev, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(ev, rest), nil
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = filepath.Dir(dir)
	}
	if ev, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(ev, rest), nil
	}
	return abs, nil
}
