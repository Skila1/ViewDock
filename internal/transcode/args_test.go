package transcode

import (
	"strings"
	"testing"
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
