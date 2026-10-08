package hwaccel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

type Info struct {
	Available       bool     `json:"available"`
	VAAPI           bool     `json:"vaapi"`
	NVENC           bool     `json:"nvenc"`
	H264NVENC       bool     `json:"h264_nvenc,omitempty"`
	HEVCNVENC       bool     `json:"hevc_nvenc,omitempty"`
	AV1NVENC        bool     `json:"av1_nvenc,omitempty"`
	NVDEC           bool     `json:"nvdec,omitempty"`
	DetectionReason string   `json:"detection_reason,omitempty"`
	ZScale          bool     `json:"zscale"`
	Encoders        []string `json:"encoders"`
	HWAccel         []string `json:"hwaccel"`
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
	compiledVAAPI := false
	listedH264NVENC := false
	for _, h := range d.HWAccel {
		switch strings.ToLower(h) {
		case "vaapi":
			compiledVAAPI = true
		case "nvdec":
			info.NVDEC = true
		}
	}
	for _, e := range d.Encoders {
		el := strings.ToLower(e)
		if strings.Contains(el, "h264_nvenc") {
			listedH264NVENC = true
		}
		if strings.Contains(el, "hevc_nvenc") {
			info.HEVCNVENC = true
		}
		if strings.Contains(el, "av1_nvenc") {
			info.AV1NVENC = true
		}
		if strings.Contains(el, "vaapi") {
			compiledVAAPI = true
		}
	}
	// FFmpeg lists vaapi whenever it was compiled in. Only use it when a
	// device node is actually present or transcode dies immediately.
	if compiledVAAPI && hasVAAPI() {
		info.VAAPI = true
	}
	// NVIDIA device is a hint only. NVENC/H264NVENC stay false until Apply
	// proves h264_nvenc can encode a frame.
	nvidiaHint := hasNVIDIA()
	if listedH264NVENC {
		info.DetectionReason = "nvenc_listed"
	} else if nvidiaHint {
		info.DetectionReason = "nvenc_not_listed"
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

func isForceCPU(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "cpu", "none", "software":
		return true
	}
	return false
}

func hwAccelMode() string {
	for _, key := range []string{"VD_HWACCEL", "VD_HW_ACCEL"} {
		v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		if v != "" && v != "auto" && !isForceCPU(v) {
			return v
		}
	}
	return ""
}

func hasEncoder(encoders []string, name string) bool {
	for _, e := range encoders {
		if strings.Contains(strings.ToLower(e), strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// Apply disables hardware encode when the operator asked for CPU, or when a
// compiled-in backend cannot actually initialize a device. /dev/dri often
// exists on machines with no usable GPU; ffmpeg then dies on the first frame.
// FFmpeg usability is authoritative for NVENC — not /dev/nvidia0 alone.
func Apply(info Info, ffmpegBin string) Info {
	if isForceCPU(os.Getenv("VD_HWACCEL")) || isForceCPU(os.Getenv("VD_HW_ACCEL")) {
		info.VAAPI = false
		info.NVENC = false
		info.H264NVENC = false
		info.Available = false
		info.DetectionReason = "forced_cpu"
		return info
	}
	mode := hwAccelMode()
	switch mode {
	case "vaapi":
		info.NVENC = false
		info.H264NVENC = false
	case "nvenc", "cuda":
		info.VAAPI = false
	}
	if info.VAAPI && ffmpegBin != "" && !probeVAAPI(ffmpegBin) {
		info.VAAPI = false
	}

	listed := hasEncoder(info.Encoders, "h264_nvenc")
	wantNVENC := mode != "vaapi"
	info.NVENC = false
	info.H264NVENC = false
	if wantNVENC {
		switch {
		case strings.TrimSpace(ffmpegBin) == "":
			if listed {
				info.DetectionReason = "nvenc_listed"
			} else {
				info.DetectionReason = "nvenc_not_listed"
			}
		case !listed && mode != "nvenc" && mode != "cuda":
			info.DetectionReason = "nvenc_not_listed"
		case probeNVENC(ffmpegBin):
			info.NVENC = true
			info.H264NVENC = true
			info.DetectionReason = "nvidia_nvenc_available"
		default:
			if listed {
				info.DetectionReason = "nvenc_probe_failed"
			} else {
				info.DetectionReason = "nvenc_not_listed"
			}
		}
	}

	info.Available = info.VAAPI || info.NVENC
	return info
}

func probeVAAPI(bin string) bool {
	matches, _ := filepath.Glob("/dev/dri/renderD*")
	if len(matches) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"-hide_banner", "-loglevel", "error",
		"-init_hw_device", "vaapi=va:"+matches[0],
		"-f", "lavfi", "-i", "nullsrc=s=64x64:d=0.1",
		"-frames:v", "1", "-f", "null", "-",
	)
	out, err := cmd.CombinedOutput()
	return err == nil && !DeviceFailed(string(out))
}

func probeNVENC(bin string) bool {
	if strings.TrimSpace(bin) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "nullsrc=s=64x64:d=0.1",
		"-c:v", "h264_nvenc", "-frames:v", "1", "-f", "null", "-",
	)
	out, err := cmd.CombinedOutput()
	return err == nil && !DeviceFailed(string(out))
}

func DeviceFailed(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "device creation failed") ||
		strings.Contains(s, "no device available") ||
		strings.Contains(s, "device setup failed") ||
		strings.Contains(s, "cannot load libcuda") ||
		strings.Contains(s, "no nvenc capable devices") ||
		strings.Contains(s, "cannot load libnvidia-encode") ||
		strings.Contains(s, "no capable devices found") ||
		strings.Contains(s, "createbitstreambuffer")
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
	if info.NVENC || info.H264NVENC {
		return "h264_nvenc", "nvenc"
	}
	return "libx264", ""
}

// StartupMessage is a single info-level line for playback startup.
func StartupMessage(info Info) string {
	switch {
	case info.NVENC || info.H264NVENC:
		return "Hardware acceleration: NVIDIA NVENC available"
	case info.VAAPI:
		return "Hardware acceleration: VAAPI available"
	default:
		return "Hardware acceleration: unavailable, using CPU transcoding"
	}
}
