package labs

import (
	"fmt"
	"path/filepath"
	"strconv"
)

// Preview snapshot names inside the work directory.
const (
	PreviewOutgoing = "outgoing.jpg"
	PreviewRaw      = "raw.jpg"
	previewEvery    = 2 // seconds between preview frames
)

// SourceState is what the selection shows right now.
type SourceState struct {
	// Path is a local media file this process can read.
	Path       string
	ItemKind   string
	ItemID     string
	Title      string
	PositionMS int64
	Playing    bool
}

// BuildArgs returns FFmpeg arguments that render src to the configured
// output, write preview snapshots of the outgoing and raw frames to workDir,
// and report progress on stdout. output is the validated output URL.
func BuildArgs(cfg Config, src SourceState, output, workDir string) []string {
	ss := strconv.FormatFloat(float64(max64(src.PositionMS, 0))/1000, 'f', 3, 64)
	// stdin stays open (no -nostdin): it carries the graceful "q" command.
	args := []string{"-hide_banner", "-loglevel", "info", "-nostats", "-progress", "pipe:1", "-threads", strconv.Itoa(cfg.Threads)}
	if src.Playing {
		args = append(args, "-re", "-ss", ss, "-i", src.Path)
	} else {
		args = append(args, "-ss", ss, "-i", src.Path)
	}
	network := cfg.Mode != ModeV4L2
	if network && !src.Playing {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
	}

	video := "[0:v:0]"
	if !src.Playing {
		// Hold the frame at the pause position, paced in real time.
		video += fmt.Sprintf("trim=end_frame=1,loop=loop=-1:size=1:start=0,setpts=N/(%d*TB),realtime,", cfg.FPS)
	}
	graph := fmt.Sprintf("%ssplit=2[src][rawp];"+
		"[rawp]fps=1/%d,scale=640:-2[rawo];"+
		"[src]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d,format=yuv420p,split=2[out][outp];"+
		"[outp]fps=1/%d[outo]",
		video, previewEvery, cfg.Width, cfg.Height, cfg.Width, cfg.Height, cfg.FPS, previewEvery)
	args = append(args, "-filter_complex", graph)

	switch cfg.Mode {
	case ModeV4L2:
		args = append(args, "-map", "[out]", "-an", "-f", "v4l2", "-pix_fmt", "yuv420p", cfg.Device)
	default:
		gop := strconv.Itoa(cfg.FPS * 2)
		vk := strconv.Itoa(cfg.VideoKbps) + "k"
		args = append(args, "-map", "[out]")
		if src.Playing {
			args = append(args, "-map", "0:a:0?")
		} else {
			args = append(args, "-map", "1:a:0")
		}
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency", "-pix_fmt", "yuv420p",
			"-b:v", vk, "-maxrate", vk, "-bufsize", strconv.Itoa(cfg.VideoKbps*2)+"k", "-g", gop, "-keyint_min", gop,
			"-c:a", "aac", "-b:a", strconv.Itoa(cfg.AudioKbps)+"k", "-ar", "48000", "-ac", "2",
			"-af", "astats=metadata=1:reset=12,ametadata=mode=print:key=lavfi.astats.Overall.RMS_level",
		)
		if cfg.Mode == ModeSRT {
			args = append(args, "-f", "mpegts", output)
		} else {
			args = append(args, "-f", "flv", output)
		}
	}
	for _, p := range [][2]string{{"[outo]", PreviewOutgoing}, {"[rawo]", PreviewRaw}} {
		args = append(args, "-map", p[0], "-an", "-f", "image2", "-update", "1", "-atomic_writing", "1", "-q:v", "5", "-y", filepath.Join(workDir, p[1]))
	}
	return args
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
