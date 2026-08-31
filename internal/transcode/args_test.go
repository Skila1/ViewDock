package transcode

import (
	"strings"
	"testing"

	"github.com/viewdock/viewdock/internal/hwaccel"
)

func hasSeq(args []string, want ...string) bool {
	if len(want) == 0 {
		return true
	}
	n := 0
	for _, a := range args {
		if a == want[n] {
			n++
			if n == len(want) {
				return true
			}
		}
	}
	return false
}

func TestBuildArgs_HEVCCopyEAC3Transcode(t *testing.T) {
	args, err := BuildArgs(Opts{
		AbsPath: "/media/scarface.mkv", SessionDir: "/cache/s1",
		CopyVideo: true, CopyAudio: false, HEVC: true, SrcHeight: 1080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeq(args, "-c:v", "copy") || !hasSeq(args, "-tag:v", "hvc1") {
		t.Fatalf("want video copy+hvc1: %v", args)
	}
	if !hasSeq(args, "-c:a", "aac") {
		t.Fatalf("want audio aac: %v", args)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "libx264") {
		t.Fatal("must not encode video")
	}
}

func TestBuildArgs_HEVCTranscodeAACCopy(t *testing.T) {
	args, err := BuildArgs(Opts{
		AbsPath: "/media/x.mkv", SessionDir: "/cache/s1",
		CopyVideo: false, CopyAudio: true, SrcHeight: 1080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeq(args, "-c:v", "libx264") || !hasSeq(args, "-c:a", "copy") {
		t.Fatalf("want h264 + audio copy: %v", args)
	}
}

func TestBuildArgs_BothCopy(t *testing.T) {
	args, err := BuildArgs(Opts{
		AbsPath: "/media/x.mkv", SessionDir: "/cache/s1",
		CopyVideo: true, CopyAudio: true, SrcHeight: 1080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeq(args, "-c:v", "copy") || !hasSeq(args, "-c:a", "copy") {
		t.Fatalf("want both copy: %v", args)
	}
}

func TestBuildArgs_BothTranscode(t *testing.T) {
	args, err := BuildArgs(Opts{
		AbsPath: "/media/scarface.mkv", SessionDir: "/cache/s1",
		CopyVideo: false, CopyAudio: false, SrcHeight: 1080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeq(args, "-c:v", "libx264") || !hasSeq(args, "-c:a", "aac") {
		t.Fatalf("want full transcode: %v", args)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "h264_nvenc") {
		t.Fatal("CPU transcode must not use NVENC")
	}
}

func TestBuildArgs_NVENCTranscode(t *testing.T) {
	args, err := BuildArgs(Opts{
		AbsPath: "/media/x.mkv", SessionDir: "/cache/s1",
		CopyVideo: false, CopyAudio: false, SrcHeight: 1080,
		HW: hwaccel.Info{NVENC: true, H264NVENC: true, Available: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeq(args, "-c:v", "h264_nvenc",
		"-preset", "p1", "-tune", "ll", "-rc", "constqp", "-qp", "23",
		"-pix_fmt", "yuv420p", "-profile:v", "main", "-level", "4.0", "-g", "48") {
		t.Fatalf("want NVENC flag sequence: %v", args)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "libx264") {
		t.Fatal("NVENC transcode must not use libx264")
	}
	if hasSeq(args, "-hwaccel", "cuda") {
		t.Fatal("must not add -hwaccel cuda")
	}
	if !hasSeq(args, "-hls_playlist_type", "event") || !hasSeq(args, "-hls_segment_type", "fmp4") {
		t.Fatalf("EVENT fMP4 required: %v", args)
	}
}

func TestBuildArgs_NVENCCopySkipsEncoder(t *testing.T) {
	args, err := BuildArgs(Opts{
		AbsPath: "/media/x.mkv", SessionDir: "/cache/s1",
		CopyVideo: true, CopyAudio: true, SrcHeight: 1080,
		HW: hwaccel.Info{NVENC: true, H264NVENC: true, Available: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeq(args, "-c:v", "copy") {
		t.Fatalf("want video copy: %v", args)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "h264_nvenc") {
		t.Fatal("copy path must not use h264_nvenc")
	}
	if !hasSeq(args, "-hls_playlist_type", "event") || !hasSeq(args, "-hls_segment_type", "fmp4") {
		t.Fatalf("EVENT fMP4 required: %v", args)
	}
}

func TestBuildArgs_PartialTranscodeUsesFMP4(t *testing.T) {
	args, err := BuildArgs(Opts{
		AbsPath: "/media/x.mkv", SessionDir: "/cache/s1",
		CopyVideo: false, CopyAudio: true, SrcHeight: 1080,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !hasSeq(args, "-hls_segment_type", "fmp4") || !strings.Contains(joined, "seg%d.m4s") {
		t.Fatalf("hls.js cannot parse EC-3 in MPEG-TS: %v", args)
	}
	if strings.Contains(joined, "mpegts") || strings.Contains(joined, ".ts") {
		t.Fatal("partial transcode must not write MPEG-TS")
	}
}

func TestBuildArgs_HLSIsGrowingEvent(t *testing.T) {
	args, err := BuildArgs(Opts{
		AbsPath: "/media/x.mkv", SessionDir: "/cache/s1",
		CopyVideo: false, CopyAudio: false, SrcHeight: 1080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeq(args, "-hls_playlist_type", "event") {
		t.Fatalf("in-progress movies must be EVENT, got %v", args)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "vod") {
		t.Fatal("do not advertise VOD while FFmpeg is still appending")
	}
}
