package jellyfin

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// fmp4 is a minimal whole media segment: a moof box and an mdat box.
func fmp4(payload int) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(8))
	b.WriteString("moof")
	_ = binary.Write(&b, binary.BigEndian, uint32(8+payload))
	b.WriteString("mdat")
	b.Write(make([]byte, payload))
	return b.Bytes()
}

func TestCompleteSegment(t *testing.T) {
	whole := fmp4(100)
	if err := completeSegment(bytes.NewReader(whole), int64(len(whole)), ".mp4"); err != nil {
		t.Fatalf("whole segment rejected: %v", err)
	}
	for _, cut := range []int{0, 8, 50, len(whole) - 1} {
		if err := completeSegment(bytes.NewReader(whole[:cut]), int64(cut), ".mp4"); err == nil {
			t.Errorf("segment cut at %d bytes accepted", cut)
		}
	}
	if err := completeSegment(bytes.NewReader(make([]byte, 376)), 376, ".ts"); err != nil {
		t.Errorf("whole ts rejected: %v", err)
	}
	if err := completeSegment(bytes.NewReader(make([]byte, 300)), 300, ".ts"); err == nil {
		t.Error("cut ts accepted")
	}
}

func TestPartyMembersShareOneJellyfinSession(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	jf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/hls1/main/") {
			mu.Lock()
			hits[r.URL.Path+" "+r.URL.Query().Get("PlaySessionId")]++
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			_, _ = w.Write(fmp4(1000))
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(jf.Close)
	svc := New(nil, nil, t.TempDir(), nil)
	now := time.Now()
	plan := "AudioCodec=aac&VideoCodec=hevc"
	svc.grants = map[string]*grant{
		"host":  {sourceID: "s1", remoteID: "m1", base: jf.URL, playID: "p-host", deviceID: "d-host", planKey: plan, created: now.Add(-time.Minute), expires: now.Add(time.Hour)},
		"guest": {sourceID: "s1", remoteID: "m1", base: jf.URL, playID: "p-guest", deviceID: "d-guest", planKey: plan, created: now, expires: now.Add(time.Hour)},
		"other": {sourceID: "s1", remoteID: "m1", base: jf.URL, playID: "p-other", deviceID: "d-other", planKey: "VideoCodec=h264", created: now.Add(-time.Hour), expires: now.Add(time.Hour)},
	}
	r := chi.NewRouter()
	r.Get("/media-sources/stream/{grant}/*", svc.handleStream)
	get := func(tok, seg string) []byte {
		rec := httptest.NewRecorder()
		q := "?AudioCodec=aac&VideoCodec=hevc&PlaySessionId=p-" + tok + "&DeviceId=d-" + tok
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media-sources/stream/"+tok+"/Videos/m1/hls1/main/"+seg+".mp4"+q, nil))
		if rec.Code != 200 {
			t.Fatalf("%s segment %s: %d %s", tok, seg, rec.Code, rec.Body.String())
		}
		body, _ := io.ReadAll(rec.Body)
		return body
	}

	// Alone, the host streams from its own session as before.
	if b := get("host", "10"); len(b) != len(fmp4(1000)) {
		t.Fatalf("host got %d bytes", len(b))
	}
	// The guest joins at the same spot: both ask at once, Jellyfin sees one
	// request, made on the host's session.
	var wg sync.WaitGroup
	for _, tok := range []string{"host", "guest"} {
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			if b := get(tok, "11"); len(b) != len(fmp4(1000)) {
				t.Errorf("%s got %d bytes", tok, len(b))
			}
		}(tok)
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if hits["/Videos/m1/hls1/main/11.mp4 p-host"] != 1 || hits["/Videos/m1/hls1/main/11.mp4 p-guest"] != 0 {
		t.Fatalf("segment 11 requests: %v", hits)
	}
	if hits["/Videos/m1/hls1/main/10.mp4 p-host"] != 1 {
		t.Fatalf("segment 10 requests: %v", hits)
	}
}
