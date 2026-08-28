package decision

import (
	"strings"

	"github.com/viewdock/viewdock/internal/capability"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/hwaccel"
)

type Input struct {
	Info          *ffmpeg.MediaInfo
	Client        capability.Profile
	Quality       string // auto|1080|720|480
	LAN           bool
	ShareMaxH     int
	RemoteBitrate int64 // bits/s cap when !LAN; 0 = default 8Mbps
	AudioIndex    int
	SubtitleIndex *int
	HW            hwaccel.Info
}

type Result struct {
	Mode           string   `json:"mode"`
	Delivery       string   `json:"delivery"`
	HLSAttach      string   `json:"hls_attach"`
	Reasons        []string `json:"reasons"`
	Height         int      `json:"height"`
	NeedVideoXcode bool     `json:"-"`
	NeedAudioXcode bool     `json:"-"`
	NeedBurn       bool     `json:"-"`
	CopyVideo      bool     `json:"-"`
	CopyAudio      bool     `json:"-"`
	HEVCRemuxTag   bool     `json:"-"`
	Refuse         string   `json:"-"`
	SubAction      string   `json:"-"` // extract|burn|none
}

func Decide(in Input) Result {
	r := Result{
		HLSAttach: in.Client.HLSAttach(),
		Reasons:   []string{},
		CopyVideo: true,
		CopyAudio: true,
	}
	if in.Info == nil || (in.Info.VideoCodec == "" && in.Info.Container == "" && len(in.Info.Streams) == 0) {
		r.Mode = ModeDirect
		r.Delivery = DeliveryDirect
		r.Reasons = []string{DirectUnprobed}
		return r
	}
	v := videoStream(in.Info, in)
	a := audioStream(in.Info, in.AudioIndex)
	srcH := in.Info.Height
	if srcH == 0 && v != nil {
		srcH = v.Height
	}
	r.Height = PickHeight(srcH, in.Client.ViewportH, in.LAN, in.Quality, in.ShareMaxH, in.Info, in.RemoteBitrate)

	vcodec := ""
	if v != nil {
		vcodec = v.Codec
	} else {
		vcodec = in.Info.VideoCodec
	}
	acodec := ""
	if a != nil {
		acodec = a.Codec
	} else {
		acodec = in.Info.AudioCodec
	}

	vOK, vReason := videoDirect(vcodec, in.Client)
	aOK, aReason := audioDirect(acodec, in.Client)
	cOK, cReason := containerDirect(in.Info.Container)

	if !vOK {
		r.NeedVideoXcode = true
		r.CopyVideo = false
		if vReason != "" {
			r.Reasons = append(r.Reasons, vReason)
		}
	} else if vReason != "" {
		r.Reasons = append(r.Reasons, vReason)
	}
	if !aOK {
		r.NeedAudioXcode = true
		r.CopyAudio = false
		if aReason != "" {
			r.Reasons = append(r.Reasons, aReason)
		}
	} else if aReason != "" {
		r.Reasons = append(r.Reasons, aReason)
	}

	sub := selectedSub(in.Info, in.SubtitleIndex)
	if sub != nil {
		action, reason := subtitleAction(*sub, in.Client)
		r.SubAction = action
		if reason != "" {
			r.Reasons = append(r.Reasons, reason)
		}
		if action == "burn" {
			r.NeedBurn = true
			r.NeedVideoXcode = true
			r.CopyVideo = false
		}
	} else {
		r.SubAction = "none"
	}

	if r.Height > 0 && srcH > 0 && r.Height < srcH {
		r.NeedVideoXcode = true
		r.CopyVideo = false
		if !in.LAN {
			r.Reasons = append(r.Reasons, QualityRemote)
		} else if in.ShareMaxH > 0 && r.Height <= in.ShareMaxH && (in.Quality == "" || in.Quality == "auto") {
			r.Reasons = append(r.Reasons, QualityShare)
		} else if in.Quality != "" && in.Quality != "auto" {
			r.Reasons = append(r.Reasons, QualityRequested)
		} else {
			r.Reasons = append(r.Reasons, QualityViewport)
		}
	}

	hdr := in.Info.HDR
	if hdr == "" && v != nil {
		hdr = v.HDR
	}
	w := in.Info.Width
	if w == 0 && v != nil {
		w = v.Width
	}
	if is4K(w, srcH) && hdr != "" && !in.HW.ZScale && r.NeedVideoXcode {
		r.Refuse = RefuseNoZScale
		r.Reasons = append(r.Reasons, RefuseNoZScale)
	}

	if r.NeedVideoXcode && hdr != "" && in.Client.Bool("hdr") == false {
		r.Reasons = appendUnique(r.Reasons, TranscodeVideoHDR)
	}

	if r.NeedVideoXcode && r.NeedAudioXcode {
		r.Mode = ModeTranscodeAV
	} else if r.NeedBurn && !r.NeedAudioXcode && !needVideoBesidesBurn(vOK, r.Height, srcH) {
		r.Mode = ModeBurnSubs
	} else if r.NeedVideoXcode {
		r.Mode = ModeTranscodeVid
	} else if r.NeedAudioXcode {
		r.Mode = ModeTranscodeAud
	} else if cOK {
		r.Mode = ModeDirect
		r.Reasons = append(r.Reasons, DirectPlay)
		if cReason != "" {
			r.Reasons = appendUnique(r.Reasons, cReason)
		}
	} else {
		r.Mode = ModeRemux
		if cReason != "" {
			r.Reasons = append(r.Reasons, cReason)
		} else {
			r.Reasons = append(r.Reasons, RemuxContainer)
		}
		r.Reasons = append(r.Reasons, RemuxFMP4)
		if isHEVC(vcodec) {
			r.HEVCRemuxTag = true
			r.Reasons = append(r.Reasons, RemuxHEVCTag)
		}
	}

	if r.Mode == ModeDirect {
		r.Delivery = DeliveryDirect
	} else {
		r.Delivery = DeliveryHLS
	}

	if r.NeedVideoXcode {
		if in.HW.VAAPI {
			r.Reasons = appendUnique(r.Reasons, HWVAAPI)
		} else if in.HW.NVENC {
			r.Reasons = appendUnique(r.Reasons, HWNVENC)
		} else {
			r.Reasons = appendUnique(r.Reasons, HWFallbackCPU)
		}
	}

	r.Reasons = unique(r.Reasons)
	return r
}

func needVideoBesidesBurn(vOK bool, destH, srcH int) bool {
	if !vOK {
		return true
	}
	return destH > 0 && srcH > 0 && destH < srcH
}

func videoDirect(codec string, c capability.Profile) (bool, string) {
	switch normalizeVideo(codec) {
	case "h264":
		return true, DirectVideoH264
	case "hevc":
		if c.Bool("hevc") {
			return true, ""
		}
		return false, TranscodeVideoHEVC
	case "av1":
		if c.Bool("av1") {
			return true, ""
		}
		return false, TranscodeVideoAV1
	case "":
		return false, TranscodeVideo
	default:
		return false, TranscodeVideo
	}
}

func audioDirect(codec string, c capability.Profile) (bool, string) {
	switch normalizeAudio(codec) {
	case "aac":
		return true, DirectAudioAAC
	case "mp3":
		return true, DirectAudioMP3
	case "ac3":
		if c.Bool("ac3") {
			return true, ""
		}
		return false, TranscodeAudioAC3
	case "eac3":
		if c.Bool("eac3") {
			return true, ""
		}
		return false, TranscodeAudioEAC3
	case "truehd":
		return false, TranscodeAudioTrueHD
	case "dts", "dts-hd", "dca":
		return false, TranscodeAudioDTS
	case "":
		return true, ""
	default:
		return false, TranscodeAudio
	}
}

func containerDirect(c string) (bool, string) {
	n := strings.ToLower(c)
	if isISOBMFF(n) {
		return true, DirectContainerMP4
	}
	if strings.Contains(n, "mkv") || strings.Contains(n, "matroska") {
		return false, RemuxContainerMKV
	}
	return false, RemuxContainer
}

func isISOBMFF(n string) bool {
	return n == "mp4" || n == "mov" || n == "m4v" || n == "isom" ||
		strings.Contains(n, "mp4") || n == "quicktime"
}

func normalizeVideo(c string) string {
	c = strings.ToLower(c)
	switch c {
	case "h264", "avc", "avc1":
		return "h264"
	case "hevc", "h265", "hvc1", "hev1":
		return "hevc"
	case "av1", "av01":
		return "av1"
	default:
		return c
	}
}

func normalizeAudio(c string) string {
	c = strings.ToLower(c)
	switch c {
	case "aac", "mp4a":
		return "aac"
	case "mp3", "mp3float":
		return "mp3"
	case "ac3", "ac-3":
		return "ac3"
	case "eac3", "ec-3", "eac-3":
		return "eac3"
	case "truehd", "mlp":
		return "truehd"
	default:
		return c
	}
}

func isHEVC(c string) bool { return normalizeVideo(c) == "hevc" }

func is4K(w, h int) bool { return w >= 3840 || h >= 2160 }

func videoStream(info *ffmpeg.MediaInfo, in Input) *ffmpeg.Stream {
	for i := range info.Streams {
		if info.Streams[i].Kind == "video" {
			return &info.Streams[i]
		}
	}
	return nil
}

func audioStream(info *ffmpeg.MediaInfo, idx int) *ffmpeg.Stream {
	var first *ffmpeg.Stream
	for i := range info.Streams {
		s := &info.Streams[i]
		if s.Kind != "audio" {
			continue
		}
		if first == nil {
			first = s
		}
		if idx > 0 && s.Index == idx {
			return s
		}
	}
	if idx == 0 {
		return first
	}
	return first
}

func selectedSub(info *ffmpeg.MediaInfo, idx *int) *ffmpeg.Stream {
	if idx == nil {
		return nil
	}
	for i := range info.Streams {
		s := &info.Streams[i]
		if s.Kind == "subtitle" && s.Index == *idx {
			return s
		}
	}
	return nil
}

func subtitleAction(s ffmpeg.Stream, c capability.Profile) (string, string) {
	codec := strings.ToLower(s.Codec)
	switch {
	case codec == "hdmv_pgs_subtitle" || codec == "pgs" || codec == "pgssub":
		return "burn", BurnPGS
	case codec == "dvd_subtitle" || codec == "dvdsub" || codec == "vobsub":
		return "burn", BurnVobSub
	case codec == "ass" || codec == "ssa":
		if c.Bool("ass_js") {
			return "extract", TextASSJS
		}
		return "burn", BurnASS
	default:
		return "extract", TextSub
	}
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func unique(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
