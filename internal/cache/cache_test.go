package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHLSEvictPinsActive(t *testing.T) {
	root := t.TempDir()
	h := NewHLS(root, 10)
	old := mustDir(t, h, "old")
	_ = os.WriteFile(filepath.Join(old, "x"), []byte("1234567890"), 0o644)
	_ = os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	_ = os.Chtimes(filepath.Join(old, "x"), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	_ = os.WriteFile(filepath.Join(mustDir(t, h, "live"), "x"), []byte("1234567890"), 0o644)
	h.Evict([]string{"live"})
	if _, err := os.Stat(h.Dir("live")); err != nil {
		t.Fatal("pinned session evicted")
	}
	if _, err := os.Stat(h.Dir("old")); err == nil {
		t.Fatal("inactive dir should evict under cap")
	}
}

func mustDir(t *testing.T, h *HLS, id string) string {
	t.Helper()
	d, err := h.Ensure(id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestArtworkLRU(t *testing.T) {
	a := NewArtwork(t.TempDir(), 5)
	p1 := a.Path("a")
	_ = os.WriteFile(p1, []byte("12345"), 0o644)
	a.Touch("a", 5)
	p2 := a.Path("b")
	_ = os.WriteFile(p2, []byte("x"), 0o644)
	a.Touch("b", 1)
	if _, err := os.Stat(p1); err == nil {
		t.Fatal("expected LRU evict of a")
	}
}
