package hls

import (
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
	args := hlsArgs("/cache/s1", 4)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-hls_playlist_type") || !strings.Contains(joined, "event") {
		t.Fatalf("remux must write EVENT: %v", args)
	}
	if strings.Contains(joined, "vod") {
		t.Fatal("remux must not advertise VOD while appending")
	}
}
