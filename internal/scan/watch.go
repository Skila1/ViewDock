package scan

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

var Debounce = 2 * time.Second

// Watch starts a recursive watcher on the library root. If fsnotify fails or
// the path looks like NFS/CIFS/UNC, it polls every PollInterval instead.
func (s *Scanner) Watch(ctx context.Context, libraryID string) error {
	root, err := s.rootOf(ctx, libraryID)
	if err != nil {
		return err
	}
	if looksRemote(root) {
		return s.poll(ctx, libraryID)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return s.poll(ctx, libraryID)
	}
	defer w.Close()
	if err := addRecursive(w, root); err != nil {
		return s.poll(ctx, libraryID)
	}
	pending := map[string]time.Time{}
	tick := time.NewTicker(Debounce / 2)
	if Debounce <= 0 {
		tick.Reset(50 * time.Millisecond)
	}
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-w.Events:
			if !ok {
				return s.poll(ctx, libraryID)
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
				continue
			}
			if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
				_ = addRecursive(w, ev.Name)
				continue
			}
			pending[ev.Name] = time.Now()
		case _, ok := <-w.Errors:
			if !ok {
				return s.poll(ctx, libraryID)
			}
		case <-tick.C:
			now := time.Now()
			for p, t0 := range pending {
				if now.Sub(t0) >= Debounce {
					delete(pending, p)
					_ = s.IngestFile(ctx, libraryID, p)
				}
			}
		}
	}
}

func (s *Scanner) WatchAll(ctx context.Context) error {
	if s.Libs == nil {
		return nil
	}
	libs, err := s.Libs.List(ctx)
	if err != nil {
		return err
	}
	for _, lib := range libs {
		lib := lib
		go func() { _ = s.Watch(ctx, lib.ID) }()
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *Scanner) poll(ctx context.Context, libraryID string) error {
	interval := PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	_, _, _ = s.scanLibrary(ctx, libraryID)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			_, _, _ = s.scanLibrary(ctx, libraryID)
		}
	}
}

func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if ShouldSkip(filepath.ToSlash(path)) && path != root {
				return filepath.SkipDir
			}
			return w.Add(path)
		}
		return nil
	})
}

func looksRemote(path string) bool {
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
		return true
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		if fstype := mountFS(path); fstype != "" {
			switch strings.ToLower(fstype) {
			case "nfs", "nfs4", "cifs", "smb", "smb3", "fuse.sshfs", "fuse.rclone":
				return true
			}
		}
	}
	return false
}

func mountFS(path string) string {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return ""
	}
	defer f.Close()
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	best, bestLen := "", 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		mp, fstype := fields[1], fields[2]
		if strings.HasPrefix(abs, mp) && len(mp) >= bestLen {
			best, bestLen = fstype, len(mp)
		}
	}
	return best
}
