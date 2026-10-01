package mediafs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DefaultRepairDepth reaches /media/<library>/<show>/<season>, the deepest
// folder a move writes into or removes from.
const DefaultRepairDepth = 4

// RepairReport summarises one ownership repair pass.
type RepairReport struct {
	Checked int      `json:"checked"`
	Fixed   int      `json:"fixed"`
	Failed  []string `json:"failed,omitempty"`
}

// Repair gives the ViewDock account (uid:gid) the folders under each storage
// root that were created by root outside ViewDock, for example with mkdir
// over SSH. It runs from the container entrypoint before privileges are
// dropped, so the unprivileged application can write every library folder.
//
// It only touches folders: files are never changed. A folder is changed only
// when it is the storage root itself or is owned by root (uid 0); folders
// owned by any other account belong to someone else and are left alone.
// Links are never followed. Folders more than maxDepth levels below a root
// are not visited.
func Repair(roots []string, uid, gid, maxDepth int) RepairReport {
	var rep RepairReport
	if uid <= 0 || os.Geteuid() != 0 {
		return rep
	}
	if maxDepth <= 0 {
		maxDepth = DefaultRepairDepth
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		st, err := os.Lstat(abs)
		if err != nil || !st.IsDir() {
			continue
		}
		_ = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			depth := 0
			if rel, err := filepath.Rel(abs, path); err == nil && rel != "." {
				depth = strings.Count(rel, string(os.PathSeparator)) + 1
			}
			if depth > maxDepth {
				return fs.SkipDir
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rep.Checked++
			if fixFolder(path, info, uid, gid, depth == 0) {
				rep.Fixed++
			} else if needsFix(info, uid, depth == 0) {
				rep.Failed = append(rep.Failed, path)
			}
			return nil
		})
	}
	return rep
}

func needsFix(info os.FileInfo, uid int, isRoot bool) bool {
	owner, _, ok := ownerInfo(info)
	if !ok {
		return false
	}
	if owner == uid {
		return info.Mode().Perm()&0o700 != 0o700
	}
	return isRoot || owner == 0
}

// fixFolder changes one folder and reports whether it did.
func fixFolder(path string, info os.FileInfo, uid, gid int, isRoot bool) bool {
	if !needsFix(info, uid, isRoot) {
		return false
	}
	owner, _, _ := ownerInfo(info)
	if owner != uid {
		if err := os.Lchown(path, uid, gid); err != nil {
			return false
		}
	}
	if perm := info.Mode().Perm(); perm&0o700 != 0o700 {
		if err := os.Chmod(path, perm|0o700); err != nil {
			return false
		}
	}
	return true
}
