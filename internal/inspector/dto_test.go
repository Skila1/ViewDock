package inspector

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/viewdock/viewdock/internal/capability"
)

func TestGPUNullable(t *testing.T) {
	d := Build(Input{ID: "s1", Reasons: []string{"DIRECT_PLAY"}, Client: capability.Profile{}})
	b, _ := json.Marshal(d)
	if !strings.Contains(string(b), `"gpu":null`) {
		t.Fatalf("gpu should be null: %s", b)
	}
	d = Build(Input{ID: "s1", GPUAvail: true, VAAPI: true, HWAccel: "vaapi", Reasons: []string{}})
	if d.GPU == nil || !d.GPU.VAAPI {
		t.Fatal("gpu present")
	}
}

func TestBuildStreamActions(t *testing.T) {
	d := Build(Input{
		ID: "s1", Playback: "Partial Transcode", Hardware: "Not required",
		Video:   StreamCol{Codec: "hevc_main10", Action: "COPY", Reason: "DIRECT_VIDEO_HEVC_MAIN10"},
		Audio:   StreamCol{Codec: "eac3", Action: "TRANSCODE", To: "aac", Reason: "TRANSCODE_AUDIO_EAC3"},
		Cont:    StreamCol{Codec: "mkv", Action: "REMUX", To: "hls", Reason: "REMUX_CONTAINER_MKV"},
		Reasons: []string{"DIRECT_VIDEO_HEVC_MAIN10", "TRANSCODE_AUDIO_EAC3"},
	})
	if d.Decision.Playback != "Partial Transcode" || d.Decision.Video.Action != "COPY" || d.Decision.Audio.To != "aac" {
		t.Fatalf("%+v", d.Decision)
	}
	if d.Decision.Hardware != "Not required" {
		t.Fatal(d.Decision.Hardware)
	}
}

func TestGPUUsedNVENC(t *testing.T) {
	d := Build(Input{
		ID: "s1", GPUAvail: true, NVENC: true, Encoder: "h264_nvenc",
		NeedVideoXcode: true, HWAccel: "nvenc", Reasons: []string{},
	})
	if d.GPU == nil {
		t.Fatal("gpu present when NVENC available")
	}
	if !d.GPU.GPUUsed || d.GPU.Vendor != "nvidia" || d.GPU.Encoder != "h264_nvenc" {
		t.Fatalf("gpu_used nvidia h264_nvenc: %+v", d.GPU)
	}
	if d.GPU.Fallback || !d.GPU.Available || !d.GPU.NVENC {
		t.Fatalf("%+v", d.GPU)
	}
	if d.Decision.EncoderType != "nvidia_nvenc" {
		t.Fatal(d.Decision.EncoderType)
	}
}

func TestGPUUsedFromHandlerFlag(t *testing.T) {
	d := Build(Input{
		ID: "s1", GPUAvail: true, NVENC: true, Encoder: "h264_nvenc",
		GPUUsed: true, Reasons: []string{},
	})
	if d.GPU == nil || !d.GPU.GPUUsed || d.GPU.Vendor != "nvidia" {
		t.Fatalf("%+v", d.GPU)
	}
}

func TestGPULibx264NotUsed(t *testing.T) {
	d := Build(Input{
		ID: "s1", GPUAvail: true, NVENC: true, Encoder: "libx264",
		Reasons: []string{"DIRECT_PLAY"},
	})
	if d.GPU == nil {
		t.Fatal("gpu present when NVENC available")
	}
	if d.GPU.GPUUsed || d.GPU.Vendor != "nvidia" || d.GPU.Encoder != "libx264" {
		t.Fatalf("available but not used, encoder libx264: %+v", d.GPU)
	}
	if d.Decision.EncoderType != "cpu" {
		t.Fatal(d.Decision.EncoderType)
	}
}

func TestGPUFallbackEmitsObject(t *testing.T) {
	d := Build(Input{
		ID: "s1", GPUAvail: false, Fallback: true, Encoder: "libx264",
		FallbackReason: "device creation failed", Reasons: []string{},
	})
	if d.GPU == nil {
		t.Fatal("gpu present on fallback even when !GPUAvail")
	}
	if d.GPU.GPUUsed || !d.GPU.Fallback || d.GPU.Encoder != "libx264" {
		t.Fatalf("%+v", d.GPU)
	}
	if d.GPU.Vendor != "" {
		t.Fatalf("vendor only when NVENC or gpu_used: %+v", d.GPU)
	}
	if d.GPU.FallbackReason != "device creation failed" {
		t.Fatal(d.GPU.FallbackReason)
	}
	if d.Decision.EncoderType != "cpu" {
		t.Fatal(d.Decision.EncoderType)
	}
}

func TestBuildGenerationTelemetry(t *testing.T) {
	d := Build(Input{
		ID: "s1", VODOnDemand: true, VODPlanKind: "keyframe",
		GenStartSeg: 12, GenerationID: 3, HLSAttach: "native", SeekableFrom: 0,
		Reasons: []string{},
	})
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		`"vod_ondemand":true`,
		`"vod_plan_kind":"keyframe"`,
		`"gen_start_seg":12`,
		`"generation_id":3`,
		`"hls_attach":"native"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, `"seekable_from_ms"`) {
		t.Fatal("origin 0 must omit seekable_from_ms")
	}
	if !strings.Contains(got, `"origin_ms":0`) {
		t.Fatalf("VOD origin must be 0: %s", got)
	}
}

func TestBuildVODOriginIgnoresWindowStart(t *testing.T) {
	d := Build(Input{
		ID: "s3", VODOnDemand: true, HLSAttach: "native", SeekableFrom: 55_000, Reasons: []string{},
	})
	if d.OriginMS != 0 {
		t.Fatalf("VOD origin must be 0, got %d", d.OriginMS)
	}
}

func TestBuildEVENTOriginIsWindowStart(t *testing.T) {
	d := Build(Input{
		ID: "s2", HLSAttach: "mse", SeekableFrom: 55_000, Reasons: []string{},
	})
	if d.VODOnDemand || d.OriginMS != 55_000 {
		t.Fatalf("EVENT origin: vod=%v origin=%d", d.VODOnDemand, d.OriginMS)
	}
}

func TestGPUUsedFalseWhenFallback(t *testing.T) {
	d := Build(Input{
		ID: "s1", GPUAvail: true, NVENC: true, Encoder: "h264_nvenc",
		NeedVideoXcode: true, GPUUsed: true, Fallback: true, Reasons: []string{},
	})
	if d.GPU == nil || d.GPU.GPUUsed || !d.GPU.Fallback {
		t.Fatalf("gpu_used false on fallback: %+v", d.GPU)
	}
	if d.GPU.Vendor != "nvidia" {
		t.Fatalf("vendor nvidia when NVENC available: %+v", d.GPU)
	}
}
