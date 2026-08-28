package decision

import (
	"slices"
	"testing"

	"github.com/viewdock/viewdock/internal/capability"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/hwaccel"
)

func info(v, a, c string, w, h int) *ffmpeg.MediaInfo {
	return &ffmpeg.MediaInfo{
		VideoCodec: v, AudioCodec: a, Container: c, Width: w, Height: h,
		Streams: []ffmpeg.Stream{
			{Index: 0, Kind: "video", Codec: v, Width: w, Height: h},
			{Index: 1, Kind: "audio", Codec: a},
		},
	}
}

func has(reasons []string, code string) bool {
	return slices.Contains(reasons, code)
}

func TestDecideTable(t *testing.T) {
	winChrome := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36"
	chrome := "Mozilla/5.0 Chrome/120.0.0.0"
	sub := func(idx int, codec string) *ffmpeg.MediaInfo {
		m := info("h264", "aac", "mkv", 1920, 1080)
		m.Streams = append(m.Streams, ffmpeg.Stream{Index: idx, Kind: "subtitle", Codec: codec})
		return m
	}
	pgs := 2
	ass := 2

	cases := []struct {
		name    string
		in      Input
		mode    string
		reason  string
		deliver string
	}{
		{
			name: "unprobed direct",
			in:   Input{Info: &ffmpeg.MediaInfo{}, Client: capability.Profile{}, Quality: "auto", LAN: true},
			mode: ModeDirect, reason: DirectUnprobed, deliver: DeliveryDirect,
		},
		{
			name: "h264 aac mp4 direct",
			in:   Input{Info: info("h264", "aac", "mp4", 1920, 1080), Client: capability.Profile{}, Quality: "auto", LAN: true},
			mode: ModeDirect, reason: DirectPlay, deliver: DeliveryDirect,
		},
		{
			name: "h264 mp3 mp4 direct",
			in:   Input{Info: info("h264", "mp3", "mp4", 1280, 720), Client: capability.Profile{}, Quality: "auto", LAN: true},
			mode: ModeDirect, reason: DirectAudioMP3, deliver: DeliveryDirect,
		},
		{
			name: "h264 aac mkv remux",
			in:   Input{Info: info("h264", "aac", "mkv", 1920, 1080), Client: capability.Profile{}, Quality: "auto", LAN: true},
			mode: ModeRemux, reason: RemuxContainerMKV, deliver: DeliveryHLS,
		},
		{
			name: "windows chrome hevc",
			in: Input{
				Info:    info("hevc", "aac", "mp4", 1920, 1080),
				Client:  capability.Profile{UserAgent: winChrome, HEVC: capability.Ptr(true)},
				Quality: "auto", LAN: true,
			},
			mode: ModeTranscodeVid, reason: TranscodeVideoHEVC, deliver: DeliveryHLS,
		},
		{
			name: "truehd",
			in: Input{
				Info:   info("h264", "truehd", "mkv", 1920, 1080),
				Client: capability.Profile{}, Quality: "auto", LAN: true,
			},
			mode: ModeTranscodeAud, reason: TranscodeAudioTrueHD, deliver: DeliveryHLS,
		},
		{
			name: "chrome ac3",
			in: Input{
				Info:    info("h264", "ac3", "mp4", 1920, 1080),
				Client:  capability.Profile{UserAgent: chrome, AC3: capability.Ptr(true)},
				Quality: "auto", LAN: true,
			},
			mode: ModeTranscodeAud, reason: TranscodeAudioAC3, deliver: DeliveryHLS,
		},
		{
			name: "pgs burn",
			in: Input{
				Info: sub(2, "hdmv_pgs_subtitle"), SubtitleIndex: &pgs,
				Client: capability.Profile{}, Quality: "auto", LAN: true,
			},
			mode: ModeBurnSubs, reason: BurnPGS, deliver: DeliveryHLS,
		},
		{
			name: "ass burn without js",
			in: Input{
				Info: sub(2, "ass"), SubtitleIndex: &ass,
				Client: capability.Profile{ASSJS: capability.Ptr(false)}, Quality: "auto", LAN: true,
			},
			mode: ModeBurnSubs, reason: BurnASS, deliver: DeliveryHLS,
		},
		{
			name: "ass js extract still remux",
			in: Input{
				Info: sub(2, "ass"), SubtitleIndex: &ass,
				Client: capability.Profile{ASSJS: capability.Ptr(true)}, Quality: "auto", LAN: true,
			},
			mode: ModeRemux, reason: TextASSJS, deliver: DeliveryHLS,
		},
		{
			name: "remote 4k quality",
			in: Input{
				Info:   info("h264", "aac", "mp4", 3840, 2160),
				Client: capability.Profile{ViewportH: 1080}, Quality: "auto", LAN: false,
			},
			mode: ModeTranscodeVid, reason: QualityRemote, deliver: DeliveryHLS,
		},
		{
			name: "av1 unsupported",
			in: Input{
				Info:   info("av1", "aac", "mp4", 1920, 1080),
				Client: capability.Profile{AV1: capability.Ptr(false)}, Quality: "auto", LAN: true,
			},
			mode: ModeTranscodeVid, reason: TranscodeVideoAV1, deliver: DeliveryHLS,
		},
		{
			name: "4k hdr no zscale refuse",
			in: Input{
				Info: &ffmpeg.MediaInfo{VideoCodec: "hevc", AudioCodec: "aac", Container: "mkv", Width: 3840, Height: 2160, HDR: "hdr10",
					Streams: []ffmpeg.Stream{{Kind: "video", Codec: "hevc", Width: 3840, Height: 2160, HDR: "hdr10"}, {Kind: "audio", Codec: "aac"}}},
				Client: capability.Profile{UserAgent: winChrome}, Quality: "auto", LAN: true,
				HW: hwaccel.Info{ZScale: false},
			},
			mode: ModeTranscodeVid, reason: RefuseNoZScale, deliver: DeliveryHLS,
		},
		{
			name: "hw fallback cpu",
			in: Input{
				Info:   info("hevc", "aac", "mp4", 1920, 1080),
				Client: capability.Profile{UserAgent: winChrome}, Quality: "auto", LAN: true,
				HW: hwaccel.Info{},
			},
			mode: ModeTranscodeVid, reason: HWFallbackCPU, deliver: DeliveryHLS,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.in)
			if got.Mode != tc.mode {
				t.Fatalf("mode %s want %s reasons=%v", got.Mode, tc.mode, got.Reasons)
			}
			if got.Delivery != tc.deliver {
				t.Fatalf("delivery %s", got.Delivery)
			}
			if !has(got.Reasons, tc.reason) {
				t.Fatalf("missing %s in %v", tc.reason, got.Reasons)
			}
		})
	}
}
