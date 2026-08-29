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
		Video: StreamCol{Codec: "hevc_main10", Action: "COPY", Reason: "DIRECT_VIDEO_HEVC_MAIN10"},
		Audio: StreamCol{Codec: "eac3", Action: "TRANSCODE", To: "aac", Reason: "TRANSCODE_AUDIO_EAC3"},
		Cont:  StreamCol{Codec: "mkv", Action: "REMUX", To: "hls", Reason: "REMUX_CONTAINER_MKV"},
		Reasons: []string{"DIRECT_VIDEO_HEVC_MAIN10", "TRANSCODE_AUDIO_EAC3"},
	})
	if d.Decision.Playback != "Partial Transcode" || d.Decision.Video.Action != "COPY" || d.Decision.Audio.To != "aac" {
		t.Fatalf("%+v", d.Decision)
	}
	if d.Decision.Hardware != "Not required" {
		t.Fatal(d.Decision.Hardware)
	}
}
