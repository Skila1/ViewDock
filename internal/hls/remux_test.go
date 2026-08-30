package hls

import (
	"strings"
	"testing"
)

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
