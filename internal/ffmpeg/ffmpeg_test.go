package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func requireFF(t *testing.T) *Tool {
	t.Helper()
	if !Available() {
		t.Skip("ffmpeg/ffprobe not in PATH")
	}
	return New()
}

func makeH264AAC(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "tiny.mp4")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", "-y", dst,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lavfi mp4: %v (%s)", err, out)
	}
	return dst
}

func TestProbeH264AAC(t *testing.T) {
	tool := requireFF(t)
	src := makeH264AAC(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := tool.ProbeFile(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if info.VideoCodec != "h264" {
		t.Fatalf("video %q", info.VideoCodec)
	}
	if info.AudioCodec != "aac" {
		t.Fatalf("audio %q", info.AudioCodec)
	}
	if info.Container != "mp4" {
		t.Fatalf("container %q", info.Container)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Fatalf("size %dx%d", info.Width, info.Height)
	}
	if info.DurationMS < 500 {
		t.Fatalf("duration %d", info.DurationMS)
	}
}

func TestThumb(t *testing.T) {
	tool := requireFF(t)
	src := makeH264AAC(t)
	dest := filepath.Join(t.TempDir(), "t.jpg")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := tool.Thumb(ctx, src, dest, 200); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dest)
	if err != nil || st.Size() < 100 {
		t.Fatalf("thumb: %v size=%d", err, st.Size())
	}
}

func TestDetect(t *testing.T) {
	tool := requireFF(t)
	d := tool.Detect()
	if d.Version == "" {
		t.Fatal("missing version")
	}
	if !HasEncoder(d, "libx264") && !HasEncoder(d, "h264") {
		t.Fatalf("encoders %v", d.Encoders)
	}
}

func TestParseProbeJSON(t *testing.T) {
	raw := []byte(`{
		"streams":[{"index":0,"codec_name":"h264","codec_type":"video","width":1920,"height":1080,"bits_per_raw_sample":"8","color_transfer":"smpte2084","disposition":{"default":1}}],
		"format":{"duration":"12.5","size":"1000","format_name":"matroska,webm"}
	}`)
	info, err := ParseProbeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if info.Container != "mkv" || info.HDR != "hdr10" || info.DurationMS != 12500 {
		t.Fatalf("%+v", info)
	}
}

func TestContentType(t *testing.T) {
	if ContentType("mp4") != "video/mp4" {
		t.Fatal(ContentType("mp4"))
	}
}
