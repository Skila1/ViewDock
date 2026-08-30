package hls

import (
	"bytes"
	"strconv"
	"strings"
)

// Snapshot is what a client can legally infer from one media playlist body.
// PlaylistDurationMS is only the sum of listed EXTINF, never the movie runtime.
type Snapshot struct {
	Type               string // EVENT, VOD, or LIVE when PLAYLIST-TYPE is omitted
	HasEndlist         bool
	HasStart           bool
	Version            int
	TargetDuration     int
	MediaSequence      int
	SegmentCount       int
	PlaylistDurationMS int64
}

// Inspect parses HLS media-playlist tags. It does not invent a planned movie duration.
func Inspect(body []byte) Snapshot {
	s := Snapshot{Type: "LIVE"}
	for _, raw := range bytes.Split(body, []byte("\n")) {
		line := strings.TrimRight(string(raw), "\r")
		switch {
		case strings.HasPrefix(line, "#EXT-X-PLAYLIST-TYPE:"):
			s.Type = strings.TrimSpace(strings.TrimPrefix(line, "#EXT-X-PLAYLIST-TYPE:"))
		case line == "#EXT-X-ENDLIST":
			s.HasEndlist = true
		case strings.HasPrefix(line, "#EXT-X-START:"):
			s.HasStart = true
		case strings.HasPrefix(line, "#EXT-X-VERSION:"):
			s.Version = atoi(strings.TrimPrefix(line, "#EXT-X-VERSION:"))
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			s.TargetDuration = atoi(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"))
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			s.MediaSequence = atoi(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"))
		case strings.HasPrefix(line, "#EXTINF:"):
			s.SegmentCount++
			s.PlaylistDurationMS += extinfMS(line)
		}
	}
	return s
}

func extinfMS(line string) int64 {
	rest := strings.TrimPrefix(line, "#EXTINF:")
	if i := strings.IndexByte(rest, ','); i >= 0 {
		rest = rest[:i]
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
	if err != nil {
		return 0
	}
	return int64(sec * 1000)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
