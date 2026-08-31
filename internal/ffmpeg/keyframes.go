package ffmpeg

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Keyframes returns packet PTS of video keyframes in milliseconds.
// Incomplete results should be discarded by the caller (see hls.KeyframesCover).
func (t *Tool) Keyframes(ctx context.Context, path string) ([]int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
	}
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "packet=pts_time,flags",
		"-of", "csv=p=0",
		path,
	}
	cmd := t.command(ctx, t.bin("ffprobe"), args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return ParseKeyframeCSV(string(out)), nil
}

func ParseKeyframeCSV(raw string) []int64 {
	var out []int64
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}
		if !strings.Contains(parts[1], "K") {
			continue
		}
		sec, err := strconv.ParseFloat(parts[0], 64)
		if err != nil || sec < 0 {
			continue
		}
		out = append(out, int64(sec*1000+0.5))
	}
	return out
}
