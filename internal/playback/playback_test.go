package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/progress"
	"github.com/viewdock/viewdock/internal/share"
)

type mockLocator struct {
	file *library.LocatedFile
	f    *os.File
}

func (m *mockLocator) LocateItem(ctx context.Context, itemKind, itemID string) (*library.LocatedFile, error) {
	if m.file == nil || m.file.ItemKind != itemKind || m.file.ItemID != itemID {
		return nil, os.ErrNotExist
	}
	return m.file, nil
}
func (m *mockLocator) LocateFile(ctx context.Context, id string) (*library.LocatedFile, error) {
	if m.file == nil || m.file.ID != id {
		return nil, os.ErrNotExist
	}
	return m.file, nil
}
func (m *mockLocator) Contains(libraryID, absPath string) error { return nil }
func (m *mockLocator) Open(ctx context.Context, id string) (*os.File, error) {
	return os.Open(m.file.AbsPath)
}

type mockProber struct{ info *ffmpeg.MediaInfo }

func (m mockProber) ProbeFile(context.Context, string) (*ffmpeg.MediaInfo, error) { return m.info, nil }

type mockGrants struct{ ok bool }

func (g mockGrants) CanRead(context.Context, string, string) bool     { return g.ok }
func (g mockGrants) CanDownload(context.Context, string, string) bool { return g.ok }
func (g mockGrants) GrantedLibraryIDs(context.Context, string) ([]string, error) {
	return []string{"lib"}, nil
}

func testAPI(t *testing.T, loc *mockLocator, p progress.Store) *API {
	t.Helper()
	a := New(Deps{
		Cfg: config.Load(), Locator: loc, Grants: mockGrants{ok: true},
		Gate: share.NoopGate(), Prober: mockProber{info: &ffmpeg.MediaInfo{
			DurationMS: 120000, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
			Width: 1920, Height: 1080, Size: 1000,
			Streams: []ffmpeg.Stream{{Index: 0, Kind: "video", Codec: "h264"}, {Index: 1, Kind: "audio", Codec: "aac"}},
		}},
		FF: &ffmpeg.Tool{FFmpeg: "ffmpeg", FFprobe: "ffprobe"},
		Progress: p, CacheDir: t.TempDir(), Slots: 2,
	})
	t.Cleanup(a.Close)
	return a
}

func withUser(p *auth.Principal, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

func TestCreateDirectPlayAndFileRange(t *testing.T) {
	media := filepath.Join(t.TempDir(), "v.mp4")
	payload := bytes.Repeat([]byte("A"), 4096)
	if err := os.WriteFile(media, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	loc := &mockLocator{file: &library.LocatedFile{
		ID: "f1", LibraryID: "lib", AbsPath: media, ItemKind: "movie", ItemID: "m1",
		Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, DurationMS: 120000,
	}}
	api := testAPI(t, loc, nil)
	r := chi.NewRouter()
	r.Route("/api/v1", api.Routes)
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u1", DisplayName: "Ada"}
	h := withUser(user, r)

	body, _ := json.Marshal(map[string]any{"item_kind": "movie", "item_id": "m1", "client": map[string]any{"mse": true}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:9"
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var sess map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	if sess["delivery"] != "direct" {
		t.Fatalf("%v", sess)
	}
	reasons := sess["decision"].(map[string]any)["reasons"].([]any)
	if len(reasons) == 0 {
		t.Fatal("expected inspector reasons")
	}
	id := sess["id"].(string)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/"+id+"/file", nil)
	req.RemoteAddr = "127.0.0.1:9"
	req.Header.Set("Range", "bytes=0-9")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("ct %s", rec.Header().Get("Content-Type"))
	}
	b, _ := io.ReadAll(rec.Body)
	if string(b) != "AAAAAAAAAA" {
		t.Fatalf("body %q", b)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/playback/sessions/"+id, nil)
	req.RemoteAddr = "127.0.0.1:9"
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("delete %d", rec.Code)
	}
}

func TestGuestSkipsProgressDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if _, err := sqlDB.Exec(`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at)
		VALUES ('u1','u','x','U','',0,0,'','t','t')`); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(t.TempDir(), "v.mp4")
	_ = os.WriteFile(media, []byte("hello world"), 0o644)
	loc := &mockLocator{file: &library.LocatedFile{
		ID: "f1", LibraryID: "lib", AbsPath: media, ItemKind: "movie", ItemID: "m1",
		Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
	}}
	store := progress.New(sqlDB)
	api := testAPI(t, loc, store)
	api.Gate = allowGate{}
	r := chi.NewRouter()
	r.Route("/api/v1", api.Routes)
	guest := &auth.Principal{Kind: auth.KindGuestShare, GuestSessionID: "g1", MediaKind: "movie", MediaID: "m1", DisplayName: "Guest"}
	h := withUser(guest, r)

	body, _ := json.Marshal(map[string]any{"item_kind": "movie", "item_id": "m1", "share_token": "not-auth"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:9"
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var sess map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	id := sess["id"].(string)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/playback/sessions/"+id+"/progress", strings.NewReader(`{"position_ms":9000,"duration_ms":100000}`))
	req.RemoteAddr = "127.0.0.1:9"
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("progress %d %s", rec.Code, rec.Body.String())
	}
	var n int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM playback_progress`).Scan(&n)
	if n != 0 {
		t.Fatalf("guest wrote progress: %d", n)
	}
}

func TestLease410(t *testing.T) {
	media := filepath.Join(t.TempDir(), "v.mp4")
	_ = os.WriteFile(media, []byte("xx"), 0o644)
	loc := &mockLocator{file: &library.LocatedFile{
		ID: "f1", LibraryID: "lib", AbsPath: media, ItemKind: "movie", ItemID: "m1",
		Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
	}}
	api := testAPI(t, loc, nil)
	api.Lease = 20 * time.Millisecond
	r := chi.NewRouter()
	r.Route("/api/v1", api.Routes)
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u1"}
	h := withUser(user, r)
	body, _ := json.Marshal(map[string]any{"item_kind": "movie", "item_id": "m1"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:9"
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var sess map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	id, _ := sess["id"].(string)
	if id == "" {
		t.Fatalf("no id %s", rec.Body.String())
	}
	s := api.Reg.Get(id)
	s.LastPing = time.Now().Add(-time.Second)
	api.Reg.Expire(api.Lease, api.kill)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/"+id+"/file", nil)
	req.RemoteAddr = "127.0.0.1:9"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("want 410 got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "resume_ms") {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestMissingFile404(t *testing.T) {
	api := testAPI(t, &mockLocator{}, nil)
	r := chi.NewRouter()
	r.Route("/api/v1", api.Routes)
	h := withUser(&auth.Principal{Kind: auth.KindUser, UserID: "u1"}, r)
	body, _ := json.Marshal(map[string]any{"item_kind": "movie", "item_id": "missing"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:9"
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

type allowGate struct{}

func (allowGate) AllowStream(context.Context, string, string, string) error     { return nil }
func (allowGate) CanStreamMedia(context.Context, string, string, string) error { return nil }
func (allowGate) Heartbeat(context.Context, string) error                      { return nil }
func (allowGate) Release(context.Context, string)                              {}
func (allowGate) ShareTokenForGuest(context.Context, string) string            { return "tok" }
