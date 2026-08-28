package subtitle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/ffmpeg"
)

const MaxFontBytes = 32 << 20 // 32 MiB cap for extracted MKV fonts

type Result struct {
	Action   string // extract|burn|none
	Path     string
	Codec    string
	Ext      string
	FontDir  string
	FontFiles []string
}

func Action(info *ffmpeg.MediaInfo, subtitleIndex *int, assJS bool) (action, reason string, stream *ffmpeg.Stream) {
	if subtitleIndex == nil || info == nil {
		return "none", "", nil
	}
	for i := range info.Streams {
		s := &info.Streams[i]
		if s.Kind == "subtitle" && s.Index == *subtitleIndex {
			act, rs := classify(*s, assJS)
			return act, rs, s
		}
	}
	return "none", "", nil
}

func classify(s ffmpeg.Stream, assJS bool) (string, string) {
	c := strings.ToLower(s.Codec)
	switch {
	case c == "hdmv_pgs_subtitle" || c == "pgs" || c == "pgssub":
		return "burn", decision.BurnPGS
	case c == "dvd_subtitle" || c == "dvdsub" || c == "vobsub":
		return "burn", decision.BurnVobSub
	case c == "ass" || c == "ssa":
		if assJS {
			return "extract", decision.TextASSJS
		}
		return "burn", decision.BurnASS
	default:
		return "extract", decision.TextSub
	}
}

func ExtFor(codec string) string {
	switch strings.ToLower(codec) {
	case "ass", "ssa":
		return ".ass"
	case "subrip", "srt":
		return ".srt"
	case "webvtt", "vtt":
		return ".vtt"
	default:
		return ".ass"
	}
}

type Extractor struct {
	FF *ffmpeg.Tool
}

func New(ff *ffmpeg.Tool) *Extractor { return &Extractor{FF: ff} }

func (e *Extractor) Extract(ctx context.Context, src, destDir string, info *ffmpeg.MediaInfo, index int) (Result, error) {
	var st *ffmpeg.Stream
	for i := range info.Streams {
		if info.Streams[i].Kind == "subtitle" && info.Streams[i].Index == index {
			st = &info.Streams[i]
			break
		}
	}
	if st == nil {
		return Result{Action: "none"}, fmt.Errorf("subtitle index %d not found", index)
	}
	ext := ExtFor(st.Codec)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return Result{}, err
	}
	out := filepath.Join(destDir, "subtitle"+ext)
	ff := e.FF
	if ff == nil {
		ff = ffmpeg.New()
	}
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", src,
		"-map", fmt.Sprintf("0:%d", st.Index),
		"-c", "copy",
		"-y", out,
	}
	cmd := execCmd(ctx, ff, args)
	if b, err := cmd.CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("extract sub: %w (%s)", err, string(b))
	}
	return Result{Action: "extract", Path: out, Codec: st.Codec, Ext: ext}, nil
}

func (e *Extractor) ExtractFonts(ctx context.Context, src, destDir string, info *ffmpeg.MediaInfo) (Result, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return Result{}, err
	}
	ff := e.FF
	if ff == nil {
		ff = ffmpeg.New()
	}
	var files []string
	var used int64
	n := 0
	for _, s := range info.Streams {
		if s.Kind != "attachment" {
			continue
		}
		if used >= MaxFontBytes {
			break
		}
		name := s.Title
		if name == "" {
			name = fmt.Sprintf("font_%d", s.Index)
		}
		name = filepath.Base(name)
		dest := filepath.Join(destDir, name)
		args := []string{
			"-hide_banner", "-loglevel", "error",
			"-dump_attachment:t:" + fmt.Sprint(n), dest,
			"-i", src,
			"-y",
		}
		n++
		cmd := execCmd(ctx, ff, args)
		_ = cmd.Run()
		if st, err := os.Stat(dest); err == nil {
			used += st.Size()
			if used > MaxFontBytes {
				_ = os.Remove(dest)
				break
			}
			files = append(files, dest)
		}
	}
	return Result{FontDir: destDir, FontFiles: files}, nil
}

func MIME(ext string) string {
	switch strings.ToLower(ext) {
	case ".ass", ".ssa":
		return "text/x-ssa"
	case ".srt":
		return "application/x-subrip"
	case ".vtt":
		return "text/vtt"
	default:
		return "text/plain"
	}
}
