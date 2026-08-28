package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type probeFile struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

type probeStream struct {
	Index            int               `json:"index"`
	CodecName        string            `json:"codec_name"`
	CodecType        string            `json:"codec_type"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	Channels         int               `json:"channels"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	BitsPerSample    int               `json:"bits_per_sample"`
	PixFmt           string            `json:"pix_fmt"`
	ColorTransfer    string            `json:"color_transfer"`
	ColorPrimaries   string            `json:"color_primaries"`
	ColorSpace       string            `json:"color_space"`
	Tags             map[string]string `json:"tags"`
	Disposition      map[string]int    `json:"disposition"`
	SideData         []probeSide       `json:"side_data_list"`
}

type probeSide struct {
	Type string `json:"side_data_type"`
}

type probeFormat struct {
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	FormatName string `json:"format_name"`
	BitRate    string `json:"bit_rate"`
}

func (t *Tool) ProbeFile(ctx context.Context, path string) (*MediaInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	}
	cmd := t.command(ctx, t.bin("ffprobe"), args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	info, err := ParseProbeJSON(out)
	if err != nil {
		return nil, err
	}
	if info.Size == 0 {
		if st, e := os.Stat(path); e == nil {
			info.Size = st.Size()
		}
	}
	return info, nil
}

func ParseProbeJSON(raw []byte) (*MediaInfo, error) {
	var pf probeFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return nil, fmt.Errorf("ffprobe json: %w", err)
	}
	info := &MediaInfo{
		Container: containerOf(pf.Format.FormatName),
		Size:      atoi64(pf.Format.Size),
	}
	if d := parseDurationMS(pf.Format.Duration); d > 0 {
		info.DurationMS = d
	}
	for _, s := range pf.Streams {
		st := Stream{
			Index:    s.Index,
			Kind:     kindOf(s.CodecType),
			Codec:    strings.ToLower(s.CodecName),
			Language: tag(s.Tags, "language", "LANGUAGE"),
			Title:    tag(s.Tags, "title", "TITLE"),
			Channels: s.Channels,
			Width:    s.Width,
			Height:   s.Height,
			BitDepth: bitDepth(s),
			HDR:      hdrOf(s),
			Default:  s.Disposition["default"] == 1,
			Forced:   s.Disposition["forced"] == 1 || titleHas(s, "forced"),
			SDH:      s.Disposition["hearing_impaired"] == 1 || titleHas(s, "sdh", "cc", "hi"),
		}
		info.Streams = append(info.Streams, st)
		switch st.Kind {
		case "video":
			if info.VideoCodec == "" {
				info.VideoCodec = st.Codec
				info.Width = st.Width
				info.Height = st.Height
				info.BitDepth = st.BitDepth
				info.HDR = st.HDR
			}
		case "audio":
			if info.AudioCodec == "" {
				info.AudioCodec = st.Codec
			}
		}
	}
	if info.Streams == nil {
		info.Streams = []Stream{}
	}
	return info, nil
}

func kindOf(t string) string {
	switch strings.ToLower(t) {
	case "video":
		return "video"
	case "audio":
		return "audio"
	case "subtitle":
		return "subtitle"
	case "attachment":
		return "attachment"
	default:
		return strings.ToLower(t)
	}
}

func containerOf(formatName string) string {
	n := strings.ToLower(formatName)
	switch {
	case strings.Contains(n, "mp4"), strings.Contains(n, "isom"), strings.Contains(n, "m4v"):
		return "mp4"
	case strings.Contains(n, "mov"):
		return "mov"
	case strings.Contains(n, "matroska"), strings.Contains(n, "mkv"):
		return "mkv"
	case strings.Contains(n, "webm"):
		return "webm"
	case strings.Contains(n, "mpegts"), strings.Contains(n, "mpeg-ts"):
		return "ts"
	case strings.Contains(n, "avi"):
		return "avi"
	default:
		if i := strings.IndexByte(n, ','); i > 0 {
			return n[:i]
		}
		return n
	}
}

func hdrOf(s probeStream) string {
	for _, sd := range s.SideData {
		t := strings.ToLower(sd.Type)
		if strings.Contains(t, "dolby") || strings.Contains(t, "dovi") {
			return "dolby_vision"
		}
		if strings.Contains(t, "mastering") || strings.Contains(t, "content light") {
			return "hdr10"
		}
	}
	tr := strings.ToLower(s.ColorTransfer)
	switch {
	case strings.Contains(tr, "smpte2084"), strings.Contains(tr, "pq"):
		return "hdr10"
	case strings.Contains(tr, "arib-std-b67"), strings.Contains(tr, "hlg"):
		return "hlg"
	}
	return ""
}

func bitDepth(s probeStream) int {
	if n, err := strconv.Atoi(s.BitsPerRawSample); err == nil && n > 0 {
		return n
	}
	if s.BitsPerSample > 0 {
		return s.BitsPerSample
	}
	p := strings.ToLower(s.PixFmt)
	switch {
	case strings.Contains(p, "p10"), strings.Contains(p, "10le"):
		return 10
	case strings.Contains(p, "p12"):
		return 12
	case strings.Contains(p, "p16"):
		return 16
	}
	return 8
}

func tag(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; v != "" {
			return v
		}
	}
	return ""
}

func titleHas(s probeStream, tokens ...string) bool {
	t := strings.ToLower(s.Tags["title"] + " " + s.Tags["TITLE"])
	for _, tok := range tokens {
		if strings.Contains(t, tok) {
			return true
		}
	}
	return false
}

func parseDurationMS(s string) int64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if d, err2 := time.ParseDuration(s + "s"); err2 == nil {
			return d.Milliseconds()
		}
		return 0
	}
	return int64(f * 1000)
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func ContentType(container string) string {
	switch strings.ToLower(container) {
	case "mp4", "mov", "m4v", "isom":
		return "video/mp4"
	case "mkv", "matroska":
		return "video/x-matroska"
	case "webm":
		return "video/webm"
	case "ts", "mpegts":
		return "video/mp2t"
	case "avi":
		return "video/x-msvideo"
	default:
		return "application/octet-stream"
	}
}
