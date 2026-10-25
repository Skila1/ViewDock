// Package markers finds where an episode's intro plays and where its end
// credits start, from chapter titles or by comparing its audio with other
// episodes of the same series.
package markers

import (
	"math"
	"math/bits"
	"math/cmplx"
	"slices"
	"sort"
)

// Audio is fingerprinted at SampleRate in frames of frameLen samples taken
// every hop samples (32 ms), so episodes whose audio is offset by a fraction
// of a frame still produce similar fingerprints.
const (
	SampleRate = 8000
	frameLen   = 2048
	hop        = 256
	bands      = 33
	minHz      = 300
	maxHz      = 2000
)

// FrameMS is the time between fingerprint points.
const FrameMS = float64(hop) * 1000 / SampleRate

// Print is an audio fingerprint: one 32-bit point per frame, with silent
// frames marked so silence never counts as shared audio.
type Print struct {
	Points []uint32
	Silent []bool
}

// Fingerprint computes the fingerprint of mono samples at SampleRate. Each
// bit says whether the energy difference between two neighbouring frequency
// bands grew or shrank since the previous frame.
func Fingerprint(samples []int16) Print {
	if len(samples) < frameLen+hop {
		return Print{}
	}
	window := make([]float64, frameLen)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(frameLen-1))
	}
	edges := bandEdges()
	buf := make([]complex128, frameLen)
	frames := (len(samples)-frameLen)/hop + 1
	energy := make([][bands]float64, frames)
	loud := make([]bool, frames)
	for f := 0; f < frames; f++ {
		off := f * hop
		var sumSq float64
		for i := 0; i < frameLen; i++ {
			v := float64(samples[off+i])
			sumSq += v * v
			buf[i] = complex(v*window[i], 0)
		}
		// Below about -60 dBFS RMS is treated as silence.
		loud[f] = math.Sqrt(sumSq/frameLen) > 32
		fft(buf)
		for b := 0; b < bands; b++ {
			var e float64
			for k := edges[b]; k < edges[b+1]; k++ {
				m := cmplx.Abs(buf[k])
				e += m * m
			}
			energy[f][b] = e
		}
	}
	p := Print{Points: make([]uint32, frames-1), Silent: make([]bool, frames-1)}
	for f := 1; f < frames; f++ {
		var v uint32
		for b := 0; b < bands-1; b++ {
			d := (energy[f][b] - energy[f][b+1]) - (energy[f-1][b] - energy[f-1][b+1])
			if d > 0 {
				v |= 1 << b
			}
		}
		p.Points[f-1] = v
		p.Silent[f-1] = !loud[f] || !loud[f-1]
	}
	return p
}

// bandEdges returns FFT bin boundaries of bands logarithmically spaced
// between minHz and maxHz.
func bandEdges() [bands + 1]int {
	var e [bands + 1]int
	binHz := float64(SampleRate) / frameLen
	for i := 0; i <= bands; i++ {
		hz := minHz * math.Pow(maxHz/minHz, float64(i)/bands)
		e[i] = int(math.Round(hz / binHz))
		if i > 0 && e[i] <= e[i-1] {
			e[i] = e[i-1] + 1
		}
	}
	return e
}

// fft is an in-place iterative radix-2 FFT; len(a) must be a power of two.
func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wk := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u, v := a[start+k], a[start+k+size/2]*wk
				a[start+k], a[start+k+size/2] = u+v, u-v
				wk *= w
			}
		}
	}
}

// Segment is a stretch of audio two fingerprints share, in points.
type Segment struct {
	AStart, AEnd int // [AStart, AEnd) in the first print
	BStart, BEnd int
}

func (s Segment) Len() int { return s.AEnd - s.AStart }

const (
	// maxBitDiff is how many of 32 bits two points may differ by and still
	// match. Unrelated audio differs by 16 on average.
	maxBitDiff = 10
	// maxGapPoints is the longest run of mismatches inside a shared stretch
	// (about 3.5 s), for example a sound effect over the theme.
	maxGapPoints = 110
	// A matching point counts as shared audio when at least minDensity of
	// the points within smoothRadius of it (about half a second) match.
	minDensity   = 0.4
	smoothRadius = 16
	// edgeShare of the match rate in the middle of a stretch marks its edges.
	edgeShare = 0.75
	// Alignments are scored on every stride-th point over windows of
	// scoreWindowMS; the best candidates are searched in full.
	stride        = 3
	scoreWindowMS = 8000
	candidates    = 6
	// Recap clips: offsets are scored over clipWindowMS and many of them
	// are searched. A cut to another scene changes the offset, so a clip
	// only ends at a gap longer than clipGapPoints (about 2 s).
	clipWindowMS   = 4000
	clipCandidates = 24
	clipGapPoints  = 64
)

// Longest finds the longest stretch of audio a and b share, at least
// minPoints long.
func Longest(a, b Print, minPoints int) (Segment, bool) {
	var out Segment
	found := false
	for _, off := range alignments(a, b, minPoints, scoreWindowMS, 0.25, candidates) {
		for _, seg := range runsAt(a, b, off, minPoints, maxGapPoints) {
			if !found || seg.Len() > out.Len() {
				out, found = seg, true
			}
		}
	}
	return out, found
}

// Clips finds every separate clip of at least minPoints that a shares with
// b, such as the scenes of a recap taken from an earlier episode. Clips
// never overlap in a.
func Clips(a, b Print, minPoints int) []Segment {
	var all []Segment
	for _, off := range alignments(a, b, minPoints, clipWindowMS, 0.4, clipCandidates) {
		all = append(all, runsAt(a, b, off, minPoints, clipGapPoints)...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Len() > all[j].Len() })
	var out []Segment
	for _, c := range all {
		overlaps := false
		for _, o := range out {
			if c.AStart < o.AEnd && o.AStart < c.AEnd {
				overlaps = true
				break
			}
		}
		if !overlaps {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AStart < out[j].AStart })
	return out
}

// alignments returns the best few offsets of b against a (b index minus a
// index). Noise between two encodes leaves few points exactly equal, so
// every offset is scored by its best window of near matches, sampling every
// stride-th point; offsets whose best window matches less than minShare are
// dropped.
func alignments(a, b Print, minPoints int, windowMS int64, minShare float64, limit int) []int {
	if len(a.Points) < minPoints || len(b.Points) < minPoints {
		return nil
	}
	win := max(1, points(windowMS)/stride)
	type cand struct{ off, score int }
	var cs []cand
	ring := make([]bool, win)
	for off := -(len(a.Points) - minPoints); off <= len(b.Points)-minPoints; off++ {
		lo := max(0, -off)
		hi := min(len(a.Points), len(b.Points)-off)
		count, best, k := 0, 0, 0
		clear(ring)
		for i := lo; i < hi; i += stride {
			j := i + off
			m := !a.Silent[i] && !b.Silent[j] && bits.OnesCount32(a.Points[i]^b.Points[j]) <= maxBitDiff
			if ring[k] {
				count--
			}
			ring[k] = m
			if m {
				count++
			}
			k = (k + 1) % win
			best = max(best, count)
		}
		// Unrelated audio matches about one point in forty.
		if float64(best) >= minShare*float64(win) {
			cs = append(cs, cand{off, best})
		}
	}
	var out []int
	for picks := 0; picks < limit && len(cs) > 0; picks++ {
		bi := 0
		for i, c := range cs {
			if c.score > cs[bi].score {
				bi = i
			}
		}
		pick := cs[bi].off
		for off := pick - stride; off <= pick+stride; off++ {
			out = append(out, off)
		}
		// Neighbouring alignments are the same match.
		kept := cs[:0]
		for _, c := range cs {
			if c.off < pick-2*stride || c.off > pick+2*stride {
				kept = append(kept, c)
			}
		}
		cs = kept
	}
	return out
}

// runsAt finds the shared runs of at least minPoints with b shifted by off,
// bridging gaps of up to maxGap points. A point is shared when enough points
// around it match, so the odd chance match in unrelated audio never links
// two stretches.
func runsAt(a, b Print, off, minPoints, maxGap int) []Segment {
	lo := max(0, -off)
	hi := min(len(a.Points), len(b.Points)-off)
	if hi-lo < minPoints {
		return nil
	}
	n := hi - lo
	sum := make([]int, n+1)
	for k := 0; k < n; k++ {
		i := lo + k
		j := i + off
		sum[k+1] = sum[k]
		if !a.Silent[i] && !b.Silent[j] && bits.OnesCount32(a.Points[i]^b.Points[j]) <= maxBitDiff {
			sum[k+1]++
		}
	}
	density := func(k int) float64 {
		l, r := max(0, k-smoothRadius), min(n, k+smoothRadius+1)
		return float64(sum[r]-sum[l]) / float64(r-l)
	}
	shared := func(k int) bool { return sum[k+1]-sum[k] == 1 && density(k) >= minDensity }
	var out []Segment
	start, last := -1, -1
	flush := func() {
		if start < 0 {
			return
		}
		// Audio around a shared stretch can match now and then; its edges are
		// where matching falls well below the rate in the stretch's middle.
		s, e := start, last
		q := (e - s) / 4
		core := float64(sum[e-q+1]-sum[s+q]) / float64(max(1, e-q+1-s-q))
		thr := max(minDensity, edgeShare*core)
		for s < e && !(sum[s+1]-sum[s] == 1 && density(s) >= thr) {
			s++
		}
		for e > s && !(sum[e+1]-sum[e] == 1 && density(e) >= thr) {
			e--
		}
		if e-s+1 >= minPoints {
			out = append(out, Segment{AStart: lo + s, AEnd: lo + e + 1, BStart: lo + s + off, BEnd: lo + e + 1 + off})
		}
	}
	for k := 0; k < n; k++ {
		if !shared(k) {
			continue
		}
		if start >= 0 && k-last > maxGap {
			flush()
			start = -1
		}
		if start < 0 {
			start = k
		}
		last = k
	}
	flush()
	return out
}

// Mask returns p with the points in [from, to) treated as silence, so they
// never match.
func (p Print) Mask(from, to int) Print {
	from, to = max(0, from), min(len(p.Points), to)
	if from >= to {
		return p
	}
	silent := slices.Clone(p.Silent)
	for i := from; i < to; i++ {
		silent[i] = true
	}
	return Print{Points: p.Points, Silent: silent}
}

// Slice returns the points in [from, to).
func (p Print) Slice(from, to int) Print {
	from, to = max(0, min(from, len(p.Points))), max(0, min(to, len(p.Points)))
	if from >= to {
		return Print{}
	}
	return Print{Points: p.Points[from:to], Silent: p.Silent[from:to]}
}
