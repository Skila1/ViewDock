package markers

import (
	"regexp"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

// Markers of one episode, in milliseconds from its start. A zero source
// means not found.
type Markers struct {
	RecapStartMS, RecapEndMS int64
	RecapSource              string
	IntroStartMS, IntroEndMS int64
	IntroSource              string
	CreditsStartMS           int64
	CreditsSource            string
}

func (m Markers) HasRecap() bool   { return m.RecapSource != "" }
func (m Markers) HasIntro() bool   { return m.IntroSource != "" }
func (m Markers) HasCredits() bool { return m.CreditsSource != "" }

const (
	SourceChapters    = "chapters"
	SourceFingerprint = "fingerprint"

	minIntroMS   = 12_000
	maxIntroMS   = 150_000
	minCreditsMS = 15_000

	// A recap is a run of clips from earlier episodes, each at least
	// minClipMS, no more than recapGapMS apart, starting within
	// recapStartMS of where the search begins and adding up to minRecapMS.
	minClipMS    = 2500
	recapGapMS   = 20_000
	recapStartMS = 60_000
	minRecapMS   = 8000
	// A "Previously on" line before the first clip belongs to the recap.
	recapLeadMS = 8000
	recapTailMS = 3000
)

var (
	recapTitle   = regexp.MustCompile(`(?i)\b(recap|previously)\b`)
	introTitle   = regexp.MustCompile(`(?i)\b(intro|opening|op|title sequence|theme song)\b`)
	creditsTitle = regexp.MustCompile(`(?i)\b(credits|ending|ed|outro|end titles)\b`)
)

// FromChapters reads markers from chapter titles such as "Recap" or
// "Previously", "Opening" or "OP", and "Ending", "ED" or "Credits".
func FromChapters(chapters []ffmpeg.Chapter, durationMS int64) Markers {
	var m Markers
	for _, c := range chapters {
		length := c.EndMS - c.StartMS
		switch {
		case recapTitle.MatchString(c.Title):
			if !m.HasRecap() && c.StartMS < durationMS*2/5 && length >= 5000 && length <= 300_000 {
				m.RecapStartMS, m.RecapEndMS, m.RecapSource = c.StartMS, c.EndMS, SourceChapters
			}
		case introTitle.MatchString(c.Title):
			if !m.HasIntro() && c.StartMS < durationMS*2/5 && length >= minIntroMS/2 && length <= 240_000 {
				m.IntroStartMS, m.IntroEndMS, m.IntroSource = c.StartMS, c.EndMS, SourceChapters
			}
		case creditsTitle.MatchString(c.Title):
			if !m.HasCredits() && c.StartMS >= durationMS/2 && c.StartMS < durationMS {
				m.CreditsStartMS, m.CreditsSource = c.StartMS, SourceChapters
			}
		}
	}
	return m
}

// IntroWindow is the part of an episode searched for its intro, in seconds.
func IntroWindow(durationMS int64) (start, length float64) {
	return 0, min(600, 0.3*float64(durationMS)/1000)
}

// CreditsWindow is the part of an episode searched for its credits, in seconds.
func CreditsWindow(durationMS int64) (start, length float64) {
	d := float64(durationMS) / 1000
	length = min(360, 0.3*d)
	return d - length, length
}

func pointMS(windowStartSec float64, point int) int64 {
	return int64(windowStartSec*1000 + float64(point)*FrameMS)
}

// IntroFrom finds the intro an episode shares with another, from the
// fingerprints of both episodes' intro windows.
func IntroFrom(a, b Print) (startMS, endMS int64, ok bool) {
	seg, ok := Longest(a, b, points(minIntroMS))
	if !ok {
		return 0, 0, false
	}
	startMS, endMS = pointMS(0, seg.AStart), pointMS(0, seg.AEnd)
	if endMS-startMS > maxIntroMS {
		return 0, 0, false
	}
	// A shared opening that starts within a couple of seconds starts the episode.
	if startMS < 2000 {
		startMS = 0
	}
	return startMS, endMS, true
}

// CreditsFrom finds where the credits start from the fingerprints of two
// episodes' credits windows. The first window starts at aWindowStartMS of an
// episode aDurationMS long.
func CreditsFrom(a, b Print, aWindowStartMS, aDurationMS int64) (startMS int64, ok bool) {
	seg, ok := Longest(a, b, points(minCreditsMS))
	if !ok {
		return 0, false
	}
	// Nearly the whole window matching means the same recording twice, not
	// shared credits.
	if float64(seg.Len()) >= 0.95*float64(len(a.Points)) {
		return 0, false
	}
	startMS = pointMS(float64(aWindowStartMS)/1000, seg.AStart)
	if startMS < aDurationMS/2 {
		return 0, false
	}
	return startMS, true
}

// RecapFrom finds the recap among clips an episode shares with earlier
// episodes. The clips are points into the part of the episode searched,
// from windowStartMS to windowEndMS.
func RecapFrom(clips []Segment, windowStartMS, windowEndMS int64) (startMS, endMS int64, ok bool) {
	if len(clips) == 0 || clips[0].AStart > points(recapStartMS) {
		return 0, 0, false
	}
	first, last, total := clips[0], clips[0], 0
	for _, c := range clips {
		if c.AStart-last.AEnd > points(recapGapMS) {
			break
		}
		total += c.Len()
		last = c
	}
	if total < points(minRecapMS) {
		return 0, 0, false
	}
	startMS, endMS = pointMS(float64(windowStartMS)/1000, first.AStart), pointMS(float64(windowStartMS)/1000, last.AEnd)
	if startMS-windowStartMS <= recapLeadMS {
		startMS = windowStartMS
	}
	// A recap that runs into what ends the search, the intro, ends there.
	if windowEndMS-endMS <= recapTailMS {
		endMS = windowEndMS
	}
	return startMS, endMS, true
}

func points(ms int64) int { return int(float64(ms) / FrameMS) }
