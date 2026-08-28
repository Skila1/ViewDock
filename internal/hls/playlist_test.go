package hls

import (
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

func TestSafeFile(t *testing.T) {
	if SafeFile("../etc/passwd") || SafeFile("seg0.exe") {
		t.Fatal("unsafe")
	}
	if !SafeFile("index.m3u8") || !SafeFile("seg12.m4s") || !SafeFile("init.mp4") {
		t.Fatal("safe")
	}
}
