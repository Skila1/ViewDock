//go:build unix

package library_test

import (
	"os"
	"syscall"
	"testing"
)

func fileOwner(t *testing.T, path string) (int, int) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sys := st.Sys().(*syscall.Stat_t)
	return int(sys.Uid), int(sys.Gid)
}
