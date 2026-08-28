package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func (t *Tool) Thumb(ctx context.Context, src, dest string, atMS int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if atMS < 0 {
		atMS = 0
	}
	ss := fmt.Sprintf("%d.%03d", atMS/1000, atMS%1000)
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-ss", ss,
		"-i", src,
		"-frames:v", "1",
		"-q:v", "3",
		"-y",
		dest,
	}
	cmd := t.command(ctx, t.bin("ffmpeg"), args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg thumb: %w (%s)", err, truncate(string(out), 400))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func FormatTS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return strconv.FormatFloat(float64(ms)/1000.0, 'f', 3, 64)
}
