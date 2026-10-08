package hwaccel

import (
	"testing"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

func TestStartsWithoutNVENC(t *testing.T) {
	info := FromDetect(ffmpeg.DetectResult{Encoders: []string{"libx264", "aac"}, HWAccel: []string{}})
	if info.NVENC || info.H264NVENC || info.Available {
		t.Fatalf("%+v", info)
	}
	enc, hw := VideoEncoder(info)
	if enc != "libx264" || hw != "" {
		t.Fatalf("%s %s", enc, hw)
	}
	if StartupMessage(info) != "Hardware acceleration: unavailable, using CPU transcoding" {
		t.Fatalf("startup %q", StartupMessage(info))
	}
}

func TestEmptyDetectUsesLibx264(t *testing.T) {
	t.Setenv("VD_HWACCEL", "")
	t.Setenv("VD_HW_ACCEL", "")
	info := FromDetect(ffmpeg.DetectResult{})
	enc, hw := VideoEncoder(info)
	if enc != "libx264" || hw != "" || info.NVENC || info.H264NVENC || info.Available {
		t.Fatalf("%+v enc=%s hw=%s", info, enc, hw)
	}
	applied := Apply(info, "")
	enc, hw = VideoEncoder(applied)
	if enc != "libx264" || hw != "" || applied.NVENC || applied.H264NVENC {
		t.Fatalf("empty bin apply: %+v", applied)
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

func TestListedNVENCNotUsableUntilProbe(t *testing.T) {
	t.Setenv("VD_HWACCEL", "")
	t.Setenv("VD_HW_ACCEL", "")
	compiled := ffmpeg.DetectResult{
		Encoders: []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc", "libx264"},
		HWAccel:  []string{"cuda", "nvenc", "nvdec"},
	}
	info := fromDetect(compiled, func() bool { return false }, func() bool { return true })
	if info.NVENC || info.H264NVENC || info.Available {
		t.Fatalf("listed NVENC is not usable before Apply probe: %+v", info)
	}
	if !info.HEVCNVENC || !info.AV1NVENC || !info.NVDEC {
		t.Fatalf("should record compiled NVIDIA encoders/decoders: %+v", info)
	}
	if info.DetectionReason != "nvenc_listed" {
		t.Fatalf("reason %s", info.DetectionReason)
	}
	enc, hw := VideoEncoder(info)
	if enc != "libx264" || hw != "" {
		t.Fatalf("%s %s", enc, hw)
	}
	applied := Apply(info, "")
	if applied.NVENC || applied.H264NVENC || applied.Available {
		t.Fatalf("Apply without probe must leave CPU: %+v", applied)
	}
	enc, hw = VideoEncoder(applied)
	if enc != "libx264" || hw != "" {
		t.Fatalf("%s %s", enc, hw)
	}
}

func TestApplyOffDisablesHW(t *testing.T) {
	t.Setenv("VD_HWACCEL", "off")
	info := Apply(Info{VAAPI: true, NVENC: true, H264NVENC: true, Available: true}, "")
	if info.VAAPI || info.NVENC || info.H264NVENC || info.Available {
		t.Fatalf("%+v", info)
	}
	if info.DetectionReason != "forced_cpu" {
		t.Fatalf("reason %s", info.DetectionReason)
	}
}

func TestApplyVDHWAccelOffForcesCPU(t *testing.T) {
	t.Setenv("VD_HWACCEL", "")
	t.Setenv("VD_HW_ACCEL", "off")
	info := Apply(Info{VAAPI: true, NVENC: true, H264NVENC: true, Available: true, Encoders: []string{"h264_nvenc"}}, "ffmpeg")
	if info.VAAPI || info.NVENC || info.H264NVENC || info.Available {
		t.Fatalf("%+v", info)
	}
	if info.DetectionReason != "forced_cpu" {
		t.Fatalf("reason %s", info.DetectionReason)
	}
	enc, hw := VideoEncoder(info)
	if enc != "libx264" || hw != "" {
		t.Fatalf("%s %s", enc, hw)
	}
}

func TestApplyMissingBinLeavesCPU(t *testing.T) {
	t.Setenv("VD_HWACCEL", "")
	t.Setenv("VD_HW_ACCEL", "")
	info := Apply(Info{Encoders: []string{"h264_nvenc"}, NVENC: true, H264NVENC: true, Available: true}, "")
	if info.NVENC || info.H264NVENC || info.Available {
		t.Fatalf("%+v", info)
	}
	enc, hw := VideoEncoder(info)
	if enc != "libx264" || hw != "" {
		t.Fatalf("%s %s", enc, hw)
	}
}

func TestApplyMissingFFmpegProbeFails(t *testing.T) {
	t.Setenv("VD_HWACCEL", "")
	t.Setenv("VD_HW_ACCEL", "")
	info := Apply(Info{Encoders: []string{"h264_nvenc"}}, "/no/such/ffmpeg-viewdock-nvenc-probe")
	if info.NVENC || info.H264NVENC || info.Available {
		t.Fatalf("%+v", info)
	}
	if info.DetectionReason != "nvenc_probe_failed" {
		t.Fatalf("reason %s", info.DetectionReason)
	}
	enc, hw := VideoEncoder(info)
	if enc != "libx264" || hw != "" {
		t.Fatalf("%s %s", enc, hw)
	}
}

func TestDeviceFailed(t *testing.T) {
	if !DeviceFailed("Device creation failed: -542398533.\nNo device available for decoder") {
		t.Fatal("expected vaapi device failure")
	}
	if DeviceFailed("frame=  12 fps=0.0") {
		t.Fatal("normal output is not a device failure")
	}
	for _, s := range []string{
		"Cannot load libcuda.so.1",
		"No NVENC capable devices found",
		"Cannot load libnvidia-encode.so.1",
		"No capable devices found",
		"CreateBitstreamBuffer failed: 0x1",
	} {
		if !DeviceFailed(s) {
			t.Fatalf("expected NVENC device failure: %s", s)
		}
	}
}

func TestStartupMessage(t *testing.T) {
	if got := StartupMessage(Info{NVENC: true, H264NVENC: true, Available: true}); got != "Hardware acceleration: NVIDIA NVENC available" {
		t.Fatalf("%q", got)
	}
	if got := StartupMessage(Info{VAAPI: true, Available: true}); got != "Hardware acceleration: VAAPI available" {
		t.Fatalf("%q", got)
	}
	if got := StartupMessage(Info{}); got != "Hardware acceleration: unavailable, using CPU transcoding" {
		t.Fatalf("%q", got)
	}
}
