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

func TestDetectVAAPI(t *testing.T) {
	info := FromDetect(ffmpeg.DetectResult{Encoders: []string{"h264_vaapi"}, HWAccel: []string{"vaapi"}})
	if !info.VAAPI || !info.Available {
		t.Fatalf("%+v", info)
	}
}
