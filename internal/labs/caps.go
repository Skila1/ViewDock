package labs

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ModeStatus reports whether an output mode can run on this host.
type ModeStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Capabilities describes what this host can broadcast to.
type Capabilities struct {
	Platform      string                `json:"platform"`
	FFmpeg        bool                  `json:"ffmpeg"`
	FFmpegVersion string                `json:"ffmpeg_version,omitempty"`
	V4L2Loopback  bool                  `json:"v4l2loopback"`
	Devices       []Device              `json:"devices"`
	Modes         map[string]ModeStatus `json:"modes"`
	CheckedAt     time.Time             `json:"checked_at"`
}

type ffmpegFeatures struct {
	version                        string
	encoders, muxers, outProtocols map[string]bool
}

func runFFmpeg(ctx context.Context, bin string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, append([]string{"-hide_banner"}, args...)...).Output()
	return string(out), err
}

// listed parses "-encoders"/"-muxers" tables: a flags column, then names.
func listed(out string) map[string]bool {
	names := map[string]bool{}
	started := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			started = true
			continue
		}
		if !started {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, n := range strings.Split(f[1], ",") {
			names[n] = true
		}
	}
	return names
}

// outputProtocols parses the "Output:" section of "-protocols".
func outputProtocols(out string) map[string]bool {
	names := map[string]bool{}
	section := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())
		switch t {
		case "Input:", "Output:":
			section = t
			continue
		}
		if section == "Output:" && t != "" {
			names[t] = true
		}
	}
	return names
}

func probeFFmpeg(ctx context.Context, bin string) (ffmpegFeatures, bool) {
	var f ffmpegFeatures
	ver, err := runFFmpeg(ctx, bin, "-version")
	if err != nil {
		return f, false
	}
	if line, _, _ := strings.Cut(ver, "\n"); line != "" {
		f.version = strings.TrimSpace(strings.TrimPrefix(line, "ffmpeg version "))
		if len(f.version) > 80 {
			f.version = f.version[:80]
		}
	}
	enc, _ := runFFmpeg(ctx, bin, "-encoders")
	mux, _ := runFFmpeg(ctx, bin, "-muxers")
	proto, _ := runFFmpeg(ctx, bin, "-protocols")
	f.encoders, f.muxers, f.outProtocols = listed(enc), listed(mux), outputProtocols(proto)
	return f, true
}

// ProbeCapabilities inspects the FFmpeg binary and, on Linux, the
// v4l2loopback driver and devices.
func ProbeCapabilities(ctx context.Context, ffmpegBin string) Capabilities {
	c := Capabilities{Platform: runtime.GOOS, Devices: []Device{}, CheckedAt: time.Now().UTC()}
	var f ffmpegFeatures
	if ffmpegBin != "" {
		f, c.FFmpeg = probeFFmpeg(ctx, ffmpegBin)
		c.FFmpegVersion = f.version
	}
	if runtime.GOOS == "linux" {
		if _, err := os.Stat(filepath.Join(sysfs, "module", "v4l2loopback")); err == nil {
			c.V4L2Loopback = true
		}
		c.Devices = ListV4L2Devices()
	}
	c.Modes = evaluateModes(c, f)
	return c
}

func evaluateModes(c Capabilities, f ffmpegFeatures) map[string]ModeStatus {
	modes := map[string]ModeStatus{}
	noFF := ModeStatus{Reason: "FFmpeg was not found on this server. Install FFmpeg (the Docker image includes it) to use the broadcaster."}
	encode := func(container, protocol, protoHint string) ModeStatus {
		switch {
		case !c.FFmpeg:
			return noFF
		case !f.encoders["libx264"]:
			return ModeStatus{Reason: "This FFmpeg build has no libx264 encoder."}
		case !f.encoders["aac"]:
			return ModeStatus{Reason: "This FFmpeg build has no AAC encoder."}
		case !f.muxers[container]:
			return ModeStatus{Reason: "This FFmpeg build cannot write " + container + "."}
		case !f.outProtocols[protocol]:
			return ModeStatus{Reason: "This FFmpeg build has no " + protoHint + " output support."}
		}
		return ModeStatus{Available: true}
	}
	modes[ModeRTMP] = encode("flv", "rtmp", "RTMP")
	modes[ModeSRT] = encode("mpegts", "srt", "SRT (libsrt)")

	virtual := 0
	for _, d := range c.Devices {
		if d.Virtual {
			virtual++
		}
	}
	switch {
	case c.Platform != "linux":
		modes[ModeV4L2] = ModeStatus{Reason: "Virtual camera devices need Linux with the v4l2loopback driver; this server runs " + c.Platform + ". Send RTMP or SRT to a receiver such as OBS on the broadcasting computer and use its virtual camera instead."}
	case !c.FFmpeg:
		modes[ModeV4L2] = noFF
	case !f.muxers["v4l2"] && !f.muxers["video4linux2"]:
		modes[ModeV4L2] = ModeStatus{Reason: "This FFmpeg build has no v4l2 output."}
	case !c.V4L2Loopback:
		modes[ModeV4L2] = ModeStatus{Reason: "The v4l2loopback driver is not loaded. Install it and run: modprobe v4l2loopback video_nr=10 card_label=ViewDock exclusive_caps=1 (in Docker, also pass the device with --device /dev/video10)."}
	case virtual == 0:
		modes[ModeV4L2] = ModeStatus{Reason: "v4l2loopback is loaded but no virtual camera device is visible to this server (in Docker, pass it with --device)."}
	default:
		modes[ModeV4L2] = ModeStatus{Available: true}
	}
	return modes
}
