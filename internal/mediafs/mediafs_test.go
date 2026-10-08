package mediafs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRoots(t *testing.T) (Roots, string) {
	t.Helper()
	root := t.TempDir()
	ev, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return Roots{Paths: []string{ev}, UID: 4242, GID: 4343}, ev
}

func TestFolderName(t *testing.T) {
	cases := map[string]string{
		"Movies":          "Movies",
		"Kids Movies":     "Kids Movies",
		"  Anime  ":       "Anime",
		"../../etc":       "etc",
		"a/b\\c":          "a b c",
		"...":             "Library",
		"":                "Library",
		"Tom & Jerry's":   "Tom & Jerry's",
		"bad\x00name\x07": "bad name",
	}
	for in, want := range cases {
		if got := FolderName(in); got != want {
			t.Errorf("FolderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	r, root := testRoots(t)
	got, err := r.Resolve("", "Kids Movies")
	if err != nil || got != filepath.Join(root, "Kids Movies") {
		t.Fatalf("empty path: %q %v", got, err)
	}
	got, err = r.Resolve("shows/anime", "x")
	if err != nil || got != filepath.Join(root, "shows", "anime") {
		t.Fatalf("relative: %q %v", got, err)
	}
	got, err = r.Resolve(filepath.Join(root, "Archive"), "x")
	if err != nil || got != filepath.Join(root, "Archive") {
		t.Fatalf("absolute inside: %q %v", got, err)
	}
	if got, err := r.Resolve(root, "x"); err != nil || got != root {
		t.Fatalf("root itself: %q %v", got, err)
	}
	for _, bad := range []string{"../escape", "a/../../escape", filepath.Join(root, "..", "escape"), "/etc", "/", "C:\\Media", "\\\\nas\\share", "a\x00b"} {
		if _, err := r.Resolve(bad, "x"); err == nil {
			t.Errorf("Resolve(%q) should fail", bad)
		}
	}
	if _, err := r.Resolve("/etc", "x"); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("outside root error = %v", err)
	}
}

func TestResolveRejectsSymlinkEscape(t *testing.T) {
	r, root := testRoots(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if _, err := r.Resolve("link/movies", "x"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("expected symlink escape, got %v", err)
	}
	if _, err := r.EnsureDir(filepath.Join(root, "link", "movies")); err == nil {
		t.Fatal("EnsureDir must refuse a link that leaves the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "movies")); err == nil {
		t.Fatal("nothing may be created outside the root")
	}
	// A link that stays inside the root is fine.
	if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "inner")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EnsureDir(filepath.Join(root, "inner", "movies")); err != nil {
		t.Fatalf("inner link: %v", err)
	}
}

func TestEnsureDirCreatesWritableFolders(t *testing.T) {
	r, root := testRoots(t)
	for _, name := range []string{"Movies", "Shows", "Mixed", "Kids Movies", filepath.Join("deep", "er", "Archive")} {
		dir := filepath.Join(root, name)
		created, err := r.EnsureDir(dir)
		if err != nil || !created {
			t.Fatalf("%s: created=%v err=%v", name, created, err)
		}
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			t.Fatalf("%s missing: %v", name, err)
		}
		if st.Mode().Perm() != DirMode {
			t.Fatalf("%s mode %v, want %v", name, st.Mode().Perm(), DirMode)
		}
		if err := os.WriteFile(filepath.Join(dir, "probe.mkv"), []byte("x"), 0o644); err != nil {
			t.Fatalf("%s not writable: %v", name, err)
		}
		if os.Geteuid() == 0 {
			if got := ownerOf(dir); got != "4242:4343" {
				t.Fatalf("%s owner %s, want 4242:4343", name, got)
			}
		}
	}
	created, err := r.EnsureDir(filepath.Join(root, "Movies"))
	if err != nil || created {
		t.Fatalf("existing folder: created=%v err=%v", created, err)
	}
}

func TestEnsureDirRejectsFileInPath(t *testing.T) {
	r, root := testRoots(t)
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EnsureDir(filepath.Join(root, "file", "Movies")); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("expected ErrNotDirectory, got %v", err)
	}
}

func TestMkdirOwnedReportsCreatedFolders(t *testing.T) {
	r, root := testRoots(t)
	made, err := r.MkdirOwned(filepath.Join(root, "Show", "Season 01"))
	if err != nil || len(made) != 2 {
		t.Fatalf("made=%v err=%v", made, err)
	}
	if made[0] != filepath.Join(root, "Show") || made[1] != filepath.Join(root, "Show", "Season 01") {
		t.Fatalf("order: %v", made)
	}
}

func TestRepairFixesRootOwnedFoldersOnly(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("ownership repair needs root")
	}
	root := t.TempDir()
	movies := filepath.Join(root, "movies")
	shows := filepath.Join(root, "shows", "Show", "Season 01")
	other := filepath.Join(root, "other")
	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	for _, d := range []string{movies, shows, other, deep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(other, 5555, 5555); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(movies, "Film (2000).mkv")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := Repair([]string{root}, 1000, 1000, DefaultRepairDepth)
	if rep.Fixed == 0 || len(rep.Failed) != 0 {
		t.Fatalf("report %+v", rep)
	}
	for _, d := range []string{root, movies, shows} {
		if got := ownerOf(d); got != "1000:1000" {
			t.Fatalf("%s owner %s", d, got)
		}
	}
	if got := ownerOf(other); got != "5555:5555" {
		t.Fatalf("folder owned by another account changed: %s", got)
	}
	if got := ownerOf(file); got != "0:0" {
		t.Fatalf("files must not change: %s", got)
	}
	if got := ownerOf(deep); got != "0:0" {
		t.Fatalf("folders deeper than the limit must not change: %s", got)
	}
	again := Repair([]string{root}, 1000, 1000, DefaultRepairDepth)
	if again.Fixed != 0 {
		t.Fatalf("second pass should be a no-op: %+v", again)
	}
}

// hostRoots mounts a fake host file system next to the media folder.
func hostRoots(t *testing.T) (Roots, string, string) {
	t.Helper()
	r, media := testRoots(t)
	host := t.TempDir()
	host, _ = filepath.EvalSymlinks(host)
	for _, d := range []string{"etc", "root/media", "srv"} {
		if err := os.MkdirAll(filepath.Join(host, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.HostRoot = MountedHostRoot(host)
	if r.HostRoot == "" {
		t.Fatal("the fake host was not recognised")
	}
	return r, media, host
}

func TestHostFoldersAreGivenAsTheHostSeesThem(t *testing.T) {
	r, media, host := hostRoots(t)
	cases := []struct {
		in, want string
	}{
		{"movies", filepath.Join(media, "movies")},
		{filepath.Join(media, "shows"), filepath.Join(media, "shows")},
		{"/root/media/movies", filepath.Join(host, "root", "media", "movies")},
		{"/srv/tv", filepath.Join(host, "srv", "tv")},
		{filepath.Join(host, "srv", "tv"), filepath.Join(host, "srv", "tv")},
	}
	for _, c := range cases {
		got, err := r.Resolve(c.in, "Lib")
		if err != nil || got != c.want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if got := r.HostPath(filepath.Join(host, "root", "media", "movies")); got != "/root/media/movies" {
		t.Errorf("HostPath = %q", got)
	}
	if got := r.HostPath(filepath.Join(media, "movies")); got != "" {
		t.Errorf("a media folder reported a host path: %q", got)
	}
	created, err := r.EnsureDir(filepath.Join(host, "root", "media", "movies"))
	if err != nil || !created {
		t.Fatalf("EnsureDir on the host: %v %v", created, err)
	}
}

func TestHostSystemFoldersAreRefused(t *testing.T) {
	r, _, _ := hostRoots(t)
	for _, p := range []string{"/", "/etc", "/etc/media", "/usr/share/media", "/proc/1", "/var/lib/docker/volumes", "/boot"} {
		if _, err := r.Resolve(p, "Lib"); !errors.Is(err, ErrSystemFolder) {
			t.Errorf("Resolve(%q) = %v, want ErrSystemFolder", p, err)
		}
	}
	if _, err := r.Resolve("/var/media", "Lib"); err != nil {
		t.Errorf("/var/media should be allowed: %v", err)
	}
}

func TestWithoutTheHostMountOnlyTheMediaFolderIsAllowed(t *testing.T) {
	r, _ := testRoots(t)
	if _, err := r.Resolve("/root/media/movies", "Lib"); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("got %v, want ErrOutsideRoots", err)
	}
	if MountedHostRoot(t.TempDir()) != "" {
		t.Fatal("a folder without the host's /etc was taken for the host")
	}
}

func TestExplainTellsWhatToRunOnTheHost(t *testing.T) {
	r, _, host := hostRoots(t)
	err := r.Explain(filepath.Join(host, "root", "media", "movies"), ErrNotWritable)
	if err == nil || !errors.Is(err, ErrNotWritable) || !strings.Contains(err.Error(), "chown 4242:4343 /root/media/movies") || !strings.Contains(err.Error(), "chmod o+x /root") {
		t.Fatalf("explanation: %v", err)
	}
}
