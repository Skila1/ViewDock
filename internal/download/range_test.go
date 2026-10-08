package download

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
)

func serve(t *testing.T, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	ServeFile(rec, req, path, "mp4")
	return rec
}

func tempFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.mp4")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServeFileRanges(t *testing.T) {
	p := tempFile(t, "abcdefghij")
	cases := []struct {
		name, rng, body, contentRange string
		code                          int
	}{
		{"closed", "bytes=2-5", "cdef", "bytes 2-5/10", 206},
		{"open ended", "bytes=7-", "hij", "bytes 7-9/10", 206},
		{"suffix", "bytes=-3", "hij", "bytes 7-9/10", 206},
		{"end clamped", "bytes=8-100", "ij", "bytes 8-9/10", 206},
		{"unsatisfiable", "bytes=20-30", "", "bytes */10", 416},
		{"none", "", "abcdefghij", "", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := map[string]string{}
			if tc.rng != "" {
				h["Range"] = tc.rng
			}
			rec := serve(t, p, h)
			if rec.Code != tc.code {
				t.Fatalf("code %d want %d", rec.Code, tc.code)
			}
			if tc.code != 416 && rec.Body.String() != tc.body {
				t.Fatalf("body %q want %q", rec.Body.String(), tc.body)
			}
			if got := rec.Header().Get("Content-Range"); got != tc.contentRange {
				t.Fatalf("content-range %q want %q", got, tc.contentRange)
			}
			if rec.Header().Get("Accept-Ranges") != "bytes" && tc.code != 416 {
				t.Fatal("missing Accept-Ranges")
			}
		})
	}
}

func TestServeFileETagAndIfRange(t *testing.T) {
	p := tempFile(t, "abcdefghij")
	first := serve(t, p, map[string]string{"Range": "bytes=0-3"})
	tag := first.Header().Get("ETag")
	if tag == "" || tag[0] != '"' {
		t.Fatalf("strong etag expected, got %q", tag)
	}
	if first.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("content type %q", first.Header().Get("Content-Type"))
	}

	resumed := serve(t, p, map[string]string{"Range": "bytes=4-", "If-Range": tag})
	if resumed.Code != 206 || resumed.Body.String() != "efghij" {
		t.Fatalf("matching If-Range: %d %q", resumed.Code, resumed.Body.String())
	}

	stale := serve(t, p, map[string]string{"Range": "bytes=4-", "If-Range": `"stale"`})
	if stale.Code != 200 || stale.Body.String() != "abcdefghij" {
		t.Fatalf("stale If-Range must return the full body: %d %q", stale.Code, stale.Body.String())
	}

	notModified := serve(t, p, map[string]string{"If-None-Match": tag})
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d", notModified.Code)
	}
}

func TestETagChangesWithContent(t *testing.T) {
	p := tempFile(t, "abcdefghij")
	before := serve(t, p, nil).Header().Get("ETag")
	later := time.Now().Add(time.Hour)
	if err := os.WriteFile(p, []byte("abcdefghijk"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(p, later, later)
	after := serve(t, p, nil).Header().Get("ETag")
	if before == after {
		t.Fatalf("etag did not change: %s", before)
	}
}

func TestServeFileMissing(t *testing.T) {
	rec := serve(t, filepath.Join(t.TempDir(), "missing.mp4"), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code %d", rec.Code)
	}
}

type fakeGrants struct {
	ids      []string
	download map[string]bool
	err      error
}

func (f fakeGrants) CanRead(context.Context, string, string) bool { return true }
func (f fakeGrants) CanDownload(_ context.Context, _ string, lib string) bool {
	return f.download[lib]
}
func (f fakeGrants) GrantedLibraryIDs(context.Context, string) ([]string, error) {
	return f.ids, f.err
}

func TestPolicyDefaultsAndClamping(t *testing.T) {
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u"}
	pol := (&PolicyAPI{}).For(context.Background(), user)
	if !pol.Enabled || pol.MaxItems != DefaultMaxItems || pol.ExpiryDays != DefaultExpiryDays || pol.MaxItemBytes != 0 || pol.DefaultAllowed {
		t.Fatalf("defaults: %+v", pol)
	}
	pol = (&PolicyAPI{
		Enabled:    func() bool { return false },
		MaxItems:   func() int { return 0 },
		ExpiryDays: func() int { return 9999 },
		MaxItemGB:  func() int { return 2 },
	}).For(context.Background(), user)
	if pol.Enabled || pol.MaxItems != 1 || pol.ExpiryDays != MaxExpiryDays || pol.MaxItemBytes != 2<<30 {
		t.Fatalf("clamped: %+v", pol)
	}
}

func TestPolicyLibraries(t *testing.T) {
	grants := fakeGrants{ids: []string{"a", "b"}, download: map[string]bool{"a": true}}
	api := &PolicyAPI{Grants: grants}
	pol := api.For(context.Background(), &auth.Principal{Kind: auth.KindUser, UserID: "u"})
	if !pol.Libraries["a"] || pol.Libraries["b"] || len(pol.Libraries) != 2 {
		t.Fatalf("user libraries: %+v", pol.Libraries)
	}
	admin := api.For(context.Background(), &auth.Principal{Kind: auth.KindUser, UserID: "root", IsAdmin: true})
	if !admin.DefaultAllowed || !admin.Libraries["a"] || !admin.Libraries["b"] {
		t.Fatalf("admin libraries: %+v", admin)
	}
	failing := (&PolicyAPI{Grants: fakeGrants{err: errors.New("db down")}}).For(context.Background(), &auth.Principal{Kind: auth.KindUser, UserID: "u"})
	if len(failing.Libraries) != 0 || failing.DefaultAllowed {
		t.Fatalf("grant failure must deny: %+v", failing)
	}
}

func routed(api *PolicyAPI, p *auth.Principal) http.Handler {
	r := chi.NewRouter()
	if p != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(auth.WithPrincipal(req.Context(), p)))
			})
		})
	}
	api.Routes(r)
	return r
}

func TestPolicyEndpoint(t *testing.T) {
	api := &PolicyAPI{Grants: fakeGrants{ids: []string{"a"}, download: map[string]bool{"a": true}}}
	rec := httptest.NewRecorder()
	routed(api, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/offline/policy", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	routed(api, &auth.Principal{Kind: auth.KindUser, UserID: "u"}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/offline/policy", nil))
	if rec.Code != 200 {
		t.Fatalf("user: %d %s", rec.Code, rec.Body.String())
	}
	var pol Policy
	if err := json.Unmarshal(rec.Body.Bytes(), &pol); err != nil {
		t.Fatal(err)
	}
	if !pol.Libraries["a"] || pol.MaxItems != DefaultMaxItems {
		t.Fatalf("decoded: %+v", pol)
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache-control %q", rec.Header().Get("Cache-Control"))
	}
}

func TestSpeedTestRange(t *testing.T) {
	h := routed(&PolicyAPI{}, &auth.Principal{Kind: auth.KindUser, UserID: "u"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/offline/speedtest", nil)
	req.Header.Set("Range", "bytes=0-65535")
	h.ServeHTTP(rec, req)
	if rec.Code != 206 || rec.Body.Len() != 65536 {
		t.Fatalf("speedtest: %d %d", rec.Code, rec.Body.Len())
	}
	if rec.Header().Get("Content-Range") != "bytes 0-65535/8388608" {
		t.Fatalf("content-range %q", rec.Header().Get("Content-Range"))
	}
	body := rec.Body.Bytes()
	zeros := 0
	for _, b := range body {
		if b == 0 {
			zeros++
		}
	}
	if zeros > len(body)/64 {
		t.Fatalf("payload looks compressible: %d zero bytes", zeros)
	}
}
