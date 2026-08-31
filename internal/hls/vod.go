package hls

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Segment is one advertised VOD media URI. StartMS is movie time, not FFmpeg DTS.
type Segment struct {
	Index      int
	StartMS    int64
	DurationMS int64
}

// Plan is an immutable full-movie timeline. FFmpeg -ss/-start_number must use these values.
type Plan struct {
	TargetSec  int
	DurationMS int64
	Segments   []Segment
}

// EqualLengthPlan splits durationMS into targetSec slices. Last slice is the remainder.
func EqualLengthPlan(durationMS int64, targetSec int) Plan {
	if targetSec <= 0 {
		targetSec = 4
	}
	if durationMS <= 0 {
		return Plan{TargetSec: targetSec}
	}
	step := int64(targetSec) * 1000
	var segs []Segment
	var t int64
	for t < durationMS {
		dur := step
		if t+dur > durationMS {
			dur = durationMS - t
		}
		if dur <= 0 {
			break
		}
		segs = append(segs, Segment{Index: len(segs), StartMS: t, DurationMS: dur})
		t += dur
	}
	return Plan{TargetSec: targetSec, DurationMS: durationMS, Segments: segs}
}

// KeyframePlan advertises the cuts FFmpeg HLS remux actually emits:
// after hls_time from the last cut, the muxer writes the next source keyframe.
// Incomplete lists return an empty plan — callers must promote to transcode,
// never EqualLengthPlan, because copy remux cannot honor a regular grid.
func KeyframePlan(keyframesMS []int64, durationMS int64, maxSegSec int) Plan {
	if maxSegSec <= 0 {
		maxSegSec = 4
	}
	if !KeyframesCover(keyframesMS, durationMS) {
		return Plan{TargetSec: maxSegSec, DurationMS: durationMS}
	}
	pts := normalizeKeyframes(keyframesMS, durationMS)
	if len(pts) < 2 || durationMS <= 0 {
		return Plan{TargetSec: maxSegSec, DurationMS: durationMS}
	}
	targetMS := int64(maxSegSec) * 1000
	var segs []Segment
	var last int64
	for last < durationMS {
		threshold := last + targetMS
		next := durationMS
		for _, kf := range pts {
			if kf > last && kf >= threshold {
				next = kf
				break
			}
		}
		if next <= last {
			break
		}
		segs = append(segs, Segment{Index: len(segs), StartMS: last, DurationMS: next - last})
		last = next
	}
	if len(segs) == 0 {
		return Plan{TargetSec: maxSegSec, DurationMS: durationMS}
	}
	return Plan{TargetSec: maxSegSec, DurationMS: durationMS, Segments: segs}
}

func normalizeKeyframes(raw []int64, durationMS int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, ms := range raw {
		if ms < 0 {
			continue
		}
		if durationMS > 0 && ms > durationMS {
			continue
		}
		if seen[ms] {
			continue
		}
		seen[ms] = true
		out = append(out, ms)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) == 0 || out[0] != 0 {
		out = append([]int64{0}, out...)
	}
	if durationMS > 0 && out[len(out)-1] != durationMS {
		out = append(out, durationMS)
	}
	return out
}

// KeyframesCover reports whether the last keyframe is close enough to EOF to trust the plan.
func KeyframesCover(keyframesMS []int64, durationMS int64) bool {
	if durationMS <= 0 || len(keyframesMS) < 2 {
		return false
	}
	max := int64(0)
	for _, ms := range keyframesMS {
		if ms > max {
			max = ms
		}
	}
	return durationMS-max <= 2000
}

func (p Plan) StartMSForIndex(n int) int64 {
	if n < 0 || n >= len(p.Segments) {
		return 0
	}
	return p.Segments[n].StartMS
}

func (p Plan) IndexForMS(ms int64) int {
	if len(p.Segments) == 0 {
		return 0
	}
	if ms <= 0 {
		return 0
	}
	i := sort.Search(len(p.Segments), func(i int) bool {
		return p.Segments[i].StartMS > ms
	})
	if i == 0 {
		return 0
	}
	return i - 1
}

func (p Plan) LastIndex() int {
	if len(p.Segments) == 0 {
		return 0
	}
	return p.Segments[len(p.Segments)-1].Index
}

func (p Plan) TargetDuration() int {
	max := p.TargetSec
	for _, s := range p.Segments {
		sec := int(math.Ceil(float64(s.DurationMS) / 1000.0))
		if sec > max {
			max = sec
		}
	}
	if max < 1 {
		return 1
	}
	return max
}

func (p Plan) ListedDurationMS() int64 {
	var sum int64
	for _, s := range p.Segments {
		sum += s.DurationMS
	}
	return sum
}

func SegName(n int) string {
	return fmt.Sprintf("seg%d.m4s", n)
}

func ParseSegIndex(name string) (int, bool) {
	base := name
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base = base[:i]
	}
	if !strings.HasPrefix(base, "seg") {
		return 0, false
	}
	rest := strings.TrimPrefix(base, "seg")
	rest = strings.TrimSuffix(rest, ".m4s")
	rest = strings.TrimSuffix(rest, ".ts")
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// WriteVODPlaylist is the immutable media playlist AVKit must see.
func WriteVODPlaylist(p Plan) []byte {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", p.TargetDuration()))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	b.WriteString("#EXT-X-MAP:URI=\"init.mp4\"\n")
	for _, s := range p.Segments {
		sec := float64(s.DurationMS) / 1000.0
		b.WriteString(fmt.Sprintf("#EXTINF:%.3f,\n%s\n", sec, SegName(s.Index)))
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}

// ShouldRestartGen is true when the requested segment is not on the current FFmpeg run.
func ShouldRestartGen(n, genStart, highestOnDisk, lookahead int, running, onDisk bool) bool {
	if onDisk {
		return false
	}
	if !running {
		return true
	}
	if n < genStart {
		return true
	}
	if lookahead < 1 {
		lookahead = 8
	}
	// Just restarted: no files yet. Stay on this run until past genStart+lookahead.
	if highestOnDisk < genStart {
		return n > genStart+lookahead
	}
	if n > highestOnDisk+lookahead {
		return true
	}
	return false
}
