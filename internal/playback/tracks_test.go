package playback

import (
	"testing"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

func TestProfileTrackPicking(t *testing.T) {
	info := &ffmpeg.MediaInfo{Streams: []ffmpeg.Stream{
		{Index: 0, Kind: "video"},
		{Index: 1, Kind: "audio", Language: "jpn"},
		{Index: 2, Kind: "audio", Language: "eng"},
		{Index: 3, Kind: "subtitle", Language: "eng", Forced: true},
		{Index: 4, Kind: "subtitle", Language: "eng"},
	}}

	b := createBody{audioLang: "en", subLang: "en", subMode: "auto"}
	langTracks(info, &b)
	if b.AudioIndex != 2 || b.SubtitleIndex == nil || *b.SubtitleIndex != 3 {
		t.Fatalf("auto: audio %d subtitle %v, want 2 and the forced track 3", b.AudioIndex, b.SubtitleIndex)
	}

	b = createBody{subLang: "eng", subMode: "always"}
	langTracks(info, &b)
	if b.SubtitleIndex == nil || *b.SubtitleIndex != 3 {
		t.Fatalf("always prefers a forced track, then any: %v", b.SubtitleIndex)
	}

	b = createBody{audioLang: "en", subLang: "en", subMode: "always", audioChosen: true, subChosen: true, AudioIndex: 1}
	langTracks(info, &b)
	if b.AudioIndex != 1 || b.SubtitleIndex != nil {
		t.Fatalf("explicit or remembered choices win over languages: %d %v", b.AudioIndex, b.SubtitleIndex)
	}

	missing := 9
	audio, sub := validTracks(info, 7, &missing)
	if audio != 0 || sub != nil {
		t.Fatalf("tracks a file lacks are dropped: %d %v", audio, sub)
	}
}
