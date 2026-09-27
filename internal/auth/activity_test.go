package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

const activityClientID = "500000000000000001"

func activitySvc(t *testing.T, enabled *bool) *Service {
	t.Helper()
	s := testSvc(t)
	s.ActivityEnabled = func() bool { return *enabled }
	if err := s.SaveDiscord(context.Background(), DiscordOAuthConfig{LoginEnabled: true, ClientID: activityClientID, Secret: "s3cret"}, true); err != nil {
		t.Fatal(err)
	}
	return s
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestEmbeddedCookiesArePartitioned(t *testing.T) {
	s := testSvc(t)
	req := httptest.NewRequest(http.MethodGet, "/auth/csrf", nil)
	req.Header.Set(EmbedHeader, EmbedDiscordActivity)
	rec := httptest.NewRecorder()
	if _, err := IssueCSRF(rec, req, s.Cfg); err != nil {
		t.Fatal(err)
	}
	c := cookieNamed(rec, CSRFCookie)
	if c == nil || !c.Secure || !c.Partitioned || c.SameSite != http.SameSiteNoneMode {
		t.Fatalf("embedded csrf cookie = %+v", c)
	}

	rec = httptest.NewRecorder()
	if _, err := IssueCSRF(rec, httptest.NewRequest(http.MethodGet, "/auth/csrf", nil), s.Cfg); err != nil {
		t.Fatal(err)
	}
	if c := cookieNamed(rec, CSRFCookie); c == nil || c.Partitioned || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("normal csrf cookie = %+v", c)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.Header.Set(EmbedHeader, EmbedDiscordActivity)
	s.clearSessionCookie(rec, req)
	if c := cookieNamed(rec, SessionCookie); c == nil || c.MaxAge >= 0 || !c.Partitioned {
		t.Fatalf("embedded logout must clear the partitioned cookie: %+v", c)
	}
}

func TestActivityOriginFollowsTheSwitch(t *testing.T) {
	enabled := false
	s := activitySvc(t, &enabled)
	ctx := context.Background()
	if o := s.ActivityOrigin(ctx); o != "" || s.FrameAncestors(ctx) != nil {
		t.Fatalf("off: origin %q", o)
	}
	enabled = true
	if o := s.ActivityOrigin(ctx); o != "" {
		t.Fatalf("cached value must hold until reset, got %q", o)
	}
	s.ResetActivityOrigin()
	want := "https://" + activityClientID + ".discordsays.com"
	if o := s.ActivityOrigin(ctx); o != want {
		t.Fatalf("on: origin %q", o)
	}
	fa := s.FrameAncestors(ctx)
	if len(fa) != 3 || fa[2] != want {
		t.Fatalf("frame ancestors = %v", fa)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/watch-together/x/ws", nil)
	req.Header.Set("Origin", want)
	if !s.ActivityOriginAllowed(req) {
		t.Fatal("activity origin refused")
	}
	req.Header.Set("Origin", "https://600000000000000001.discordsays.com")
	if s.ActivityOriginAllowed(req) {
		t.Fatal("another application's activity origin was accepted")
	}
}

func TestActivityOriginNeedsClientCredentials(t *testing.T) {
	s := testSvc(t)
	s.ActivityEnabled = func() bool { return true }
	if err := s.SaveDiscord(context.Background(), DiscordOAuthConfig{ClientID: "not-a-snowflake", Secret: "x"}, true); err != nil {
		t.Fatal(err)
	}
	if o := s.ActivityOrigin(context.Background()); o != "" {
		t.Fatalf("invalid client id produced origin %q", o)
	}
}

func TestActivityEndpoints(t *testing.T) {
	enabled := false
	s := activitySvc(t, &enabled)
	r := chi.NewRouter()
	r.Use(s.Middleware)
	s.Routes(r)
	get := func() map[string]any {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/discord/activity", nil))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	post := func(body string) int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/discord/activity", strings.NewReader(body)))
		return rec.Code
	}
	if out := get(); out["enabled"] != false || out["client_id"] != nil {
		t.Fatalf("off config = %v", out)
	}
	if code := post(`{"code":"abc"}`); code != http.StatusServiceUnavailable {
		t.Fatalf("off sign-in = %d", code)
	}
	enabled = true
	s.ResetActivityOrigin()
	out := get()
	if out["enabled"] != true || out["client_id"] != activityClientID {
		t.Fatalf("on config = %v", out)
	}
	if scopes, _ := out["scopes"].([]any); len(scopes) != 1 || scopes[0] != "identify" {
		t.Fatalf("scopes = %v", out["scopes"])
	}
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), "s3cret") {
		t.Fatal("client secret leaked in the public Activity config")
	}
	for _, body := range []string{`{}`, `{"code":""}`, `not json`, `{"code":"` + strings.Repeat("a", 600) + `"}`} {
		if code := post(body); code != http.StatusBadRequest {
			t.Errorf("sign-in %q = %d", body, code)
		}
	}
}
