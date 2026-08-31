package playback

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/hls"
)

type stubKF struct{ pts []int64 }

func (s stubKF) Keyframes(context.Context, string) ([]int64, error) { return s.pts, nil }

func vodSession(t *testing.T, api *API, durationMS int64) *Session {
	t.Helper()
	dir := t.TempDir()
	plan := hls.EqualLengthPlan(durationMS, 4)
	if err := os.WriteFile(filepath.Join(dir, vodFile), hls.WriteVODPlaylist(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Session{
		ID: "vod1", Kind: "user", UserID: "u1",
		Delivery: "hls", HLSAttach: "native", Dir: dir,
		DurationMS: durationMS, VOD: true, VODPlan: plan,
		Stoken: "tok", StokenExp: time.Now().Add(time.Hour),
		Created: time.Now(), LastPing: time.Now(),
	}
	api.Reg.Put(s)
	t.Cleanup(func() { api.Reg.Delete(s.ID) })
	return s
}

func vodHLS(api *API) http.Handler {
	r := chi.NewRouter()
	api.HLSRoutes(r)
	return r
}

func TestVODPlaylistIsImmutableFullTimeline(t *testing.T) {
	api := testAPI(t, nil, nil)
	s := vodSession(t, api, 10193_184)
	h := vodHLS(api)
	_ = os.WriteFile(filepath.Join(s.Dir, "init.mp4"), []byte("init"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Dir, "seg0.m4s"), []byte("aaaa"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Dir, "seg1.m4s"), []byte("bbbb"), 0o644)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/"+s.ID+"/index.m3u8?stoken=tok", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(body, "#EXT-X-ENDLIST") {
		t.Fatalf("not VOD: %s", body[:min(400, len(body))])
	}
	if rec.Header().Get("X-VD-Playlist-Type") != "VOD" {
		t.Fatalf("type %s", rec.Header().Get("X-VD-Playlist-Type"))
	}
	if !strings.Contains(body, "stoken=tok") {
		t.Fatal("stoken")
	}
	if strings.Count(body, ".m4s") < 2500 {
		t.Fatalf("expected full movie segments, got %d", strings.Count(body, ".m4s"))
	}
}

func TestVODSeekForwardAndBackSameSession(t *testing.T) {
	api := testAPI(t, nil, nil)
	s := vodSession(t, api, 600_000)
	h := vodHLS(api)
	_ = os.WriteFile(filepath.Join(s.Dir, "init.mp4"), []byte("init"), 0o644)
	fwd := s.VODPlan.IndexForMS(120_000)
	back := s.VODPlan.IndexForMS(8_000)
	if fwd == back {
		t.Fatal("need distinct indexes")
	}
	_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(fwd)), []byte("FWD"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(fwd+1)), []byte("N"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(back)), []byte("BACK"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(back+1)), []byte("N"), 0o644)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/"+s.ID+"/"+hls.SegName(fwd)+"?stoken=tok", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("fwd %d %s", rec.Code, rec.Body.String())
	}
	b, _ := io.ReadAll(rec.Body)
	if string(b) != "FWD" {
		t.Fatalf("fwd body %q", b)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/"+s.ID+"/"+hls.SegName(back)+"?stoken=tok", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("back %d", rec.Code)
	}
	b, _ = io.ReadAll(rec.Body)
	if string(b) != "BACK" {
		t.Fatalf("back body %q", b)
	}
	if api.Reg.Get(s.ID) == nil || api.Reg.Get(s.ID) != s {
		t.Fatal("must keep one session")
	}
	if s.VODPlan.StartMSForIndex(fwd) != int64(fwd)*4000 {
		t.Fatalf("alignment start_number %d ss=%d", fwd, s.VODPlan.StartMSForIndex(fwd))
	}
}

func TestVODSegmentHoldUntilReady(t *testing.T) {
	api := testAPI(t, nil, nil)
	s := vodSession(t, api, 60_000)
	h := vodHLS(api)
	_ = os.WriteFile(filepath.Join(s.Dir, "init.mp4"), []byte("init"), 0o644)
	n := 3
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(n)), []byte("SEG"), 0o644)
		_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(n+1)), []byte("NEXT"), 0o644)
	}()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/"+s.ID+"/"+hls.SegName(n)+"?stoken=tok", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("hold %d %s", rec.Code, rec.Body.String())
	}
	b, _ := io.ReadAll(rec.Body)
	if string(b) != "SEG" {
		t.Fatalf("body %q", b)
	}
}

func TestUseVODOnDemandIsNativeOnly(t *testing.T) {
	if useVODOnDemand(&Session{Delivery: "hls", HLSAttach: "mse"}) {
		t.Fatal("desktop/MMS must stay EVENT")
	}
	if useVODOnDemand(&Session{Delivery: "direct", HLSAttach: "native"}) {
		t.Fatal("direct play")
	}
	if !useVODOnDemand(&Session{Delivery: "hls", HLSAttach: "native"}) {
		t.Fatal("iOS native")
	}
}

func TestPrepareVODPromotesIncompleteKeyframes(t *testing.T) {
	api := testAPI(t, nil, nil)
	api.KF = stubKF{pts: []int64{0, 1000}}
	dir := t.TempDir()
	s := &Session{
		ID: "p1", Delivery: "hls", HLSAttach: "native", Dir: dir,
		DurationMS: 10193_000, AbsPath: "/media/scarface.mkv",
		Decision: decision.Result{Mode: decision.ModeRemux, Delivery: decision.DeliveryHLS, CopyVideo: true},
		Mode:     decision.ModeRemux,
	}
	if err := api.prepareVOD(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !s.Decision.NeedVideoXcode || s.VODPlanKind != planKindPromoted {
		t.Fatalf("want promote, kind=%s need=%v", s.VODPlanKind, s.Decision.NeedVideoXcode)
	}
	if len(s.VODPlan.Segments) == 0 {
		t.Fatal("equal-length transcode plan")
	}
	if s.SeekableFromMS != 0 {
		t.Fatalf("origin %d", s.SeekableFromMS)
	}
}

func TestPrepareVODKeyframePlanWhenCovered(t *testing.T) {
	api := testAPI(t, nil, nil)
	api.KF = stubKF{pts: []int64{0, 4000, 8500, 12000}}
	dir := t.TempDir()
	s := &Session{
		ID: "p2", Delivery: "hls", HLSAttach: "native", Dir: dir,
		DurationMS: 12000, AbsPath: "/m.mkv",
		Decision: decision.Result{Mode: decision.ModeRemux, Delivery: decision.DeliveryHLS, CopyVideo: true},
		Mode:     decision.ModeRemux,
	}
	if err := api.prepareVOD(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.VODPlanKind != planKindKeyframe || s.Decision.NeedVideoXcode {
		t.Fatalf("kind=%s xcode=%v", s.VODPlanKind, s.Decision.NeedVideoXcode)
	}
	if s.VODPlan.StartMSForIndex(1) != 4000 {
		t.Fatalf("ss %d", s.VODPlan.StartMSForIndex(1))
	}
}

func TestNativePlaylistNeverServesEVENT(t *testing.T) {
	api := testAPI(t, nil, nil)
	s := vodSession(t, api, 60_000)
	_ = os.WriteFile(filepath.Join(s.Dir, "index.m3u8"), []byte("#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Dir, "init.mp4"), []byte("init"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Dir, "seg0.m4s"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Dir, "seg1.m4s"), []byte("b"), 0o644)
	h := vodHLS(api)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/"+s.ID+"/index.m3u8?stoken=tok", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "EVENT") {
		t.Fatal("AVKit must not see FFmpeg EVENT")
	}
	if !strings.Contains(rec.Body.String(), "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Fatal(rec.Body.String())
	}
}

func TestJobMuFarRequestsOneFFmpeg(t *testing.T) {
	api := testAPI(t, nil, nil)
	s := vodSession(t, api, 600_000)
	s.AbsPath = "/m.mkv"
	s.Info = &ffmpeg.MediaInfo{DurationMS: 600_000}
	var n atomic.Int32
	api.testInstall = func(sess *Session) error {
		n.Add(1)
		time.Sleep(80 * time.Millisecond)
		sess.cmd = &exec.Cmd{}
		return nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = api.ensureGenerating(s, 100)
		}()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatalf("starts %d want 1", n.Load())
	}
}

func TestJobMuDuplicateSegCoalesce(t *testing.T) {
	api := testAPI(t, nil, nil)
	s := vodSession(t, api, 60_000)
	s.AbsPath = "/m.mkv"
	s.Info = &ffmpeg.MediaInfo{DurationMS: 60_000}
	var n atomic.Int32
	api.testInstall = func(sess *Session) error {
		n.Add(1)
		sess.cmd = &exec.Cmd{}
		return nil
	}
	_ = api.ensureGenerating(s, 5)
	_ = api.ensureGenerating(s, 5)
	if n.Load() != 1 {
		t.Fatalf("starts %d", n.Load())
	}
}

func TestJobMuWaitersDoNotHoldLock(t *testing.T) {
	api := testAPI(t, nil, nil)
	s := vodSession(t, api, 120_000)
	h := vodHLS(api)
	_ = os.WriteFile(filepath.Join(s.Dir, "init.mp4"), []byte("init"), 0o644)
	ready := s.VODPlan.IndexForMS(8_000)
	waitN := s.VODPlan.IndexForMS(40_000)
	_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(ready)), []byte("READY"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(ready+1)), []byte("N"), 0o644)

	var served atomic.Bool
	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(waitN)), []byte("WAIT"), 0o644)
		_ = os.WriteFile(filepath.Join(s.Dir, hls.SegName(waitN+1)), []byte("N"), 0o644)
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/"+s.ID+"/"+hls.SegName(waitN)+"?stoken=tok", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("wait %d", rec.Code)
		}
	}()
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/"+s.ID+"/"+hls.SegName(ready)+"?stoken=tok", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("ready %d %s", rec.Code, rec.Body.String())
			return
		}
		served.Store(true)
	}()
	wg.Wait()
	if !served.Load() {
		t.Fatal("on-disk segment must serve while another waiter is generating")
	}
}

func TestSessionJSONOriginZeroForVOD(t *testing.T) {
	api := testAPI(t, nil, nil)
	s := vodSession(t, api, 10193_184)
	s.SeekableFromMS = 0
	s.VODPlanKind = planKindKeyframe
	out := api.sessionJSON(s)
	if out["vod_ondemand"] != true {
		t.Fatal("flag")
	}
	if out["seekable_from_ms"] != 0 {
		t.Fatalf("origin %v", out["seekable_from_ms"])
	}
}
