package subtitle

import (
	"context"
	"os/exec"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

func execCmd(ctx context.Context, ff *ffmpeg.Tool, args []string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	bin := "ffmpeg"
	if ff != nil && ff.FFmpeg != "" {
		bin = ff.FFmpeg
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	return cmd
}
