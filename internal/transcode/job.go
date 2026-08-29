package transcode

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/hwaccel"
	"github.com/viewdock/viewdock/internal/library"
)

type Opts struct {
	StartMS     int64
	AudioIndex  int
	VideoIndex  int
	Height      int
	SrcWidth    int
	SrcHeight   int
	HDR         string
	BurnPath    string
	SessionDir  string
	LibraryID   string
	AbsPath     string
	HW          hwaccel.Info
	SegmentTime int
	CopyVideo   bool
	CopyAudio   bool
	Stderr      io.Writer
}

func Start(ctx context.Context, ff *ffmpeg.Tool, locator library.MediaLocator, opt Opts) (*exec.Cmd, error) {
	if locator != nil {
		if err := locator.Contains(opt.LibraryID, opt.AbsPath); err != nil {
			return nil, fmt.Errorf("input path: %w", err)
		}
	}
	if (opt.SrcWidth >= 3840 || opt.SrcHeight >= 2160) && opt.HDR != "" && !opt.HW.ZScale {
		return nil, fmt.Errorf("REFUSE_4K_HDR_NO_ZSCALE")
	}
	if err := os.MkdirAll(opt.SessionDir, 0o755); err != nil {
		return nil, err
	}
	vf := BuildVF(opt.Height, opt.HDR, opt.HW.ZScale, opt.BurnPath)
	if err := ValidateChain(vf, opt.SessionDir); err != nil {
		return nil, err
	}
	if ff == nil {
		ff = ffmpeg.New()
	}
	if opt.SegmentTime <= 0 {
		opt.SegmentTime = 4
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	enc, hw := hwaccel.VideoEncoder(opt.HW)
	if hw == "vaapi" {
		args = append(args, "-hwaccel", "vaapi")
	}
	if opt.StartMS > 0 {
		args = append(args, "-ss", ffmpeg.FormatTS(opt.StartMS))
	}
	args = append(args, "-i", opt.AbsPath)
	vmap := "0:v:0"
	if opt.VideoIndex > 0 {
		vmap = fmt.Sprintf("0:%d", opt.VideoIndex)
	}
	amap := "0:a:0"
	if opt.AudioIndex > 0 {
		amap = fmt.Sprintf("0:%d", opt.AudioIndex)
	}
	args = append(args, "-map", vmap, "-map", amap)
	if opt.CopyVideo && opt.BurnPath == "" && (opt.Height <= 0 || opt.Height >= opt.SrcHeight) {
		args = append(args, "-c:v", "copy")
	} else {
		if vf != "" {
			args = append(args, "-vf", vf)
		}
		args = append(args, "-c:v", enc)
		if enc == "libx264" {
			args = append(args, "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p")
		}
	}
	if opt.CopyAudio {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "160k")
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", opt.SegmentTime),
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_flags", "independent_segments+append_list",
		"-hls_segment_filename", filepath.Join(opt.SessionDir, "seg%d.m4s"),
		filepath.Join(opt.SessionDir, "index.m3u8"),
	)
	cmd := exec.CommandContext(ctx, ff.FFmpeg, args...)
	setProcGroup(cmd)
	if opt.Stderr != nil {
		cmd.Stderr = opt.Stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func Kill(cmd *exec.Cmd) {
	ffmpeg.KillGroup(cmd)
}
