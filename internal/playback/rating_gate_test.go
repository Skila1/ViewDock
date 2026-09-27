package playback

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/share"
)

func TestCreateSessionEnforcesContentRating(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	for _, q := range []string{
		`INSERT INTO libraries(id, name, root_path, content_type, created_at, updated_at) VALUES ('lib', 'L', '/x', 'movies', 't', 't')`,
		`INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('m1', 'lib', 'Grim', 'grim', 't', 't', 17)`,
		`INSERT INTO media_files(id, library_id, rel_path, abs_path, kind, movie_id, created_at, updated_at) VALUES ('f1', 'lib', 'v.mp4', '/x/v.mp4', 'movie', 'm1', 't', 't')`,
		`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, content_age_limit)
			VALUES ('u1', 'u1', 'x', 'U', '', 0, 0, '', 't', 't', 12)`,
	} {
		if _, err := sqlDB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	media := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(media, bytes.Repeat([]byte("A"), 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	loc := &mockLocator{file: &library.LocatedFile{
		ID: "f1", LibraryID: "lib", AbsPath: media, ItemKind: "movie", ItemID: "m1",
		Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, DurationMS: 120000,
	}}
	a := New(Deps{
		Cfg: config.Load(), DB: sqlDB, Locator: loc, Grants: mockGrants{ok: true},
		Gate: share.NoopGate(), Prober: mockProber{info: &ffmpeg.MediaInfo{
			DurationMS: 120000, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
			Width: 1920, Height: 1080, Size: 1024,
			Streams: []ffmpeg.Stream{{Index: 0, Kind: "video", Codec: "h264"}, {Index: 1, Kind: "audio", Codec: "aac"}},
		}},
		FF:       &ffmpeg.Tool{FFmpeg: "ffmpeg", FFprobe: "ffprobe"},
		CacheDir: t.TempDir(), Slots: 2,
	})
	t.Cleanup(a.Close)
	r := chi.NewRouter()
	r.Route("/api/v1", a.Routes)
	h := withUser(&auth.Principal{Kind: auth.KindUser, UserID: "u1"}, r)

	create := func(body map[string]any) int {
		raw, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions", bytes.NewReader(raw))
		req.RemoteAddr = "127.0.0.1:9"
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	client := map[string]any{"mse": true}
	if code := create(map[string]any{"item_kind": "movie", "item_id": "m1", "client": client}); code == http.StatusOK {
		t.Fatal("restricted user started playback of a title above their limit")
	}
	if code := create(map[string]any{"item_kind": "movie", "item_id": "m1", "media_file_id": "f1", "client": client}); code == http.StatusOK {
		t.Fatal("restricted user bypassed the rating check with a direct media file id")
	}
	if _, err := sqlDB.Exec(`UPDATE users SET content_age_limit = 18 WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	if code := create(map[string]any{"item_kind": "movie", "item_id": "m1", "client": client}); code != http.StatusOK {
		t.Fatalf("permitted playback failed: %d", code)
	}
}
