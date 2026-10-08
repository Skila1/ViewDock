package hls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteStoken(t *testing.T) {
	in := []byte("#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4,\nseg0.m4s\n")
	out := string(RewritePlaylist(in, "abc"))
	if !strings.Contains(out, "init.mp4?stoken=abc") {
		t.Fatalf("map: %s", out)
	}
	if !strings.Contains(out, "seg0.m4s?stoken=abc") {
		t.Fatalf("seg: %s", out)
	}
	out2 := string(RewritePlaylist([]byte(out), "xyz"))
	if strings.Contains(out2, "stoken=abc") || !strings.Contains(out2, "stoken=xyz") {
		t.Fatalf("rewrite: %s", out2)
	}
}

func TestWithStartAtZero(t *testing.T) {
	in := []byte("#EXTM3U\n#EXTINF:2.0,\nseg0.ts\n")
	out := string(WithStartAtZero(in))
	if !strings.Contains(out, "#EXT-X-START:TIME-OFFSET=0,PRECISE=YES") {
		t.Fatalf("missing start: %s", out)
	}
	if strings.Count(out, "#EXT-X-START:") != 1 {
		t.Fatalf("duplicate start: %s", out)
	}
	again := string(WithStartAtZero([]byte(out)))
	if strings.Count(again, "#EXT-X-START:") != 1 {
		t.Fatalf("idempotent: %s", again)
	}
}

func TestHasMedia(t *testing.T) {
	if HasMedia([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n")) {
		t.Fatal("header-only playlist is not playable")
	}
	if !HasMedia([]byte("#EXTM3U\n#EXTINF:2.0,\nseg0.m4s\n")) {
		t.Fatal("segment playlist should be playable")
	}
}

func TestMediaReady(t *testing.T) {
	dir := t.TempDir()
	body := []byte("#EXTM3U\n#EXTINF:2.0,\nseg0.ts\n")
	if MediaReady(dir, body) {
		t.Fatal("missing segment must not be ready")
	}
	if err := os.WriteFile(filepath.Join(dir, "seg0.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !MediaReady(dir, body) {
		t.Fatal("segment on disk should be ready")
	}
}

func TestSafeFile(t *testing.T) {
	if SafeFile("../etc/passwd") || SafeFile("seg0.exe") {
		t.Fatal("unsafe")
	}
	if !SafeFile("index.m3u8") || !SafeFile("seg12.m4s") || !SafeFile("init.mp4") {
		t.Fatal("safe")
	}
}
