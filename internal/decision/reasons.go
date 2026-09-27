package decision

const (
	DirectUnprobed     = "DIRECT_UNPROBED"
	DirectPlay         = "DIRECT_PLAY"
	DirectVideoH264       = "DIRECT_VIDEO_H264"
	DirectVideoHEVC       = "DIRECT_VIDEO_HEVC"
	DirectVideoHEVCMain10 = "DIRECT_VIDEO_HEVC_MAIN10"
	DirectAudioAAC        = "DIRECT_AUDIO_AAC"
	DirectAudioMP3        = "DIRECT_AUDIO_MP3"
	DirectContainerMP4    = "DIRECT_CONTAINER_MP4"

	RemuxContainerMKV = "REMUX_CONTAINER_MKV"
	RemuxContainer    = "REMUX_CONTAINER"
	RemuxFMP4         = "REMUX_FMP4_HLS"
	RemuxHEVCTag      = "REMUX_HEVC_HVC1"

	TranscodeVideoHEVC       = "TRANSCODE_VIDEO_HEVC_UNSUPPORTED"
	TranscodeVideoHEVCMain10 = "TRANSCODE_VIDEO_HEVC_MAIN10_UNSUPPORTED"
	TranscodeVideoAV1        = "TRANSCODE_VIDEO_AV1_UNSUPPORTED"
	TranscodeVideo       = "TRANSCODE_VIDEO_CODEC"
	TranscodeVideoHDR    = "TRANSCODE_VIDEO_HDR"
	TranscodeAudioTrueHD = "TRANSCODE_AUDIO_TRUEHD"
	TranscodeAudioAC3    = "TRANSCODE_AUDIO_AC3"
	TranscodeAudioEAC3   = "TRANSCODE_AUDIO_EAC3"
	TranscodeAudioDTS    = "TRANSCODE_AUDIO_DTS"
	TranscodeAudio       = "TRANSCODE_AUDIO_UNSUPPORTED"

	BurnPGS    = "BURN_PGS"
	BurnVobSub = "BURN_VOBSUB"
	BurnASS    = "BURN_ASS"
	TextASSJS  = "TEXT_ASS_JS"
	TextSub    = "TEXT_SUBTITLE"

	QualityRemote    = "QUALITY_REMOTE_BITRATE"
	QualityViewport  = "QUALITY_VIEWPORT"
	QualityShare     = "QUALITY_SHARE_HEIGHT"
	QualityRequested = "QUALITY_REQUESTED"

	HWVAAPI        = "HW_VAAPI"
	HWNVENC        = "HW_NVENC"
	HWFallbackCPU  = "HW_FALLBACK_CPU"
	HWUnavailable  = "HW_UNAVAILABLE"
	RefuseNoZScale = "REFUSE_4K_HDR_NO_ZSCALE"

	// Native remux VOD cannot advertise equal-length copy segments.
	RemuxKeyframeIncomplete = "REMUX_KEYFRAME_INCOMPLETE_PROMOTE_XCODE"
)

const (
	ModeDirect       = "direct_play"
	ModeRemux        = "remux"
	ModeTranscodeVid = "transcode_video"
	ModeTranscodeAud = "transcode_audio"
	ModeTranscodeAV  = "transcode_av"
	ModeBurnSubs     = "burn_subs"
)

const (
	DeliveryDirect = "direct"
	DeliveryHLS    = "hls"
)

const (
	PlaybackDirect   = "Direct Play"
	PlaybackRemux    = "Remux"
	PlaybackPartial  = "Partial Transcode"
	PlaybackFull     = "Full Transcode"
	ActionCopy       = "COPY"
	ActionTranscode  = "TRANSCODE"
	ActionDirect     = "DIRECT"
	ActionRemux      = "REMUX"
)
