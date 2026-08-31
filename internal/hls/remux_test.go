package hls

import (
	"fmt"
	"strings"
	"testing"
)

func TestRemuxInputFlagsStayCopyFriendly(t *testing.T) {
	joined := strings.Join(remuxInputFlags(), " ")
	if !strings.Contains(joined, "+fastseek") || !strings.Contains(joined, "-analyzeduration") {
		t.Fatalf("remux should skip long probes: %v", remuxInputFlags())
	}
	if strings.Contains(joined, "vod") {
		t.Fatal("input flags must not change playlist type")
	}
}

func TestRemuxArgsStayEvent(t *testing.T) {
	args := hlsArgs("/cache/s1", 4, 0, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-hls_playlist_type") || !strings.Contains(joined, "event") {
		t.Fatalf("remux must write EVENT: %v", args)
	}
	if strings.Contains(joined, "vod") {
		t.Fatal("remux must not advertise VOD while appending")
	}
}

func TestRemuxSSIsExactPlanStartNoMagicOffset(t *testing.T) {
	p := KeyframePlan([]int64{0, 4000, 8500, 12000}, 12000, 4)
	n := 1
	ss := p.StartMSForIndex(n)
	if ss != 4000 {
		t.Fatalf("plan ss %d", ss)
	}
	got := ffmpegTS(ss)
	if got != "4.000" {
		t.Fatalf("exact -ss must be 4.000, got %s (no +0.5s)", got)
	}
	if ffmpegTS(ss+500) == got {
		t.Fatal("magic +0.5s would collide with exact ss")
	}
}

func ffmpegTS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%.3f", float64(ms)/1000.0)
}

func TestRemuxStartNumberAlignsWithPlan(t *testing.T) {
	p := EqualLengthPlan(3600_000, 4)
	n := p.IndexForMS(600_000)
	ss := p.StartMSForIndex(n)
	args := hlsArgs("/cache/s1", 4, n, "init.unused.mp4")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, fmt.Sprintf("-start_number %d", n)) && !hasArg(args, "-start_number", fmt.Sprintf("%d", n)) {
		t.Fatalf("want -start_number %d: %v", n, args)
	}
	if ss != int64(n)*4000 {
		t.Fatalf("ss %d != %d*4000", ss, n)
	}
	if !strings.Contains(joined, "init.unused.mp4") {
		t.Fatal("restart must not overwrite init.mp4")
	}
	if strings.Contains(joined, "vod") {
		t.Fatal("ffmpeg packaging stays EVENT")
	}
}

func hasArg(args []string, k, v string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == k && args[i+1] == v {
			return true
		}
	}
	return false
}
