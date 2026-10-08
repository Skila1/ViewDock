//go:build unix

package mediafs

import (
	"os"
	"strconv"
	"syscall"
)

// ownerOf returns "uid:gid" of path, or "" when it cannot be read.
func ownerOf(path string) string {
	uid, gid, ok := owner(path)
	if !ok {
		return ""
	}
	return strconv.Itoa(uid) + ":" + strconv.Itoa(gid)
}

func owner(path string) (uid, gid int, ok bool) {
	st, err := os.Lstat(path)
	if err != nil {
		return 0, 0, false
	}
	return ownerInfo(st)
}

func ownerInfo(st os.FileInfo) (uid, gid int, ok bool) {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(sys.Uid), int(sys.Gid), true
}
