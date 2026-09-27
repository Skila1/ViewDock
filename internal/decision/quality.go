package decision

import (
	"strconv"
	"strings"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

const defaultRemoteBitrate = 8_000_000

func PickHeight(srcH, viewportH int, lan bool, quality string, shareMax int, info *ffmpeg.MediaInfo, remoteCap int64) int {
	if srcH <= 0 {
		srcH = 1080
	}
	want := 0
	q := strings.ToLower(strings.TrimSpace(quality))
	switch q {
	case "1080", "720", "480":
		want, _ = strconv.Atoi(q)
	case "auto", "":
		want = autoHeight(srcH, viewportH, lan, info, remoteCap)
	default:
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			want = snap(n)
		} else {
			want = autoHeight(srcH, viewportH, lan, info, remoteCap)
		}
	}
	if shareMax > 0 && (want == 0 || want > shareMax) {
		want = snap(shareMax)
	}
	if want <= 0 || want >= srcH {
		return srcH
	}
	return want
}

func autoHeight(srcH, viewportH int, lan bool, info *ffmpeg.MediaInfo, remoteCap int64) int {
	capH := srcH
	if !lan {
		limit := 720
		br := estimateBitrate(info)
		if remoteCap <= 0 {
			remoteCap = defaultRemoteBitrate
		}
		if br > remoteCap || srcH >= 1080 {
			limit = 720
		}
		if srcH >= 2160 {
			limit = 720
		}
		if capH > limit {
			capH = limit
		}
	} else if capH > 1080 {
		capH = 1080
	}
	if viewportH > 0 {
		v := snap(viewportH)
		if v < capH {
			capH = v
		}
	}
	return snap(capH)
}

func estimateBitrate(info *ffmpeg.MediaInfo) int64 {
	if info == nil || info.DurationMS <= 0 || info.Size <= 0 {
		return 0
	}
	return info.Size * 8 * 1000 / info.DurationMS
}

func snap(h int) int {
	switch {
	case h <= 0:
		return 480
	case h <= 480:
		return 480
	case h <= 720:
		return 720
	default:
		return 1080
	}
}

func ParseQualityHeight(q string) int {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "1080":
		return 1080
	case "720":
		return 720
	case "480":
		return 480
	}
	return 0
}
