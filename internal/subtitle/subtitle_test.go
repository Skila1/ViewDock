package subtitle

import (
	"testing"

	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/ffmpeg"
)

func TestClassify(t *testing.T) {
	act, reason := classify(ffmpeg.Stream{Codec: "hdmv_pgs_subtitle"}, true)
	if act != "burn" || reason != decision.BurnPGS {
		t.Fatalf("%s %s", act, reason)
	}
	act, reason = classify(ffmpeg.Stream{Codec: "ass"}, true)
	if act != "extract" || reason != decision.TextASSJS {
		t.Fatalf("%s %s", act, reason)
	}
	act, reason = classify(ffmpeg.Stream{Codec: "ass"}, false)
	if act != "burn" || reason != decision.BurnASS {
		t.Fatalf("%s %s", act, reason)
	}
	act, _ = classify(ffmpeg.Stream{Codec: "subrip"}, false)
	if act != "extract" {
		t.Fatal(act)
	}
}

func TestNoASSToVTT(t *testing.T) {
	if ExtFor("ass") != ".ass" {
		t.Fatal("must not convert ASS to VTT")
	}
}
