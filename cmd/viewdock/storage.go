package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/mediafs"
)

// prepareStorage is run by the container entrypoint as root, before it
// drops to the ViewDock account. It gives that account the media storage
// roots and every folder under them that root created (for example with
// mkdir over SSH), so libraries, uploads and moves never fail on ownership.
// Files are never touched and folders owned by other accounts are left
// alone. It does not open the database.
func prepareStorage(cfg config.Config, out io.Writer) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(out, "prepare-storage: not running as root; nothing to do")
		return 0
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("VD_FIX_PERMISSIONS"))); v == "0" || v == "false" || v == "off" {
		fmt.Fprintln(out, "prepare-storage: VD_FIX_PERMISSIONS is off; skipping")
		return 0
	}
	depth := config.GetenvInt("VD_FIX_PERMISSIONS_DEPTH", mediafs.DefaultRepairDepth)
	for _, root := range cfg.LibraryRoots {
		if err := os.MkdirAll(root, mediafs.DirMode); err != nil {
			fmt.Fprintf(out, "prepare-storage: cannot create %s: %v\n", root, err)
		}
	}
	rep := mediafs.Repair(cfg.LibraryRoots, cfg.PUID, cfg.PGID, depth)
	fmt.Fprintf(out, "prepare-storage: media folders checked=%d repaired=%d owner=%d:%d\n", rep.Checked, rep.Fixed, cfg.PUID, cfg.PGID)
	for _, p := range rep.Failed {
		fmt.Fprintf(out, "prepare-storage: could not repair %s (read-only or network mount?)\n", p)
	}
	return 0
}

// warnUnwritable logs, at startup, each storage root the running account
// cannot write, with the likely fix, instead of failing later on an upload.
func warnUnwritable(cfg config.Config, logger *slog.Logger) {
	if cfg.Role == config.RoleFrontend || cfg.Role == config.RoleCoordinator {
		return
	}
	for _, root := range cfg.LibraryRoots {
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			continue
		}
		if err := mediafs.CheckWritable(root); err != nil {
			logger.Warn("media folder is not writable; library folders, uploads and moves will fail",
				"category", "storage", "dir", root, "uid", os.Geteuid(), "err", err,
				"hint", "start the container as root (the default) so the entrypoint can repair ownership, or set PUID/PGID to the folder owner")
		}
	}
}
