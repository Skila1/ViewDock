package ffmpeg

import (
	"context"
	"os/exec"
	"sync"
)

// Tool implements Prober, Thumber, and Detector.
type Tool struct {
	FFmpeg  string
	FFprobe string

	once   sync.Once
	cached DetectResult
}

func New() *Tool {
	ff, probe := lookPaths()
	return &Tool{FFmpeg: ff, FFprobe: probe}
}

func lookPaths() (ffmpeg, ffprobe string) {
	ffmpeg, _ = exec.LookPath("ffmpeg")
	ffprobe, _ = exec.LookPath("ffprobe")
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	return ffmpeg, ffprobe
}

func (t *Tool) bin(kind string) string {
	if t == nil {
		ff, pr := lookPaths()
		if kind == "ffprobe" {
			return pr
		}
		return ff
	}
	if kind == "ffprobe" {
		if t.FFprobe != "" {
			return t.FFprobe
		}
		return "ffprobe"
	}
	if t.FFmpeg != "" {
		return t.FFmpeg
	}
	return "ffmpeg"
}

func Available() bool {
	_, err1 := exec.LookPath("ffmpeg")
	_, err2 := exec.LookPath("ffprobe")
	return err1 == nil && err2 == nil
}

func (t *Tool) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	setProcGroup(cmd)
	return cmd
}
