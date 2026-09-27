package labs

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode, cfg.Device = ModeV4L2, "/dev/video10"
	src := SourceState{Path: "/media/m.mkv", PositionMS: 90_500, Playing: true}
	v4l2 := strings.Join(BuildArgs(cfg, src, "", "/work"), " ")
	for _, want := range []string{"-progress pipe:1", "-re -ss 90.500 -i /media/m.mkv", "-map [out] -an -f v4l2 -pix_fmt yuv420p /dev/video10", "scale=1280:720", "fps=30"} {
		if !strings.Contains(v4l2, want) {
			t.Errorf("v4l2 args missing %q:\n%s", want, v4l2)
		}
	}
	if strings.Contains(v4l2, "libx264") || strings.Contains(v4l2, "-nostdin") {
		t.Errorf("v4l2 args encode or close stdin:\n%s", v4l2)
	}
	for _, p := range []string{PreviewOutgoing, PreviewRaw} {
		if !strings.Contains(v4l2, filepath.Join("/work", p)) {
			t.Errorf("preview %s missing", p)
		}
	}

	cfg.Mode, cfg.Device = ModeRTMP, ""
	rtmp := BuildArgs(cfg, src, "rtmp://live.example.com/app/key", "/work")
	joined := strings.Join(rtmp, " ")
	for _, want := range []string{"-map 0:a:0?", "-c:v libx264", "-b:v 3500k", "-g 60", "-f flv rtmp://live.example.com/app/key", "astats=metadata=1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rtmp args missing %q:\n%s", want, joined)
		}
	}
	// The output URL is passed as one argument, never through a shell.
	found := false
	for _, a := range rtmp {
		if a == "rtmp://live.example.com/app/key" {
			found = true
		}
	}
	if !found {
		t.Error("output URL not a discrete argument")
	}

	cfg.Mode = ModeSRT
	paused := strings.Join(BuildArgs(cfg, SourceState{Path: "/m.mkv", PositionMS: 1000}, "srt://obs.lan:9000", "/w"), " ")
	for _, want := range []string{"-ss 1.000 -i /m.mkv", "anullsrc", "-map 1:a:0", "loop=loop=-1:size=1", "realtime", "-f mpegts srt://obs.lan:9000"} {
		if !strings.Contains(paused, want) {
			t.Errorf("paused srt args missing %q:\n%s", want, paused)
		}
	}
	if strings.Contains(paused, "-re ") {
		t.Error("paused args read input in real time instead of looping a frame")
	}
}

func TestCapabilityParsing(t *testing.T) {
	encoders := "Encoders:\n V..... = Video\n ------\n V....D libx264              libx264 H.264\n A....D aac                  AAC\n"
	muxers := "File formats:\n D. = Demuxing\n --\n  E flv             FLV (Flash Video)\n  E mpegts          MPEG-TS\n  E video4linux2,v4l2 Video4Linux2 output device\n"
	protocols := "Supported file protocols:\nInput:\n  file\n  rtmp\nOutput:\n  file\n  rtmp\n  rtmps\n"
	f := ffmpegFeatures{encoders: listed(encoders), muxers: listed(muxers), outProtocols: outputProtocols(protocols)}
	if !f.encoders["libx264"] || !f.encoders["aac"] || !f.muxers["flv"] || !f.muxers["v4l2"] || !f.outProtocols["rtmps"] || f.outProtocols["srt"] {
		t.Fatalf("parsed %+v", f)
	}
	modes := evaluateModes(Capabilities{Platform: "linux", FFmpeg: true, V4L2Loopback: true, Devices: []Device{{Path: "/dev/video10", Virtual: true}}}, f)
	if !modes[ModeRTMP].Available || !modes[ModeV4L2].Available {
		t.Fatalf("modes = %+v", modes)
	}
	if modes[ModeSRT].Available || !strings.Contains(modes[ModeSRT].Reason, "SRT") {
		t.Fatalf("srt = %+v", modes[ModeSRT])
	}
	modes = evaluateModes(Capabilities{Platform: "linux", FFmpeg: true, V4L2Loopback: true, Devices: []Device{{Path: "/dev/video0"}}}, f)
	if modes[ModeV4L2].Available {
		t.Fatal("physical camera treated as virtual output")
	}
	modes = evaluateModes(Capabilities{Platform: "linux", FFmpeg: true}, f)
	if !strings.Contains(modes[ModeV4L2].Reason, "not loaded") {
		t.Fatalf("no driver reason = %q", modes[ModeV4L2].Reason)
	}
	modes = evaluateModes(Capabilities{Platform: "windows"}, ffmpegFeatures{})
	if modes[ModeV4L2].Available || !strings.Contains(modes[ModeV4L2].Reason, "this server runs windows") || !strings.Contains(modes[ModeRTMP].Reason, "FFmpeg was not found") {
		t.Fatalf("windows without ffmpeg = %+v", modes)
	}
}
