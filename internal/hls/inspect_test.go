package hls

import (
	"strings"
	"testing"
)

func TestInspectGrowingEventIsNotMovieDuration(t *testing.T) {
	// 8 × 4s = 32s listed. A 2h movie must not be inferred from this body.
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-MAP:URI=\"init.mp4\"\n")
	for i := 0; i < 8; i++ {
		b.WriteString("#EXTINF:4.000,\n")
		b.WriteString("seg0.m4s\n")
	}
	snap := Inspect([]byte(b.String()))
	if snap.Type != "EVENT" {
		t.Fatalf("type %q", snap.Type)
	}
	if snap.HasEndlist {
		t.Fatal("growing EVENT must not have ENDLIST")
	}
	if snap.SegmentCount != 8 || snap.PlaylistDurationMS != 32_000 {
		t.Fatalf("window %d segs %dms", snap.SegmentCount, snap.PlaylistDurationMS)
	}
	if snap.PlaylistDurationMS == 7_200_000 {
		t.Fatal("playlist must not equal a 2h movie until every EXTINF is listed")
	}
}

func TestInspectCompletedTwoHourVOD(t *testing.T) {
	// Equal 4s segments: 1800 × 4s = 7200s.
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	for i := 0; i < 1800; i++ {
		b.WriteString("#EXTINF:4.000,\n")
		b.WriteString("seg.m4s\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	snap := Inspect([]byte(b.String()))
	if snap.Type != "VOD" || !snap.HasEndlist {
		t.Fatalf("want completed VOD: %+v", snap)
	}
	if snap.SegmentCount != 1800 || snap.PlaylistDurationMS != 7_200_000 {
		t.Fatalf("got %d segs %dms", snap.SegmentCount, snap.PlaylistDurationMS)
	}
}

func TestInspectOmittedTypeIsLiveWindow(t *testing.T) {
	body := []byte("#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXTINF:10,\nseg0.ts\n#EXTINF:10,\nseg1.ts\n#EXTINF:10,\nseg2.ts\n")
	snap := Inspect(body)
	if snap.Type != "LIVE" || snap.HasEndlist {
		t.Fatalf("%+v", snap)
	}
	if snap.PlaylistDurationMS != 30_000 {
		t.Fatalf("live window %d", snap.PlaylistDurationMS)
	}
}

func TestInspectStartTagAfterRewrite(t *testing.T) {
	in := []byte("#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:2.0,\nseg0.ts\n")
	out := WithStartAtZero(in)
	snap := Inspect(out)
	if !snap.HasStart || snap.Type != "EVENT" {
		t.Fatalf("%+v body=%s", snap, out)
	}
}
