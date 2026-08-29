package inspector

import "github.com/viewdock/viewdock/internal/capability"

type Source struct {
	Container  string `json:"container"`
	VideoCodec string `json:"video_codec"`
	AudioCodec string `json:"audio_codec"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	BitDepth   int    `json:"bit_depth"`
	HDR        string `json:"hdr"`
	DurationMS int64  `json:"duration_ms"`
	Size       int64  `json:"size"`
}

type StreamCol struct {
	Codec  string `json:"codec"`
	Action string `json:"action"`
	To     string `json:"to,omitempty"`
	Reason string `json:"reason"`
}

type DecisionCol struct {
	Playback  string    `json:"playback"`
	Mode      string    `json:"mode"`
	Delivery  string    `json:"delivery"`
	Reasons   []string  `json:"reasons"`
	Height    int       `json:"height"`
	Encoder   string    `json:"encoder,omitempty"`
	Container StreamCol `json:"container"`
	Video     StreamCol `json:"video"`
	Audio     StreamCol `json:"audio"`
	Hardware  string    `json:"hardware"`
}

type GPU struct {
	Available bool   `json:"available"`
	VAAPI     bool   `json:"vaapi"`
	NVENC     bool   `json:"nvenc"`
	Encoder   string `json:"encoder,omitempty"`
	HWAccel   string `json:"hwaccel,omitempty"`
}

type DTO struct {
	ID       string             `json:"id"`
	Source   Source             `json:"source"`
	Client   capability.Profile `json:"client"`
	Decision DecisionCol        `json:"decision"`
	GPU      *GPU               `json:"gpu"`
}

type Input struct {
	ID         string
	Container  string
	VideoCodec string
	AudioCodec string
	Width      int
	Height     int
	BitDepth   int
	HDR        string
	DurationMS int64
	Size       int64
	Client     capability.Profile
	Mode       string
	Delivery   string
	Reasons    []string
	OutHeight  int
	Encoder    string
	GPUAvail   bool
	VAAPI      bool
	NVENC      bool
	HWAccel    string
	Playback   string
	Hardware   string
	Video      StreamCol
	Audio      StreamCol
	Cont       StreamCol
}

func Build(in Input) DTO {
	d := DTO{
		ID: in.ID,
		Source: Source{
			Container: in.Container, VideoCodec: in.VideoCodec, AudioCodec: in.AudioCodec,
			Width: in.Width, Height: in.Height, BitDepth: in.BitDepth, HDR: in.HDR,
			DurationMS: in.DurationMS, Size: in.Size,
		},
		Client: in.Client,
		Decision: DecisionCol{
			Playback: in.Playback, Mode: in.Mode, Delivery: in.Delivery, Reasons: in.Reasons,
			Height: in.OutHeight, Encoder: in.Encoder, Hardware: in.Hardware,
			Container: in.Cont, Video: in.Video, Audio: in.Audio,
		},
	}
	if in.Reasons == nil {
		d.Decision.Reasons = []string{}
	}
	if d.Decision.Hardware == "" {
		d.Decision.Hardware = "Not required"
	}
	if in.GPUAvail {
		d.GPU = &GPU{
			Available: true, VAAPI: in.VAAPI, NVENC: in.NVENC,
			Encoder: in.Encoder, HWAccel: in.HWAccel,
		}
	}
	return d
}

type LiveRow struct {
	ID         string   `json:"id"`
	ItemKind   string   `json:"item_kind"`
	ItemID     string   `json:"item_id"`
	Mode       string   `json:"mode"`
	Playback   string   `json:"playback,omitempty"`
	Delivery   string   `json:"delivery"`
	Reasons    []string `json:"reasons"`
	UserID     string   `json:"user_id,omitempty"`
	Guest      bool     `json:"guest"`
	DurationMS int64    `json:"duration_ms"`
}

type Stats struct {
	Sessions        int  `json:"sessions"`
	Direct          int  `json:"direct"`
	HLS             int  `json:"hls"`
	TranscodeActive int  `json:"transcode_active"`
	TranscodeSlots  int  `json:"transcode_slots"`
	HWAvailable     bool `json:"hwaccel_available"`
}
