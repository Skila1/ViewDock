package cache

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const DefaultHLSCap = 20 << 30 // 20 GiB

type HLS struct {
	Root string
	Cap  int64

	mu sync.Mutex
}

func NewHLS(root string, cap int64) *HLS {
	if cap <= 0 {
		cap = DefaultHLSCap
	}
	return &HLS{Root: root, Cap: cap}
}

func (h *HLS) Dir(sessionID string) string {
	return filepath.Join(h.Root, sessionID)
}

func (h *HLS) Ensure(sessionID string) (string, error) {
	dir := h.Dir(sessionID)
	return dir, os.MkdirAll(dir, 0o755)
}

func (h *HLS) Remove(sessionID string) {
	_ = os.RemoveAll(h.Dir(sessionID))
}

// Evict deletes oldest inactive session dirs until under Cap. Pin active IDs.
func (h *HLS) Evict(active []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Root == "" {
		return
	}
	pin := map[string]bool{}
	for _, id := range active {
		pin[id] = true
	}
	ents, err := os.ReadDir(h.Root)
	if err != nil {
		return
	}
	type dir struct {
		name string
		mod  time.Time
		size int64
	}
	var list []dir
	var total int64
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(h.Root, e.Name())
		sz, mod := dirSize(p)
		total += sz
		list = append(list, dir{name: e.Name(), mod: mod, size: sz})
	}
	if total <= h.Cap {
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod.Before(list[j].mod) })
	for _, d := range list {
		if total <= h.Cap {
			break
		}
		if pin[d.name] {
			continue
		}
		_ = os.RemoveAll(filepath.Join(h.Root, d.name))
		total -= d.size
	}
}

func dirSize(root string) (int64, time.Time) {
	var n int64
	var latest time.Time
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		if !d.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n, latest
}
