// Package mediafs manages the folders that back ViewDock libraries: it
// validates requested library paths against the configured storage roots,
// creates missing folders with the right owner and mode, and repairs folder
// ownership at container start while the entrypoint still runs as root.
//
// Nothing here opens the database, so the privileged startup step never
// creates files that the unprivileged application later cannot write.
package mediafs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode"
)

// DirMode is the mode of every folder ViewDock creates: the service account
// can write, everyone else (other containers sharing the mount) can read.
const DirMode os.FileMode = 0o755

var (
	ErrInvalidPath   = errors.New("invalid library folder")
	ErrOutsideRoots  = errors.New("library folder must be inside the media storage folder")
	ErrSymlinkEscape = errors.New("library folder resolves through a link that leaves the media storage folder")
	ErrNotDirectory  = errors.New("library folder exists but is not a folder")
	ErrNotWritable   = errors.New("library folder is not writable")
	ErrNoRoots       = errors.New("no media storage folder is configured")
	ErrSystemFolder  = errors.New("library folder cannot be a system folder of the host")
)

// hostSystemDirs are host folders a library may not live in: the system
// itself, and folders that are not real storage.
var hostSystemDirs = []string{"bin", "boot", "dev", "etc", "lib", "lib32", "lib64", "libx32", "proc", "run", "sbin", "sys", "usr", "var/lib/docker"}

// Roots is the set of storage roots library folders may live under and the
// account that should own folders ViewDock creates.
type Roots struct {
	// Paths are the configured roots; the first is the default location for
	// new libraries (VD_MEDIA_DIR).
	Paths []string
	// UID and GID own new folders when the process runs as root. When the
	// process runs unprivileged the folders are owned by it already.
	UID, GID int
	// HostRoot is where the host's file system is mounted in the container
	// (/host), or "" when it is not. An absolute path outside the roots is
	// then a folder of the host: /root/media/movies is /host/root/media/movies.
	HostRoot string
}

// MountedHostRoot returns dir when the host's file system is mounted
// there (it has the host's /etc), and "" otherwise.
func MountedHostRoot(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || runtime.GOOS == "windows" {
		return ""
	}
	if st, err := os.Stat(filepath.Join(dir, "etc")); err != nil || !st.IsDir() {
		return ""
	}
	return filepath.Clean(dir)
}

// HostPath is the host's own path for a folder under HostRoot, or "" for
// any other folder.
func (r Roots) HostPath(p string) string {
	if r.HostRoot == "" || p == "" {
		return ""
	}
	root := filepath.Clean(r.HostRoot)
	clean := filepath.Clean(p)
	if !within(root, clean) {
		return ""
	}
	rel, err := filepath.Rel(root, clean)
	if err != nil {
		return ""
	}
	if rel == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

// hostTarget maps an absolute host path to the container, refusing the
// host's system folders.
func (r Roots) hostTarget(requested string) (string, error) {
	root := filepath.Clean(r.HostRoot)
	clean := filepath.Clean(requested)
	if within(root, clean) {
		clean = r.HostPath(clean)
	}
	rel := strings.TrimPrefix(filepath.ToSlash(clean), "/")
	if rel == "" {
		return "", fmt.Errorf("%w: /", ErrSystemFolder)
	}
	for _, sys := range hostSystemDirs {
		if rel == sys || strings.HasPrefix(rel, sys+"/") {
			return "", fmt.Errorf("%w: /%s", ErrSystemFolder, sys)
		}
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// Default returns the root new libraries are created under.
func (r Roots) Default() (string, error) {
	if len(r.Paths) == 0 || strings.TrimSpace(r.Paths[0]) == "" {
		return "", ErrNoRoots
	}
	return filepath.Abs(r.Paths[0])
}

// FolderName turns a library name into a single safe folder name. It keeps
// letters, digits, spaces and common punctuation and drops separators,
// control characters and leading dots, so "Kids Movies" stays "Kids Movies".
func FolderName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == 0:
			b.WriteRune(' ')
		case unicode.IsControl(r):
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || strings.ContainsRune("-_.,'()&+![]", r):
			b.WriteRune(r)
		}
	}
	var words []string
	for _, w := range strings.Fields(b.String()) {
		if strings.Trim(w, ".") != "" {
			words = append(words, w)
		}
	}
	out := strings.TrimLeft(strings.Join(words, " "), ".")
	out = strings.TrimSpace(out)
	if len(out) > 120 {
		out = strings.TrimSpace(out[:120])
	}
	if out == "" || out == "." || out == ".." {
		return "Library"
	}
	return out
}

// Resolve validates a requested library folder and returns its absolute,
// cleaned path. The folder does not need to exist yet.
//
//   - "" places the library in a folder named after the library under the
//     default root (/media/Kids Movies).
//   - A relative path ("movies", "kids/movies") is taken relative to the
//     default root and may not contain "..".
//   - An absolute path inside one of the roots stays there, after following
//     any links in the part of the path that already exists.
//   - Any other absolute path is a folder of the host when the host's file
//     system is mounted (HostRoot): /root/media/movies.
func (r Roots) Resolve(requested, libraryName string) (string, error) {
	requested = strings.TrimSpace(requested)
	if strings.ContainsFunc(requested, func(c rune) bool { return c == 0 || unicode.IsControl(c) }) {
		return "", fmt.Errorf("%w: the path contains control characters", ErrInvalidPath)
	}
	if runtime.GOOS != "windows" && looksLikeWindowsPath(requested) {
		return "", fmt.Errorf("%w: that is a Windows path; ViewDock stores media under its media folder, leave the folder empty to let it choose", ErrInvalidPath)
	}
	def, err := r.Default()
	if err != nil {
		return "", err
	}
	var target string
	switch {
	case requested == "":
		target = filepath.Join(def, FolderName(libraryName))
	case !filepath.IsAbs(requested):
		for _, part := range strings.Split(filepath.ToSlash(requested), "/") {
			if part == ".." {
				return "", fmt.Errorf("%w: \"..\" is not allowed", ErrInvalidPath)
			}
		}
		target = filepath.Join(def, requested)
	default:
		for _, part := range strings.Split(filepath.ToSlash(requested), "/") {
			if part == ".." {
				return "", fmt.Errorf("%w: \"..\" is not allowed", ErrInvalidPath)
			}
		}
		target = filepath.Clean(requested)
		if r.HostRoot != "" && !r.inConfiguredRoots(target) {
			if target, err = r.hostTarget(target); err != nil {
				return "", err
			}
		}
	}
	if _, err := r.rootFor(target); err != nil {
		return "", err
	}
	return target, nil
}

// inConfiguredRoots reports whether target is lexically inside a root other
// than the host mount.
func (r Roots) inConfiguredRoots(target string) bool {
	for _, root := range r.Paths {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil && within(abs, target) {
			return true
		}
	}
	return false
}

// rootFor returns the configured root that contains target, checking both
// the lexical path and the path with existing links followed.
func (r Roots) rootFor(target string) (string, error) {
	roots := r.Paths
	if r.HostRoot != "" {
		roots = append(append([]string{}, r.Paths...), r.HostRoot)
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if !within(absRoot, target) {
			continue
		}
		evalRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			return "", fmt.Errorf("media storage folder %s is not available: %w", absRoot, err)
		}
		evalTarget, err := evalExistingPrefix(target)
		if err != nil {
			return "", err
		}
		if !within(evalRoot, evalTarget) {
			return "", ErrSymlinkEscape
		}
		return absRoot, nil
	}
	if r.HostRoot != "" {
		return "", fmt.Errorf("%w (%s, or a folder of the host such as /srv/media)", ErrOutsideRoots, strings.Join(r.Paths, ", "))
	}
	return "", fmt.Errorf("%w (%s)", ErrOutsideRoots, strings.Join(r.Paths, ", "))
}

// Contains reports whether target (after following links) is inside one of
// the roots. It is the check the mover and the library service use before
// writing anywhere.
func (r Roots) Contains(target string) bool {
	_, err := r.rootFor(target)
	return err == nil
}

// EnsureDir creates dir and any missing parents below its storage root,
// gives new folders DirMode and, when running as root, the configured owner,
// and then proves the folder is writable. It returns whether dir was created.
// It never changes folders that already exist.
func (r Roots) EnsureDir(dir string) (bool, error) {
	root, err := r.rootFor(dir)
	if err != nil {
		return false, err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return false, fmt.Errorf("media storage folder %s is missing", root)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false, ErrOutsideRoots
	}
	created := false
	cur := root
	if rel != "." {
		for _, part := range strings.Split(rel, string(os.PathSeparator)) {
			cur = filepath.Join(cur, part)
			st, err := os.Lstat(cur)
			switch {
			case err == nil && st.Mode()&os.ModeSymlink != 0:
				// A link is fine only while it stays inside the root.
				if _, err := r.rootFor(cur); err != nil {
					return created, err
				}
				if st, err := os.Stat(cur); err != nil || !st.IsDir() {
					return created, ErrNotDirectory
				}
			case err == nil && !st.IsDir():
				return created, fmt.Errorf("%w: %s", ErrNotDirectory, cur)
			case err == nil:
			case errors.Is(err, os.ErrNotExist):
				if err := os.Mkdir(cur, DirMode); err != nil && !errors.Is(err, os.ErrExist) {
					return created, describeMkdirErr(cur, err)
				}
				created = true
				if err := r.own(cur); err != nil {
					return created, err
				}
			default:
				return created, err
			}
		}
	}
	// Re-check after creation: nothing may have swapped a component for a
	// link that leaves the root.
	if _, err := r.rootFor(dir); err != nil {
		return created, err
	}
	if err := CheckWritable(dir); err != nil {
		return created, err
	}
	return created, nil
}

// Adopt gives a folder ViewDock created by other means (a copied folder)
// the same mode and owner as folders made by EnsureDir.
func (r Roots) Adopt(path string) error { return r.own(path) }

// own applies DirMode (independent of the umask) and, when running as root,
// the configured owner to a folder ViewDock just created.
func (r Roots) own(path string) error {
	if err := os.Chmod(path, DirMode); err != nil {
		return err
	}
	if runtime.GOOS != "windows" && os.Geteuid() == 0 && r.UID > 0 {
		if err := os.Lchown(path, r.UID, r.GID); err != nil {
			return fmt.Errorf("could not give %s to the ViewDock account: %w", path, err)
		}
	}
	return nil
}

// MkdirOwned is EnsureDir for folders below an already validated library
// root (a movie or show folder during a move). It returns the folders it
// created, deepest last, so a failed move can remove exactly those.
func (r Roots) MkdirOwned(dir string) ([]string, error) {
	var missing []string
	cur := filepath.Clean(dir)
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	var made []string
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], DirMode); err != nil && !errors.Is(err, os.ErrExist) {
			return made, describeMkdirErr(missing[i], err)
		}
		made = append(made, missing[i])
		if err := r.own(missing[i]); err != nil {
			return made, err
		}
	}
	return made, nil
}

// CheckWritable proves the current process can create and remove a file in
// dir, which is what uploads and moves need.
func CheckWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".vd-write-*")
	if err != nil {
		return fmt.Errorf("%w: %s (%s)", ErrNotWritable, dir, permHint(dir, err))
	}
	name := f.Name()
	_ = f.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("%w: %s", ErrNotWritable, dir)
	}
	return nil
}

// Explain adds what to do on the host when a folder of the host cannot be
// created or written.
func (r Roots) Explain(target string, err error) error {
	host := r.HostPath(target)
	if err == nil || host == "" || !(errors.Is(err, ErrNotWritable) || errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "permission denied")) {
		return err
	}
	uid, gid := r.UID, r.GID
	if uid <= 0 {
		uid, gid = os.Getuid(), os.Getgid()
	}
	return fmt.Errorf("%w. ViewDock runs as %d:%d; on the host, run chown %d:%d %s and make sure every folder above it can be entered (for a folder inside /root: chmod o+x /root)", err, uid, gid, uid, gid, host)
}

func describeMkdirErr(path string, err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("could not create %s: %s", path, permHint(filepath.Dir(path), err))
	}
	return fmt.Errorf("could not create %s: %w", path, err)
}

func permHint(dir string, err error) string {
	if !errors.Is(err, os.ErrPermission) {
		if errors.Is(err, os.ErrNotExist) {
			return "the folder does not exist"
		}
		return err.Error()
	}
	owner := ownerOf(dir)
	if owner != "" {
		return "permission denied; the folder is owned by " + owner + " and ViewDock repairs ownership of its media folders when the container starts, so restart ViewDock once"
	}
	return "permission denied; restart ViewDock once so it can repair folder ownership"
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// evalExistingPrefix follows links in the longest existing prefix of p and
// appends the rest unchanged.
func evalExistingPrefix(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rest := ""
	cur := abs
	for {
		if ev, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return ev, nil
			}
			return filepath.Join(ev, rest), nil
		} else if errors.Is(err, syscall.ENOTDIR) {
			return "", fmt.Errorf("%w: %s", ErrNotDirectory, p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		if rest == "" {
			rest = filepath.Base(cur)
		} else {
			rest = filepath.Join(filepath.Base(cur), rest)
		}
		cur = parent
	}
}

func looksLikeWindowsPath(p string) bool {
	if len(p) >= 2 && p[1] == ':' && unicode.IsLetter(rune(p[0])) {
		return true
	}
	return strings.HasPrefix(p, `\\`)
}
