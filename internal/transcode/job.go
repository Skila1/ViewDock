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
	HEVC        bool
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
	if ff == nil {
		ff = ffmpeg.New()
	}
	args, err := BuildArgs(opt)
	if err != nil {
		return nil, err
	}
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

// BuildArgs is the FFmpeg argv for a transcode or partial-copy HLS job.
func BuildArgs(opt Opts) ([]string, error) {
	vf := BuildVF(opt.Height, opt.HDR, opt.HW.ZScale, opt.BurnPath)
	if err := ValidateChain(vf, opt.SessionDir); err != nil {
		return nil, err
	}
	enc, hw := hwaccel.VideoEncoder(opt.HW)
	if opt.SegmentTime <= 0 {
		if hw == "" {
			opt.SegmentTime = 2
		} else {
			opt.SegmentTime = 4
		}
	}
	copyV := opt.CopyVideo && opt.BurnPath == "" && (opt.Height <= 0 || opt.Height >= opt.SrcHeight)
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if hw == "vaapi" && !copyV {
		args = append(args, "-hwaccel", "vaapi")
	}
	if opt.StartMS > 0 {
		args = append(args, "-ss", ffmpeg.FormatTS(opt.StartMS))
	}
	args = append(args, "-i", opt.AbsPath, "-map_chapters", "-1", "-dn")
	vmap := "0:v:0"
	if opt.VideoIndex > 0 {
		vmap = fmt.Sprintf("0:%d", opt.VideoIndex)
	}
	amap := "0:a:0"
	if opt.AudioIndex > 0 {
		amap = fmt.Sprintf("0:%d", opt.AudioIndex)
	}
	args = append(args, "-map", vmap, "-map", amap)
	if copyV {
		args = append(args, "-c:v", "copy")
		if opt.HEVC {
			args = append(args, "-tag:v", "hvc1")
		}
	} else {
		if vf != "" {
			args = append(args, "-vf", vf)
		}
		args = append(args, "-c:v", enc)
		if enc == "libx264" {
			args = append(args, "-preset", "ultrafast", "-crf", "23",
				"-pix_fmt", "yuv420p", "-profile:v", "main", "-level", "4.0",
				"-g", "48", "-keyint_min", "48", "-sc_threshold", "0")
		}
		if enc == "h264_nvenc" {
			args = append(args, "-preset", "p1", "-tune", "ll", "-rc", "constqp", "-qp", "23",
				"-pix_fmt", "yuv420p", "-profile:v", "main", "-level", "4.0", "-g", "48")
		}
	}
	if opt.CopyAudio {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "160k")
	}
	args = append(args, "-max_muxing_queue_size", "2048")
	// Always EVENT fMP4. hls.js cannot demux EC-3/AC-3 from MPEG-TS
	// ("Unsupported EC-3 in M2TS") even when Safari MMS reports eac3.
	args = append(args, hlsFMP4(opt.SessionDir, opt.SegmentTime)...)
	return args, nil
}

func hlsFMP4(dir string, seg int) []string {
	return []string{
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", seg),
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg%d.m4s"),
		filepath.Join(dir, "index.m3u8"),
	}
}

func Kill(cmd *exec.Cmd) {
	ffmpeg.KillGroup(cmd)
}
