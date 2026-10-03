package jellyfin

import (
	"testing"

	"github.com/viewdock/viewdock/internal/capability"
)

func TestNegotiate(t *testing.T) {
	hdr4k := mediaSource{Container: "mkv", MediaStreams: []mediaStream{
		{Type: "Video", Codec: "hevc", Height: 2160, BitDepth: 10, VideoRangeType: "HDR10", BitRate: 70_000_000},
		{Type: "Audio", Codec: "truehd"},
	}}
	gpu := capability.Profile{Codecs: map[string]capability.CodecCap{
		"h264": {MaxHeight: 2160, Hardware: true},
		"hevc": {MaxHeight: 2160, Hardware: true, TenBit: true},
	}}
	softwareOnly := capability.Profile{Codecs: map[string]capability.CodecCap{
		"h264": {MaxHeight: 1080},
		"hevc": {MaxHeight: 1080, TenBit: true},
	}}
	h264Only := capability.Profile{Codecs: map[string]capability.CodecCap{"h264": {MaxHeight: 2160, Hardware: true}}}

	t.Run("hardware HEVC device gets the original 4K HDR video", func(t *testing.T) {
		p := negotiate(hdr4k, gpu, "auto", true)
		if !p.copy || p.direct || p.codec != "hevc" || p.container != "mp4" || p.maxHeight != 0 {
			t.Fatalf("plan %+v", p)
		}
		q := hlsQuery("m", "p", "d", p)
		if q.Get("VideoCodec") != "h264,hevc" || q.Get("hevc-rangetype") != "HDR10" || q.Get("hevc-videobitdepth") != "10" || q.Get("SegmentContainer") != "mp4" {
			t.Fatalf("query %v", q)
		}
		if p.videoRate < 70_000_000 {
			t.Fatalf("copy ceiling %d is below the source, so Jellyfin would re-encode", p.videoRate)
		}
	})
	t.Run("4K without a hardware decoder is re-encoded to 1080p", func(t *testing.T) {
		p := negotiate(hdr4k, softwareOnly, "", true)
		if p.copy || p.codec != "h264" || p.maxHeight != 1080 || p.maxWidth != 1920 || p.videoRate != transcodeMaxBitrate {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("no HEVC decoder", func(t *testing.T) {
		p := negotiate(hdr4k, h264Only, "auto", true)
		if p.copy || p.maxHeight != 1080 || hlsQuery("m", "p", "d", p).Get("VideoCodec") != "h264" {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("Dolby Vision without a base layer is re-encoded", func(t *testing.T) {
		dv := mediaSource{MediaStreams: []mediaStream{{Type: "Video", Codec: "hevc", Height: 2160, BitDepth: 10, VideoRangeType: "DOVI"}}}
		if p := negotiate(dv, gpu, "auto", true); p.copy {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("browser-ready MP4 plays directly", func(t *testing.T) {
		mp4 := mediaSource{Container: "mp4", MediaStreams: []mediaStream{{Type: "Video", Codec: "h264", Height: 1080}, {Type: "Audio", Codec: "aac"}}}
		if p := negotiate(mp4, h264Only, "auto", true); !p.direct {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("H.264 in MKV is copied into TS segments", func(t *testing.T) {
		mkv := mediaSource{Container: "mkv", MediaStreams: []mediaStream{{Type: "Video", Codec: "h264", Height: 1080, Level: 41}, {Type: "Audio", Codec: "dts"}}}
		p := negotiate(mkv, h264Only, "auto", true)
		if !p.copy || p.direct || p.container != "ts" || hlsQuery("m", "p", "d", p).Get("VideoCodec") != "h264" {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("10-bit H.264 is re-encoded", func(t *testing.T) {
		hi10 := mediaSource{MediaStreams: []mediaStream{{Type: "Video", Codec: "h264", Height: 1080, BitDepth: 10}}}
		if p := negotiate(hi10, gpu, "auto", true); p.copy {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("a quality preset always re-encodes", func(t *testing.T) {
		p := negotiate(hdr4k, gpu, "720", true)
		if p.copy || p.maxHeight != 720 || p.videoRate != 5_000_000 {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("SD re-encodes keep their size", func(t *testing.T) {
		sd := mediaSource{MediaStreams: []mediaStream{{Type: "Video", Codec: "mpeg2video", Height: 480, BitRate: 6_000_000}}}
		p := negotiate(sd, gpu, "auto", true)
		if p.copy || p.maxHeight != 0 || p.videoRate != 6_000_000 {
			t.Fatalf("plan %+v", p)
		}
	})
	t.Run("old clients without measured codecs fall back to 1080p flags", func(t *testing.T) {
		yes := true
		old := capability.Profile{HEVC: &yes, HEVCMain10: &yes, UserAgent: "Mozilla/5.0 (Macintosh) Version/17.0 Safari/605.1.15"}
		fhd := mediaSource{MediaStreams: []mediaStream{{Type: "Video", Codec: "hevc", Height: 1080, BitDepth: 10}}}
		if p := negotiate(fhd, old, "auto", true); !p.copy {
			t.Fatalf("1080p plan %+v", p)
		}
		if p := negotiate(hdr4k, old, "auto", true); p.copy {
			t.Fatalf("4K plan %+v", p)
		}
	})
}
