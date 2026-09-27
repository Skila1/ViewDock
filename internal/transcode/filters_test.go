package transcode

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFilterWhitelist(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub.ass")
	if err := ValidateChain("scale=-2:720,tonemap=hable", dir); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChain("subtitles="+sub, dir); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChain("movie=/etc/passwd", dir); err == nil {
		t.Fatal("movie filter must be rejected")
	}
	if err := ValidateChain("subtitles=/etc/passwd", dir); err == nil {
		t.Fatal("outside subtitles path must be rejected")
	}
}

func TestBuildVF(t *testing.T) {
	vf := BuildVF(720, "hdr10", true, "")
	if vf == "" || !strings.Contains(vf, "tonemap") || !strings.Contains(vf, "scale=-2:720") {
		t.Fatal(vf)
	}
}
