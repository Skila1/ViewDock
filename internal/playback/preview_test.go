package playback

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
)

func TestPreviewsComeFromLocalFilesOnly(t *testing.T) {
	if previewSource(&Session{AbsPath: "/media/a.mkv"}) != "/media/a.mkv" {
		t.Fatal("a local file has previews")
	}
	if previewSource(&Session{AbsPath: "/media/a.mkv", RemoteURL: "https://jf/stream"}) != "" {
		t.Fatal("a stream from an external source must not be read for previews")
	}
	api := testAPI(t, nil, nil)
	if _, ok := api.sessionJSON(&Session{ID: "r", RemoteURL: "https://jf/stream"})["urls"].(map[string]string)["preview"]; ok {
		t.Fatal("no preview url without a local file")
	}
	urls := api.sessionJSON(&Session{ID: "l", AbsPath: "/media/a.mkv", Stoken: "tok"})["urls"].(map[string]string)
	if urls["preview"] != "/api/v1/playback/sessions/l/preview?stoken=tok" {
		t.Fatalf("preview url %q", urls["preview"])
	}
}

func TestPreviewFilterTonemapsHDRAfterScaling(t *testing.T) {
	if got := previewFilter("", true); got != "scale=-2:180" {
		t.Fatalf("SDR filter %q", got)
	}
	got := previewFilter("hdr10", true)
	if got[:len("scale=-2:180,zscale")] != "scale=-2:180,zscale" {
		t.Fatalf("HDR must scale first, then tonemap: %q", got)
	}
}

func TestPreviewServesTheFrameForItsTenSeconds(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	media := filepath.Join(dir, "clip.mp4")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24",
		"-t", "35", "-c:v", "libx264", "-g", "48", "-pix_fmt", "yuv420p", media)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("make clip: %v %s", err, out)
	}
	api := testAPI(t, nil, nil)
	api.Reg.Put(&Session{ID: "s1", Kind: "user", UserID: "u1", AbsPath: media, DurationMS: 35000, LastPing: time.Now(), Created: time.Now()})
	r := chi.NewRouter()
	r.Route("/api/v1", api.Routes)
	h := withUser(&auth.Principal{Kind: auth.KindUser, UserID: "u1"}, r)

	get := func(q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/s1/preview"+q, nil))
		return rec
	}
	rec := get("?t=27")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Body.Len() < 1000 {
		t.Fatalf("preview %d %q %d bytes", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	tdir, err := api.previews.titleDir(media)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tdir, "20.jpg")); err != nil {
		t.Fatalf("27s should be cached as the 20s preview: %v", err)
	}
	if rec := get("?t=500"); rec.Code != 200 {
		t.Fatalf("past the end clamps to the last preview, got %d", rec.Code)
	}
	if rec := get("?t=soon"); rec.Code != 400 {
		t.Fatalf("bad time %d", rec.Code)
	}
}
