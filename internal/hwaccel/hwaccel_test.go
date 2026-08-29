package hwaccel

import (
	"testing"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

func TestStartsWithoutNVENC(t *testing.T) {
	info := FromDetect(ffmpeg.DetectResult{Encoders: []string{"libx264", "aac"}, HWAccel: []string{}})
	if info.NVENC || info.Available {
		t.Fatalf("%+v", info)
	}
	enc, hw := VideoEncoder(info)
	if enc != "libx264" || hw != "" {
		t.Fatalf("%s %s", enc, hw)
	}
}

func TestRefuse4KHDRWithoutZScale(t *testing.T) {
	ok, reason := CanTranscodeHDR4K(Info{ZScale: false}, 3840, 2160, "hdr10")
	if ok || reason != "REFUSE_4K_HDR_NO_ZSCALE" {
		t.Fatalf("ok=%v reason=%s", ok, reason)
	}
	ok, _ = CanTranscodeHDR4K(Info{ZScale: true}, 3840, 2160, "hdr10")
	if !ok {
		t.Fatal("zscale should allow")
	}
	ok, _ = CanTranscodeHDR4K(Info{}, 1920, 1080, "hdr10")
	if !ok {
		t.Fatal("1080p HDR ok without zscale")
	}
}

func TestDetectVAAPIRequiresDevice(t *testing.T) {
	compiled := ffmpeg.DetectResult{Encoders: []string{"h264_vaapi"}, HWAccel: []string{"vaapi"}}
	none := fromDetect(compiled, func() bool { return false }, func() bool { return false })
	if none.VAAPI || none.Available {
		t.Fatalf("compiled-in vaapi without a render node must not be used: %+v", none)
	}
	ok := fromDetect(compiled, func() bool { return true }, func() bool { return false })
	if !ok.VAAPI || !ok.Available {
		t.Fatalf("%+v", ok)
	}
}

func TestDeviceFailed(t *testing.T) {
	if !DeviceFailed("Device creation failed: -542398533.\nNo device available for decoder") {
		t.Fatal("expected vaapi device failure")
	}
	if DeviceFailed("frame=  12 fps=0.0") {
		t.Fatal("normal output is not a device failure")
	}
}
