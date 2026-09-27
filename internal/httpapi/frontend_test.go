package httpapi

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/viewdock/viewdock/internal/config"
)

func frontendFor(t *testing.T, upstream string) http.Handler {
	t.Helper()
	u, err := ParseControlURL(upstream)
	if err != nil {
		t.Fatal(err)
	}
	web := fstest.MapFS{
		"index.html":    {Data: []byte("<html>shell</html>")},
		"sw.js":         {Data: []byte("self.x=1")},
		"assets/app.js": {Data: []byte("app")},
	}
	return New(config.Config{}, nil, nil, web).FrontendHandler(u)
}

func TestFrontendServesShellAndRelaysAPI(t *testing.T) {
	var seen *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(r.Context())
		w.Header().Set("Set-Cookie", "vd_session=abc; Path=/; HttpOnly")
		WriteJSON(w, http.StatusOK, map[string]string{"path": r.URL.Path})
	}))
	defer upstream.Close()
	h := frontendFor(t, upstream.URL)

	for path, want := range map[string]string{"/": "shell", "/library/movies": "shell", "/sw.js": "self.x=1", "/assets/app.js": "app"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "http://viewdock.example/api/v1/auth/login", strings.NewReader(`{}`))
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("Cookie", "vd_session=old")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || seen == nil || seen.URL.Path != "/api/v1/auth/login" {
		t.Fatalf("relay %d %s", rec.Code, rec.Body.String())
	}
	if seen.Host != "viewdock.example" {
		t.Errorf("upstream host %q, want the public host", seen.Host)
	}
	if got := seen.Header.Get("X-Forwarded-For"); got != "192.0.2.1" {
		t.Errorf("X-Forwarded-For %q: an untrusted client value was forwarded", got)
	}
	if seen.Header.Get("Cookie") != "vd_session=old" || rec.Header().Get("Set-Cookie") == "" {
		t.Error("session cookies must pass through the frontend in both directions")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("API responses must not be cached")
	}
}

func TestFrontendKeepsServingWhenControlIsDown(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	addr := upstream.URL
	upstream.Close()
	h := frontendFor(t, addr)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system", nil))
	var body ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusBadGateway || body.Code != "control_unavailable" {
		t.Fatalf("api with control down: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "shell") {
		t.Fatalf("shell with control down: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"control":"unavailable"`) {
		t.Fatalf("readyz: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFrontendRelaysWebSocketUpgrade(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/watch-together/rooms/r1/ws" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		line, _ := rw.ReadString('\n')
		_, _ = rw.WriteString("echo:" + line)
		_ = rw.Flush()
	}))
	defer upstream.Close()
	front := httptest.NewServer(frontendFor(t, upstream.URL))
	defer front.Close()

	fu, _ := url.Parse(front.URL)
	conn, err := net.DialTimeout("tcp", fu.Host, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "GET /api/v1/watch-together/rooms/r1/ws HTTP/1.1\r\nHost: "+fu.Host+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	_, _ = io.WriteString(conn, "hello\n")
	line, err := br.ReadString('\n')
	if err != nil || line != "echo:hello\n" {
		t.Fatalf("tunnel: %q %v", line, err)
	}
}

func TestParseControlURL(t *testing.T) {
	for _, bad := range []string{"", "control:8080", "ftp://c", "http://user:pw@c", "http://c/api", "http://c?x=1"} {
		if _, err := ParseControlURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if u, err := ParseControlURL("http://viewdock-control:8080/"); err != nil || u.String() != "http://viewdock-control:8080" {
		t.Fatalf("valid url: %v %v", u, err)
	}
}

func TestCoordinatorRelay(t *testing.T) {
	var cookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie = r.Header.Get("Cookie")
		WriteJSON(w, http.StatusOK, map[string]string{"path": r.URL.Path})
	}))
	u, err := ParseUpstreamURL("VD_COORDINATOR_URL", upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	relay := New(config.Config{}, nil, nil, fstest.MapFS{}).CoordinatorRelay(u)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/watch-together/rooms", strings.NewReader(`{}`))
	req.Header.Set("Cookie", "vd_session=abc")
	rec := httptest.NewRecorder()
	relay.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/api/v1/watch-together/rooms") || cookie != "vd_session=abc" {
		t.Fatalf("relay: %d %s cookie %q", rec.Code, rec.Body.String(), cookie)
	}

	upstream.Close()
	rec = httptest.NewRecorder()
	relay.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/watch-together/join", strings.NewReader(`{}`)))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "coordinator_unavailable") || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("coordinator down: %d %s", rec.Code, rec.Body.String())
	}
}
