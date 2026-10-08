package config

import (
	"reflect"
	"testing"
)

func TestLibraryRoots(t *testing.T) {
	t.Setenv("VD_MEDIA_DIR", "/media")
	t.Setenv("VD_LIBRARY_ROOTS", "/mnt/nas:/media, /mnt/usb,,/mnt/nas")
	t.Setenv("PUID", "1001")
	cfg := Load()
	want := []string{"/media", "/mnt/nas", "/mnt/usb"}
	if !reflect.DeepEqual(cfg.LibraryRoots, want) {
		t.Fatalf("roots %v, want %v", cfg.LibraryRoots, want)
	}
	if cfg.PUID != 1001 || cfg.PGID != 1000 {
		t.Fatalf("owner %d:%d", cfg.PUID, cfg.PGID)
	}
}
