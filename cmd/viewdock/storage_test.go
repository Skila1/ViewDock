//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/viewdock/viewdock/internal/config"
)

func ownerOf(t *testing.T, p string) uint32 {
	t.Helper()
	st, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Sys().(*syscall.Stat_t).Uid
}

// The entrypoint runs prepare-storage as root on every container start, so a
// library folder created by root over SSH becomes writable for ViewDock
// without anyone running chown.
func TestPrepareStorageRepairsRootCreatedLibraryFolders(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root, like the container entrypoint")
	}
	media := t.TempDir()
	extra := filepath.Join(t.TempDir(), "nas")
	movies := filepath.Join(media, "movies")
	shows := filepath.Join(media, "shows", "Show", "Season 01")
	for _, d := range []string{movies, shows} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	film := filepath.Join(movies, "Heat (1995).mkv")
	if err := os.WriteFile(film, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{LibraryRoots: []string{media, extra}, PUID: 1000, PGID: 1000}
	var out bytes.Buffer
	if code := prepareStorage(cfg, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	for _, d := range []string{media, movies, filepath.Join(media, "shows"), shows, extra} {
		if got := ownerOf(t, d); got != 1000 {
			t.Fatalf("%s owned by %d\n%s", d, got, out.String())
		}
	}
	if got := ownerOf(t, film); got != 0 {
		t.Fatalf("media files must not be touched, owner %d", got)
	}
	if !strings.Contains(out.String(), "repaired=") {
		t.Fatalf("output: %s", out.String())
	}

	// Can be turned off.
	off := filepath.Join(media, "later")
	if err := os.Mkdir(off, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VD_FIX_PERMISSIONS", "false")
	out.Reset()
	prepareStorage(cfg, &out)
	if got := ownerOf(t, off); got != 0 {
		t.Fatalf("VD_FIX_PERMISSIONS=false still changed ownership")
	}
}
