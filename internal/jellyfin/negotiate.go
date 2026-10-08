package jellyfin

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/viewdock/viewdock/internal/capability"
)

// Jellyfin sizes its encoder from VideoBitrate. Without one, hardware encoders
// fall back to a low default.
const (
	autoMaxBitrate     = 80_000_000
	autoUnknownBitrate = 20_000_000
	audioBitrate       = 320_000
	// A copied stream is never re-encoded, so its ceiling only has to stay
	// above the source: Jellyfin re-encodes whenever the source exceeds it.
	copyMaxBitrate = 400_000_000
	// Re-encoding in real time is what Jellyfin struggles with, so an auto
	// re-encode is capped at 1080p and this bitrate.
	transcodeMaxHeight  = 1080
	transcodeMaxWidth   = 1920
	transcodeMaxBitrate = 20_000_000
)

type qualityPreset struct {
	maxHeight, maxWidth int
	videoBitrate        int64
}

// qualityPresets match the quality choices the player offers for local files.
var qualityPresets = map[string]qualityPreset{
	"1080": {maxHeight: 1080, maxWidth: 1920, videoBitrate: 10_000_000},
	"720":  {maxHeight: 720, maxWidth: 1280, videoBitrate: 5_000_000},
	"480":  {maxHeight: 480, maxWidth: 854, videoBitrate: 2_000_000},
}

// browserRanges are the dynamic ranges a browser shows itself, tone mapping
// HDR on SDR screens. Dolby Vision qualifies when it has an HDR10, HLG or
// SDR base layer (profiles 7, 8 and UHD Blu-ray remuxes with an enhancement
// layer): Jellyfin drops the Dolby Vision data and copies the base layer.
// Profile 5 ("DOVI") has no such layer and shows wrong colours without it.
var browserRanges = map[string]bool{
	"": true, "SDR": true, "HDR10": true, "HDR10Plus": true, "HLG": true,
	"DOVIWithHDR10": true, "DOVIWithHDR10Plus": true, "DOVIWithHLG": true, "DOVIWithSDR": true,
	"DOVIWithEL": true, "DOVIWithELHDR10Plus": true,
}

// copyRanges is what ViewDock tells Jellyfin the browser shows. Leaving
// Dolby Vision out makes Jellyfin copy a Dolby Vision source as its base layer.
const copyRanges = "SDR,HDR10,HDR10Plus,HLG"

// streamPlan is what ViewDock asks Jellyfin for, for one device.
type streamPlan struct {
	// direct streams the file as is; otherwise Jellyfin serves HLS.
	direct bool
	// copy sends the original video inside HLS instead of re-encoding it.
	copy bool
	// codec is the video codec the player receives.
	codec string
	// container is the HLS segment container: "ts", or "mp4" for HEVC and AV1.
	container           string
	videoRate           int64
	maxHeight, maxWidth int
	// extra holds Jellyfin's per-codec limits ("hevc-rangetype", ...).
	extra url.Values
	// why says what the source is and, for a re-encode, why it was needed.
	why string
}

func videoStream(ms mediaSource) mediaStream {
	for _, st := range ms.MediaStreams {
		if st.Type == "Video" {
			return st
		}
	}
	return mediaStream{}
}

func normalCodec(c string) string {
	switch c = strings.ToLower(c); c {
	case "h265", "hevc":
		return "hevc"
	case "avc", "h264":
		return "h264"
	}
	return c
}

func (st mediaStream) tenBit() bool {
	return st.BitDepth > 8 || (st.VideoRangeType != "" && st.VideoRangeType != "SDR")
}

// negotiate decides what to ask Jellyfin for. When the device decodes the
// source video (codec, bit depth, dynamic range and size, with hardware
// decoding for 4K), Jellyfin sends the original video and only converts the
// audio. Otherwise it re-encodes to H.264, capped at 1080p for auto quality
// so the encode keeps up. A quality preset always re-encodes to that preset.
func negotiate(ms mediaSource, client capability.Profile, quality string, transcode bool) streamPlan {
	v := videoStream(ms)
	src := normalCodec(v.Codec)
	height := v.Height
	if height <= 0 {
		height = 1080
	}
	preset, capped := qualityPresets[quality]
	if capped && !transcode {
		capped = false
	}
	srcRate := autoVideoBitrate(ms)
	depthNote := "8-bit"
	if v.tenBit() {
		depthNote = "10-bit"
	}
	rangeNote := v.VideoRangeType
	if rangeNote == "" {
		rangeNote = "SDR"
	}
	source := fmt.Sprintf("source %s %dp %s %s", src, height, depthNote, rangeNote)

	var reason string
	switch {
	case capped:
		reason = "quality " + quality + " was chosen"
	case src != "h264" && src != "hevc" && src != "av1":
		reason = "browsers do not play " + src
	case !browserRanges[v.VideoRangeType]:
		reason = "browsers cannot show the " + rangeNote + " dynamic range"
	case src == "h264" && v.tenBit():
		reason = "browsers do not play 10-bit H.264"
	default:
		if limit := client.DecodeLimit(src, v.tenBit()); limit == 0 {
			reason = "the device has no " + depthNote + " " + src + " decoder"
		} else if height > limit {
			reason = fmt.Sprintf("the device decodes %s up to %dp only (no hardware decoder for more)", src, limit)
		}
	}
	if reason == "" {
		p := streamPlan{copy: true, codec: src, container: "ts", extra: url.Values{}, why: source}
		if src == "h264" && browserPlayable(ms) {
			p.direct = true
			return p
		}
		p.videoRate = min(max(2*videoRateOf(ms), transcodeMaxBitrate), copyMaxBitrate)
		switch src {
		case "h264":
			if lvl := int(v.Level); lvl > 51 {
				p.extra.Set("h264-level", fmt.Sprint(lvl))
			}
		default:
			p.container = "mp4"
			depth := v.BitDepth
			if depth < 8 {
				depth = 8
			}
			if v.tenBit() && depth < 10 {
				depth = 10
			}
			p.extra.Set(src+"-videobitdepth", fmt.Sprint(depth))
			p.extra.Set(src+"-rangetype", copyRanges)
		}
		return p
	}

	p := streamPlan{codec: "h264", container: "ts", videoRate: srcRate, extra: url.Values{}, why: source + ": " + reason}
	if capped {
		p.maxHeight, p.maxWidth, p.videoRate = preset.maxHeight, preset.maxWidth, preset.videoBitrate
		return p
	}
	maxH := transcodeMaxHeight
	if lim := client.DecodeLimit("h264", false); lim > 0 && lim < maxH {
		maxH = lim
	}
	if height > maxH {
		p.maxHeight = maxH
		p.maxWidth = maxH * 16 / 9
		if maxH == transcodeMaxHeight {
			p.maxWidth = transcodeMaxWidth
		}
	}
	p.videoRate = min(p.videoRate, transcodeMaxBitrate)
	return p
}

// planBitrate is the video bitrate the player receives: the source's when
// copied, the encoder's target when re-encoded.
func planBitrate(p streamPlan, ms mediaSource) int64 {
	if p.copy || p.direct {
		if r := videoRateOf(ms); r != copyMaxBitrate/2 {
			return r
		}
		return 0
	}
	return p.videoRate
}

func videoRateOf(ms mediaSource) int64 {
	var rate int64
	for _, st := range ms.MediaStreams {
		if st.Type == "Video" && st.BitRate > rate {
			rate = st.BitRate
		}
	}
	if rate <= 0 && ms.Bitrate > audioBitrate {
		rate = ms.Bitrate - audioBitrate
	}
	if rate <= 0 {
		return copyMaxBitrate / 2
	}
	return rate
}

// autoVideoBitrate is the file's own video bitrate, for re-encodes: a
// transcode stays at the original quality without asking for far more.
func autoVideoBitrate(ms mediaSource) int64 {
	var rate int64
	for _, st := range ms.MediaStreams {
		if st.Type == "Video" && st.BitRate > rate {
			rate = st.BitRate
		}
	}
	if rate <= 0 && ms.Bitrate > audioBitrate {
		rate = ms.Bitrate - audioBitrate
	}
	if rate <= 0 {
		rate = autoUnknownBitrate
	}
	if rate > autoMaxBitrate-audioBitrate {
		rate = autoMaxBitrate - audioBitrate
	}
	return rate
}

// hlsSegmentSeconds is the segment length asked of Jellyfin. Copied video
// is cut on keyframes, so segments run up to the next one after this.
const hlsSegmentSeconds = "3"

func hlsQuery(mediaSourceID, playID, deviceID string, p streamPlan) url.Values {
	codecs := "h264"
	if p.copy && p.codec != "h264" {
		// h264 first: it is Jellyfin's target if it has to re-encode after all.
		codecs = "h264," + p.codec
	}
	q := url.Values{
		"MediaSourceId": {mediaSourceID}, "PlaySessionId": {playID}, "DeviceId": {deviceID},
		"VideoCodec": {codecs}, "AudioCodec": {"aac"},
		"VideoBitrate": {fmt.Sprint(p.videoRate)}, "AudioBitrate": {fmt.Sprint(audioBitrate)},
		"MaxStreamingBitrate": {fmt.Sprint(p.videoRate + audioBitrate)}, "TranscodingMaxAudioChannels": {"2"},
		"SegmentContainer": {p.container}, "AllowVideoStreamCopy": {"true"}, "AllowAudioStreamCopy": {"true"},
		"BreakOnNonKeyFrames": {"true"}, "h264-level": {"51"},
		// Short segments: a remux segment of Jellyfin's default length can
		// pass 100 MB, and two of them do not fit in a browser's media
		// buffer (about 150 MB in Chromium), which then stops for good.
		"SegmentLength": {hlsSegmentSeconds},
		"h264-profile": {"high,main,baseline,constrained baseline"},
	}
	for k, vs := range p.extra {
		q[k] = vs
	}
	if p.maxHeight > 0 {
		q.Set("MaxHeight", fmt.Sprint(p.maxHeight))
		q.Set("MaxWidth", fmt.Sprint(p.maxWidth))
	}
	return q
}
