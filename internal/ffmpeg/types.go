package ffmpeg

import "context"

type Stream struct {
	Index    int    `json:"index"`
	Kind     string `json:"kind"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Channels int    `json:"channels"`
	Default  bool   `json:"default"`
	Forced   bool   `json:"forced"`
	SDH      bool   `json:"sdh"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	BitDepth int    `json:"bit_depth"`
	HDR      string `json:"hdr"`
}

type MediaInfo struct {
	DurationMS int64    `json:"duration_ms"`
	Container  string   `json:"container"`
	Size       int64    `json:"size"`
	VideoCodec string   `json:"video_codec"`
	AudioCodec string   `json:"audio_codec"`
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	BitDepth   int      `json:"bit_depth"`
	HDR        string   `json:"hdr"`
	Streams    []Stream `json:"streams"`
}

type Prober interface {
	ProbeFile(ctx context.Context, path string) (*MediaInfo, error)
}

type Thumber interface {
	Thumb(ctx context.Context, src, dest string, atMS int64) error
}

type DetectResult struct {
	FFmpeg   string   `json:"ffmpeg"`
	FFprobe  string   `json:"ffprobe"`
	Version  string   `json:"version"`
	Encoders []string `json:"encoders"`
	Filters  []string `json:"filters"`
	HWAccel  []string `json:"hwaccel"`
	ZScale   bool     `json:"zscale"`
}

type Detector interface {
	Detect() DetectResult
}
