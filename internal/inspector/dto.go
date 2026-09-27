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
	Playback    string    `json:"playback"`
	Mode        string    `json:"mode"`
	Delivery    string    `json:"delivery"`
	Reasons     []string  `json:"reasons"`
	Height      int       `json:"height"`
	Encoder     string    `json:"encoder,omitempty"`
	EncoderType string    `json:"encoder_type,omitempty"` // cpu | nvidia_nvenc
	Container   StreamCol `json:"container"`
	Video       StreamCol `json:"video"`
	Audio       StreamCol `json:"audio"`
	Hardware    string    `json:"hardware"`
}

type GPU struct {
	Available       bool   `json:"available"`
	Vendor          string `json:"vendor,omitempty"` // "nvidia" when NVENC
	VAAPI           bool   `json:"vaapi"`
	NVENC           bool   `json:"nvenc"`
	Encoder         string `json:"encoder,omitempty"` // h264_nvenc or libx264
	HWAccel         string `json:"hwaccel,omitempty"`
	GPUUsed         bool   `json:"gpu_used"`
	Fallback        bool   `json:"fallback,omitempty"`
	FallbackReason  string `json:"fallback_reason,omitempty"`
	DetectionReason string `json:"detection_reason,omitempty"`
}

type DTO struct {
	ID            string             `json:"id"`
	Source        Source             `json:"source"`
	Client        capability.Profile `json:"client"`
	Decision      DecisionCol        `json:"decision"`
	GPU           *GPU               `json:"gpu"`
	VODOnDemand   bool               `json:"vod_ondemand,omitempty"`
	VODPlanKind   string             `json:"vod_plan_kind,omitempty"`
	GenStartSeg   int                `json:"gen_start_seg,omitempty"`
	GenerationID  int                `json:"generation_id,omitempty"`
	HLSAttach     string             `json:"hls_attach,omitempty"`
	SeekableFrom  int64              `json:"seekable_from_ms,omitempty"`
	OriginMS      int64              `json:"origin_ms"`
}

type Input struct {
	ID              string
	Container       string
	VideoCodec      string
	AudioCodec      string
	Width           int
	Height          int
	BitDepth        int
	HDR             string
	DurationMS      int64
	Size            int64
	Client          capability.Profile
	Mode            string
	Delivery        string
	Reasons         []string
	OutHeight       int
	Encoder         string
	GPUAvail        bool
	VAAPI           bool
	NVENC           bool
	HWAccel         string
	Playback        string
	Hardware        string
	NeedVideoXcode  bool
	GPUUsed         bool
	Fallback        bool
	FallbackReason  string
	DetectionReason string
	Vendor          string
	Video           StreamCol
	Audio           StreamCol
	Cont            StreamCol
	VODOnDemand     bool
	VODPlanKind     string
	GenStartSeg     int
	GenerationID    int
	HLSAttach       string
	SeekableFrom    int64
}

func sessionGPUUsed(in Input) bool {
	if in.Fallback || in.Encoder != "h264_nvenc" {
		return false
	}
	return in.GPUUsed || in.NeedVideoXcode
}

func encoderType(enc string) string {
	switch enc {
	case "h264_nvenc":
		return "nvidia_nvenc"
	case "":
		return ""
	default:
		return "cpu"
	}
}

func Build(in Input) DTO {
	d := DTO{
		ID: in.ID,
		Source: Source{
			Container: in.Container, VideoCodec: in.VideoCodec, AudioCodec: in.AudioCodec,
			Width: in.Width, Height: in.Height, BitDepth: in.BitDepth, HDR: in.HDR,
			DurationMS: in.DurationMS, Size: in.Size,
		},
		Client:       in.Client,
		VODOnDemand:  in.VODOnDemand,
		VODPlanKind:  in.VODPlanKind,
		GenStartSeg:  in.GenStartSeg,
		GenerationID: in.GenerationID,
		HLSAttach:    in.HLSAttach,
		SeekableFrom: in.SeekableFrom,
		OriginMS:     in.SeekableFrom,
		Decision: DecisionCol{
			Playback: in.Playback, Mode: in.Mode, Delivery: in.Delivery, Reasons: in.Reasons,
			Height: in.OutHeight, Encoder: in.Encoder, EncoderType: encoderType(in.Encoder),
			Hardware: in.Hardware, Container: in.Cont, Video: in.Video, Audio: in.Audio,
		},
	}
	if in.VODOnDemand {
		d.OriginMS = 0
	}
	if in.Reasons == nil {
		d.Decision.Reasons = []string{}
	}
	if d.Decision.Hardware == "" {
		d.Decision.Hardware = "Not required"
	}
	used := sessionGPUUsed(in)
	if in.GPUAvail || used || in.Fallback {
		enc := in.Encoder
		if enc == "" {
			enc = "libx264"
		}
		vendor := in.Vendor
		if vendor == "" && (in.NVENC || used) {
			vendor = "nvidia"
		}
		d.GPU = &GPU{
			Available:       in.GPUAvail,
			Vendor:          vendor,
			VAAPI:           in.VAAPI,
			NVENC:           in.NVENC,
			Encoder:         enc,
			HWAccel:         in.HWAccel,
			GPUUsed:         used,
			Fallback:        in.Fallback,
			FallbackReason:  in.FallbackReason,
			DetectionReason: in.DetectionReason,
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
