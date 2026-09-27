package hls

import (
	"strings"
	"testing"
)

func TestEqualLengthPlanAlignsSSAndStartNumber(t *testing.T) {
	p := EqualLengthPlan(10193_184, 4)
	if len(p.Segments) < 2500 {
		t.Fatalf("seg count %d", len(p.Segments))
	}
	last := p.Segments[len(p.Segments)-1]
	if last.StartMS+last.DurationMS != 10193_184 {
		t.Fatalf("last end %d+%d", last.StartMS, last.DurationMS)
	}
	for i, s := range p.Segments {
		if s.Index != i {
			t.Fatalf("index %d != %d", s.Index, i)
		}
		if p.StartMSForIndex(i) != s.StartMS {
			t.Fatalf("start_number %d ss=%d want %d", i, p.StartMSForIndex(i), s.StartMS)
		}
		if p.IndexForMS(s.StartMS) != i {
			t.Fatalf("ms %d -> %d want %d", s.StartMS, p.IndexForMS(s.StartMS), i)
		}
		if s.DurationMS > 0 && p.IndexForMS(s.StartMS+s.DurationMS-1) != i {
			t.Fatalf("mid-seg %d mapped away from %d", s.StartMS+s.DurationMS-1, i)
		}
	}
	// Seek to 1:00:00 and back to 10s stay on the same plan.
	fwd := p.IndexForMS(3600_000)
	back := p.IndexForMS(10_000)
	if p.StartMSForIndex(fwd) > 3600_000 {
		t.Fatalf("forward ss after target")
	}
	if back != 2 {
		t.Fatalf("10s -> seg %d", back)
	}
	if fwd == back {
		t.Fatal("forward and back must be different indexes")
	}
}

func TestKeyframePlanMatchesFFmpegRemuxCuts(t *testing.T) {
	// FFmpeg copy remux: after hls_time from last cut, emit the next keyframe.
	p := KeyframePlan([]int64{0, 2100, 4000, 8500, 12000}, 12000, 4)
	if len(p.Segments) != 3 {
		t.Fatalf("segs %d %#v", len(p.Segments), p.Segments)
	}
	if p.Segments[0].StartMS != 0 || p.Segments[0].DurationMS != 4000 {
		t.Fatalf("seg0 %+v", p.Segments[0])
	}
	if p.Segments[1].StartMS != 4000 || p.Segments[1].DurationMS != 4500 {
		t.Fatalf("seg1 %+v", p.Segments[1])
	}
	if p.StartMSForIndex(1) != 4000 {
		t.Fatalf("ss for start_number 1 = %d", p.StartMSForIndex(1))
	}
	if p.StartMSForIndex(2) != 8500 {
		t.Fatalf("ss for start_number 2 = %d", p.StartMSForIndex(2))
	}
}

func TestKeyframePlanIncompleteIsEmpty(t *testing.T) {
	p := KeyframePlan([]int64{0, 1000}, 10193_000, 4)
	if len(p.Segments) != 0 {
		t.Fatalf("incomplete probe must not invent a remux plan: %#v", p.Segments)
	}
}

func TestWriteVODPlaylistImmutableTags(t *testing.T) {
	p := EqualLengthPlan(10_500, 4)
	body := string(WriteVODPlaylist(p))
	for _, tag := range []string{
		"#EXT-X-PLAYLIST-TYPE:VOD",
		"#EXT-X-ENDLIST",
		"#EXT-X-INDEPENDENT-SEGMENTS",
		`#EXT-X-MAP:URI="init.mp4"`,
		"seg0.m4s",
		"seg2.m4s",
	} {
		if !strings.Contains(body, tag) {
			t.Fatalf("missing %s\n%s", tag, body)
		}
	}
	if strings.Contains(body, "EVENT") {
		t.Fatal(body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "#EXT-X-ENDLIST") {
		t.Fatalf("ENDLIST must be last: %s", body)
	}
	again := string(WriteVODPlaylist(p))
	if again != body {
		t.Fatal("playlist must be deterministic")
	}
}

func TestParseSegIndex(t *testing.T) {
	n, ok := ParseSegIndex("seg12.m4s")
	if !ok || n != 12 {
		t.Fatalf("%d %v", n, ok)
	}
	if _, ok := ParseSegIndex("init.mp4"); ok {
		t.Fatal("init")
	}
}

func TestShouldRestartGen(t *testing.T) {
	if ShouldRestartGen(5, 0, 4, 8, true, false) {
		t.Fatal("next sequential seg must wait")
	}
	if !ShouldRestartGen(900, 0, 4, 8, true, false) {
		t.Fatal("far seek must restart")
	}
	if !ShouldRestartGen(2, 100, 105, 8, true, false) {
		t.Fatal("seek back off the current run must restart")
	}
	if ShouldRestartGen(2, 100, 105, 8, true, true) {
		t.Fatal("on-disk seek back must not restart")
	}
	if !ShouldRestartGen(10, 0, 4, 8, false, false) {
		t.Fatal("dead job must restart")
	}
	if ShouldRestartGen(900, 900, -1, 8, true, false) {
		t.Fatal("fresh restart at 900 must wait, not start a second job")
	}
	if !ShouldRestartGen(920, 900, -1, 8, true, false) {
		t.Fatal("far past a fresh restart must restart")
	}
}

func TestKeyframesCover(t *testing.T) {
	if KeyframesCover([]int64{0, 1000}, 10193_000) {
		t.Fatal("partial probe")
	}
	if !KeyframesCover([]int64{0, 4000, 10192_000}, 10193_000) {
		t.Fatal("near EOF")
	}
}
