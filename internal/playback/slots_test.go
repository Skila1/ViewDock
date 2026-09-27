package playback

import (
	"testing"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/bandwidth"
	"github.com/viewdock/viewdock/internal/capability"
	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/ffmpeg"
)

func heldSession(id, user, item string) *Session {
	s := &Session{
		ID: id, Kind: "user", UserID: user, ItemKind: "movie", ItemID: item,
		SlotHeld: true, Mode: decision.ModeTranscodeAV,
	}
	return s
}

func TestOneVideoEncodeOccupiesOneSlot(t *testing.T) {
	api := testAPI(t, &mockLocator{}, nil)
	if err := api.Lim.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	s := heldSession("s1", "u1", "m1")
	api.Reg.Put(s)
	if api.Lim.Active() != 1 {
		t.Fatalf("active %d", api.Lim.Active())
	}
}

func TestSeekSupersedesAndReleasesSlot(t *testing.T) {
	api := testAPI(t, &mockLocator{}, nil)
	_ = api.Lim.TryAcquire()
	api.Reg.Put(heldSession("old", "u1", "m1"))
	p := &auth.Principal{Kind: auth.KindUser, UserID: "u1"}
	api.supersedePlayback(p, "movie", "m1", "old")
	if api.Lim.Active() != 0 {
		t.Fatalf("slot not released: %d", api.Lim.Active())
	}
	if api.Reg.Get("old") != nil {
		t.Fatal("old session still registered")
	}
	if err := api.Lim.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	api.Reg.Put(heldSession("new", "u1", "m1"))
	if api.Lim.Active() != 1 || len(api.Reg.List()) != 1 {
		t.Fatalf("active=%d sessions=%d", api.Lim.Active(), len(api.Reg.List()))
	}
}

func TestRapidSeeksDoNotExhaustPool(t *testing.T) {
	api := testAPI(t, &mockLocator{}, nil)
	p := &auth.Principal{Kind: auth.KindUser, UserID: "u1"}
	positions := []int64{5 * 60_000, 20 * 60_000, 45 * 60_000, 60 * 60_000}
	for i, ms := range positions {
		api.supersedePlayback(p, "movie", "m1", "")
		if err := api.Lim.TryAcquire(); err != nil {
			t.Fatalf("seek %d at %d: %v active=%d", i, ms, err, api.Lim.Active())
		}
		s := heldSession("s"+string(rune('a'+i)), "u1", "m1")
		s.StartMS = ms
		api.Reg.Put(s)
	}
	if api.Lim.Active() != 1 {
		t.Fatalf("want 1 active encoder, got %d", api.Lim.Active())
	}
	if n := len(api.Reg.List()); n != 1 {
		t.Fatalf("want 1 session, got %d", n)
	}
}

func TestKillReleasesSlot(t *testing.T) {
	api := testAPI(t, &mockLocator{}, nil)
	_ = api.Lim.TryAcquire()
	s := heldSession("x", "u1", "m1")
	api.Reg.Put(s)
	api.Reg.Delete("x")
	api.kill(s)
	if api.Lim.Active() != 0 {
		t.Fatal("kill must release")
	}
	api.kill(s)
	if api.Lim.Active() != 0 {
		t.Fatal("double kill must not underflow then fail later")
	}
}

func TestRemuxAndAudioOnlyDoNotConsumeVideoSlot(t *testing.T) {
	remux := decision.Decide(decision.Input{
		Info: &ffmpeg.MediaInfo{VideoCodec: "h264", AudioCodec: "aac", Container: "mkv", Width: 1920, Height: 1080,
			Streams: []ffmpeg.Stream{{Kind: "video", Codec: "h264"}, {Kind: "audio", Codec: "aac"}}},
		Quality: "auto", LAN: true,
	})
	audioOnly := decision.Decide(decision.Input{
		Info: &ffmpeg.MediaInfo{VideoCodec: "hevc", AudioCodec: "eac3", Container: "mkv", Width: 1920, Height: 1080, BitDepth: 10,
			Streams: []ffmpeg.Stream{{Kind: "video", Codec: "hevc", BitDepth: 10, Width: 1920, Height: 1080}, {Kind: "audio", Codec: "eac3"}}},
		Client: capability.Profile{
			HEVCMain10: capability.Ptr(true),
			DecodingInfo: map[string]any{"hevc_main10": map[string]any{"supported": true}},
		},
		Quality: "auto", LAN: true,
	})
	if decision.NeedsVideoSlot(remux) || decision.NeedsVideoSlot(audioOnly) {
		t.Fatalf("remux slot=%v audio-only slot=%v", decision.NeedsVideoSlot(remux), decision.NeedsVideoSlot(audioOnly))
	}
}

func TestTwoUsersUseSeparateSlots(t *testing.T) {
	api := testAPI(t, &mockLocator{}, nil)
	_ = api.Lim.TryAcquire()
	_ = api.Lim.TryAcquire()
	api.Reg.Put(heldSession("a", "u1", "m1"))
	api.Reg.Put(heldSession("b", "u2", "m2"))
	if api.Lim.Active() != 2 {
		t.Fatalf("active %d", api.Lim.Active())
	}
	if err := api.Lim.TryAcquire(); err != bandwidth.ErrLoad {
		t.Fatalf("want 429 got %v", err)
	}
	p := &auth.Principal{Kind: auth.KindUser, UserID: "u1"}
	api.supersedePlayback(p, "movie", "m1", "")
	if api.Lim.Active() != 1 {
		t.Fatalf("other user slot must remain: %d", api.Lim.Active())
	}
	if api.Reg.Get("b") == nil {
		t.Fatal("user 2 session must survive user 1 supersede")
	}
}

func Test429OnlyWhenVideoEncodesFull(t *testing.T) {
	api := testAPI(t, &mockLocator{}, nil)
	if err := api.Lim.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	if err := api.Lim.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	if err := api.Lim.TryAcquire(); err != bandwidth.ErrLoad {
		t.Fatalf("want LOAD_429 got %v", err)
	}
}
