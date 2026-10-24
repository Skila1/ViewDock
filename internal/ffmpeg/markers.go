package ffmpeg

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
)

// Chapter is one chapter of a media file.
type Chapter struct {
	StartMS int64
	EndMS   int64
	Title   string
}

// Chapters lists a file's chapters, which may be empty.
func (t *Tool) Chapters(ctx context.Context, path string) ([]Chapter, error) {
	cmd := t.command(ctx, t.bin("ffprobe"), "-v", "quiet", "-print_format", "json", "-show_chapters", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe chapters: %w", err)
	}
	return ParseChaptersJSON(out)
}

func ParseChaptersJSON(raw []byte) ([]Chapter, error) {
	var pf struct {
		Chapters []struct {
			StartTime string            `json:"start_time"`
			EndTime   string            `json:"end_time"`
			Tags      map[string]string `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(raw, &pf); err != nil {
		return nil, fmt.Errorf("ffprobe chapters json: %w", err)
	}
	out := make([]Chapter, 0, len(pf.Chapters))
	for _, c := range pf.Chapters {
		start, _ := strconv.ParseFloat(c.StartTime, 64)
		end, _ := strconv.ParseFloat(c.EndTime, 64)
		out = append(out, Chapter{StartMS: int64(start * 1000), EndMS: int64(end * 1000), Title: tag(c.Tags, "title", "TITLE")})
	}
	return out, nil
}

// AudioPCM decodes durSec seconds of the first audio track from startSec as
// mono signed 16-bit samples at rate Hz.
func (t *Tool) AudioPCM(ctx context.Context, path string, startSec, durSec float64, rate int) ([]int16, error) {
	cmd := t.command(ctx, t.bin("ffmpeg"),
		"-v", "error", "-nostdin",
		"-ss", strconv.FormatFloat(startSec, 'f', 3, 64),
		"-i", path,
		"-t", strconv.FormatFloat(durSec, 'f', 3, 64),
		"-map", "0:a:0", "-vn", "-sn", "-dn",
		"-ac", "1", "-ar", strconv.Itoa(rate),
		"-f", "s16le", "-",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg audio: %w", err)
	}
	samples := make([]int16, len(out)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(out[i*2:]))
	}
	return samples, nil
}
