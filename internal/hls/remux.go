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
	StartMS      int64
	StartNumber  int
	InitFilename string
	AudioIndex   int
	VideoIndex   int
	HEVC         bool
	SegmentTime  int
	Stderr       io.Writer
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
	args := remuxInputFlags()
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
		"-map_chapters", "-1", "-dn",
		"-map", vmap, "-map", amap,
		"-c", "copy",
		"-muxpreload", "0",
		"-muxdelay", "0",
		"-flush_packets", "1",
	)
	if opt.HEVC {
		args = append(args, "-tag:v", "hvc1")
	}
	args = append(args, hlsArgs(destDir, opt.SegmentTime, opt.StartNumber, opt.InitFilename)...)
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

func remuxInputFlags() []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-fflags", "+fastseek+discardcorrupt",
		"-analyzeduration", "1000000",
		"-probesize", "5000000",
	}
}

func hlsArgs(destDir string, seg, startNumber int, initName string) []string {
	if initName == "" {
		initName = "init.mp4"
	}
	args := []string{
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", seg),
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", initName,
		"-hls_flags", "independent_segments",
	}
	if startNumber > 0 {
		args = append(args, "-start_number", fmt.Sprintf("%d", startNumber))
	}
	args = append(args,
		"-hls_segment_filename", filepath.Join(destDir, "seg%d.m4s"),
		filepath.Join(destDir, "index.m3u8"),
	)
	return args
}

func ffmpegSetGroup(cmd *exec.Cmd) {
	setProcGroup(cmd)
}

func Kill(cmd *exec.Cmd) {
	ffmpeg.KillGroup(cmd)
}
