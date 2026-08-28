package hwaccel

import (
	"strings"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

type Info struct {
	Available bool     `json:"available"`
	VAAPI     bool     `json:"vaapi"`
	NVENC     bool     `json:"nvenc"`
	ZScale    bool     `json:"zscale"`
	Encoders  []string `json:"encoders"`
	HWAccel   []string `json:"hwaccel"`
}

func FromDetect(d ffmpeg.DetectResult) Info {
	info := Info{
		ZScale:   d.ZScale,
		Encoders: d.Encoders,
		HWAccel:  d.HWAccel,
	}
	if info.Encoders == nil {
		info.Encoders = []string{}
	}
	if info.HWAccel == nil {
		info.HWAccel = []string{}
	}
	for _, h := range d.HWAccel {
		switch strings.ToLower(h) {
		case "vaapi":
			info.VAAPI = true
		case "cuda", "nvenc", "nvdec":
			info.NVENC = true
		}
	}
	for _, e := range d.Encoders {
		el := strings.ToLower(e)
		if strings.Contains(el, "nvenc") {
			info.NVENC = true
		}
		if strings.Contains(el, "vaapi") {
			info.VAAPI = true
		}
	}
	info.Available = info.VAAPI || info.NVENC
	return info
}

func Detect(d ffmpeg.Detector) Info {
	if d == nil {
		return Info{Encoders: []string{}, HWAccel: []string{}}
	}
	return FromDetect(d.Detect())
}

// CanTranscodeHDR4K reports whether a 4K HDR transcode may start.
// Missing zscale refuses the job; the app still starts without NVENC.
func CanTranscodeHDR4K(info Info, width, height int, hdr string) (ok bool, reason string) {
	if hdr == "" {
		return true, ""
	}
	if width < 3840 && height < 2160 {
		return true, ""
	}
	if !info.ZScale {
		return false, "REFUSE_4K_HDR_NO_ZSCALE"
	}
	return true, ""
}

func VideoEncoder(info Info) (name string, hw string) {
	if info.VAAPI {
		return "h264_vaapi", "vaapi"
	}
	if info.NVENC {
		return "h264_nvenc", "nvenc"
	}
	return "libx264", ""
}
