package playback

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/hwaccel"
	"github.com/viewdock/viewdock/internal/library"
)

type denyContains struct{}

func (denyContains) LocateItem(context.Context, string, string) (*library.LocatedFile, error) {
	return nil, os.ErrNotExist
}
func (denyContains) LocateFile(context.Context, string) (*library.LocatedFile, error) {
	return nil, os.ErrNotExist
}
func (denyContains) Contains(string, string) error { return errors.New("blocked") }
func (denyContains) Open(context.Context, string) (*os.File, error) {
	return nil, os.ErrNotExist
}

func TestFallbackCPUOneShotSetsFields(t *testing.T) {
	media := t.TempDir() + "/v.mp4"
	if err := os.WriteFile(media, []byte("xx"), 0o644); err != nil {
		t.Fatal(err)
	}
	api := testAPI(t, &mockLocator{file: &library.LocatedFile{
		ID: "f1", LibraryID: "lib", AbsPath: media, ItemKind: "movie", ItemID: "m1",
	}}, nil)
	api.Locator = denyContains{}

	s := &Session{
		ID: "s1", AbsPath: media, LibraryID: "lib", Dir: t.TempDir(),
		Encoder: "h264_nvenc", EncoderType: "nvidia_nvenc",
		HW: hwaccel.Info{NVENC: true, H264NVENC: true, Available: true},
		Info: &ffmpeg.MediaInfo{
			Width: 1920, Height: 1080, VideoCodec: "hevc", AudioCodec: "aac",
		},
		Decision: decision.Result{Mode: decision.ModeTranscodeVid, NeedVideoXcode: true},
	}
	stderr := "Cannot load libnvidia-encode.so.1\nNo capable devices found"
	if !hwaccel.DeviceFailed(stderr) {
		t.Fatal("stderr should match DeviceFailed")
	}
	_ = api.fallbackCPU(s, stderr)
	if !s.cpuFallback || !s.Fallback {
		t.Fatalf("expected one-shot fallback flags: %+v", s)
	}
	if s.EncoderType != "cpu" || s.Encoder != "libx264" {
		t.Fatalf("encoder %s type %s", s.Encoder, s.EncoderType)
	}
	if s.HW.NVENC || s.HW.H264NVENC || s.HW.Available {
		t.Fatalf("HW after fallback: %+v", s.HW)
	}
	if !strings.Contains(s.FallbackReason, "libnvidia-encode") {
		t.Fatalf("FallbackReason %q", s.FallbackReason)
	}
	if !containsReason(s.Reasons, decision.HWFallbackCPU) {
		t.Fatalf("reasons %v", s.Reasons)
	}
	if api.fallbackCPU(s, stderr) {
		t.Fatal("one-shot: second fallback must not run")
	}
}

func TestSessionEncoderType(t *testing.T) {
	if sessionEncoderType("h264_nvenc", true) != "nvidia_nvenc" {
		t.Fatal("NVENC transcode")
	}
	if sessionEncoderType("h264_nvenc", false) != "cpu" {
		t.Fatal("copy/remux")
	}
	if sessionEncoderType("copy", false) != "cpu" {
		t.Fatal("copy")
	}
	if sessionEncoderType("libx264", true) != "cpu" {
		t.Fatal("CPU transcode")
	}
}

func TestFallbackReasonEmptyUsesDecision(t *testing.T) {
	if fallbackReason("") != decision.HWFallbackCPU {
		t.Fatal(fallbackReason(""))
	}
	if fallbackReason("  CreateBitstreamBuffer failed  ") != "CreateBitstreamBuffer failed" {
		t.Fatal(fallbackReason("  CreateBitstreamBuffer failed  "))
	}
}

func containsReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}
