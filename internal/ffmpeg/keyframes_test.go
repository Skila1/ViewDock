package ffmpeg

import "testing"

func TestParseKeyframeCSV(t *testing.T) {
	got := ParseKeyframeCSV("0.000000,K__\n0.042000,___\n2.002000,K__\n")
	if len(got) != 2 || got[0] != 0 || got[1] != 2002 {
		t.Fatalf("%v", got)
	}
}
