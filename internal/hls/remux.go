package hls

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

type RemuxOpts struct {
	StartMS     int64
	AudioIndex  int
	VideoIndex  int
	HEVC        bool
	SegmentTime int
	Stderr      io.Writer
}

func Remux(ctx context.Context, ff *ffmpeg.Tool, src, destDir string, opt RemuxOpts) (*exec.Cmd, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	if opt.SegmentTime <= 0 {
		opt.SegmentTime = 4
	}
	if ff == nil {
		ff = ffmpeg.New()
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if opt.StartMS > 0 {
		args = append(args, "-ss", ffmpeg.FormatTS(opt.StartMS))
	}
	vmap := "0:v:0"
	if opt.VideoIndex > 0 {
		vmap = fmt.Sprintf("0:%d", opt.VideoIndex)
	}
	amap := "0:a:0"
	if opt.AudioIndex > 0 {
		amap = fmt.Sprintf("0:%d", opt.AudioIndex)
	}
	args = append(args,
		"-i", src,
		"-map", vmap, "-map", amap,
		"-c", "copy",
	)
	if opt.HEVC {
		args = append(args, "-tag:v", "hvc1")
	}
	args = append(args, hlsArgs(destDir, opt.SegmentTime)...)
	cmd := exec.CommandContext(ctx, ff.FFmpeg, args...)
	ffmpegSetGroup(cmd)
	cmd.Dir = destDir
	if opt.Stderr != nil {
		cmd.Stderr = opt.Stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func hlsArgs(destDir string, seg int) []string {
	return []string{
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", seg),
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_flags", "independent_segments+append_list",
		"-hls_segment_filename", filepath.Join(destDir, "seg%d.m4s"),
		filepath.Join(destDir, "index.m3u8"),
	}
}

func ffmpegSetGroup(cmd *exec.Cmd) {
	setProcGroup(cmd)
}

func Kill(cmd *exec.Cmd) {
	ffmpeg.KillGroup(cmd)
}
