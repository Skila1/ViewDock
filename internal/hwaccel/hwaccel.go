package hwaccel

import (
	"os"
	"path/filepath"
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
	return fromDetect(d, vaapiDevice, nvidiaDevice)
}

func fromDetect(d ffmpeg.DetectResult, hasVAAPI, hasNVIDIA func() bool) Info {
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
	compiledVAAPI, compiledNVENC := false, false
	for _, h := range d.HWAccel {
		switch strings.ToLower(h) {
		case "vaapi":
			compiledVAAPI = true
		case "cuda", "nvenc", "nvdec":
			compiledNVENC = true
		}
	}
	for _, e := range d.Encoders {
		el := strings.ToLower(e)
		if strings.Contains(el, "nvenc") {
			compiledNVENC = true
		}
		if strings.Contains(el, "vaapi") {
			compiledVAAPI = true
		}
	}
	// FFmpeg lists vaapi/nvenc whenever they were compiled in. Only use them
	// when a device node is actually present or transcode dies immediately.
	if compiledVAAPI && hasVAAPI() {
		info.VAAPI = true
	}
	if compiledNVENC && hasNVIDIA() {
		info.NVENC = true
	}
	info.Available = info.VAAPI || info.NVENC
	return info
}

func vaapiDevice() bool {
	matches, _ := filepath.Glob("/dev/dri/renderD*")
	return len(matches) > 0
}

func nvidiaDevice() bool {
	for _, p := range []string{"/dev/nvidia0", "/dev/nvidiactl"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func DeviceFailed(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "device creation failed") ||
		strings.Contains(s, "no device available") ||
		strings.Contains(s, "device setup failed") ||
		strings.Contains(s, "cannot load libcuda") ||
		strings.Contains(s, "no nvenc capable devices")
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
