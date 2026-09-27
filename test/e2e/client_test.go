//go:build e2e

// Package e2e drives a running ViewDock deployment over HTTP. It is used by the
// acceptance scripts in scripts/acceptance and is excluded from `go test ./...`.
//
//	VD_E2E_URL=http://127.0.0.1:8080 VD_E2E_SETUP_TOKEN=... go test -tags e2e ./test/e2e -run TestCoreFlow -v
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
	csrf string
}

func newClient(t *testing.T, base string) *client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: strings.TrimRight(base, "/"), http: &http.Client{Jar: jar, Timeout: 60 * time.Second}}
}

func env(t *testing.T, key, def string) string {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	if def == "" {
		t.Skipf("%s not set", key)
	}
	return def
}

func envOr(key string) string { return os.Getenv(key) }

func (c *client) ensureCSRF() {
	if c.csrf != "" {
		return
	}
	var out struct {
		Token string `json:"token"`
	}
	c.do("GET", "/api/v1/auth/csrf", nil, &out, 200)
	c.csrf = out.Token
}

// do sends a JSON request and decodes the response. want lists accepted statuses.
func (c *client) do(method, path string, body any, out any, want ...int) int {
	c.t.Helper()
	status, raw, err := c.try(method, path, body)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	ok := len(want) == 0 && status < 300
	for _, w := range want {
		if status == w {
			ok = true
		}
	}
	if !ok {
		c.t.Fatalf("%s %s: status %d body %s", method, path, status, truncate(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode %v body %s", method, path, err, truncate(raw))
		}
	}
	return status
}

func (c *client) try(method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = c.base + path
	}
	req, err := http.NewRequest(method, target, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" && method != "HEAD" && c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if u, err := url.Parse(c.base); err == nil {
		req.Header.Set("Origin", u.Scheme+"://"+u.Host)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	return res.StatusCode, raw, nil
}

func truncate(b []byte) string {
	if len(b) > 600 {
		return string(b[:600]) + "..."
	}
	return string(b)
}

// bootstrap performs first-run setup or logs in when setup already happened.
func bootstrap(t *testing.T, c *client) {
	t.Helper()
	user := env(t, "VD_E2E_ADMIN_USER", "admin")
	pass := env(t, "VD_E2E_ADMIN_PASS", "correct-horse-battery-staple")
	c.ensureCSRF()
	var st struct {
		Needed bool   `json:"needed"`
		Step   string `json:"step"`
	}
	c.do("GET", "/api/v1/setup/status", nil, &st)
	if st.Needed && st.Step == "admin" {
		token := os.Getenv("VD_E2E_SETUP_TOKEN")
		c.do("POST", "/api/v1/setup/admin", map[string]string{
			"username": user, "password": pass, "display_name": "Admin", "bootstrap_token": token,
		}, nil, 200)
		c.csrf = ""
		c.ensureCSRF()
	} else {
		c.do("POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pass}, nil, 200)
		c.csrf = ""
		c.ensureCSRF()
	}
	if st.Needed {
		if st.Step == "admin" || st.Step == "library" {
			c.do("POST", "/api/v1/setup/library", map[string]string{"name": "Movies", "path": env(t, "VD_E2E_MEDIA_DIR", "/media"), "content_type": "movies"}, nil, 200)
		}
		c.do("GET", "/api/v1/setup/ffmpeg", nil, nil, 200)
		c.do("POST", "/api/v1/setup/tmdb", map[string]any{"skip": true}, nil, 200)
		c.do("POST", "/api/v1/setup/complete", map[string]any{}, nil, 200)
	}
}

func scanAndWait(t *testing.T, c *client, minMovies int) []map[string]any {
	t.Helper()
	var libs []map[string]any
	c.do("GET", "/api/v1/libraries", nil, &libs)
	for _, lib := range libs {
		c.do("POST", fmt.Sprintf("/api/v1/libraries/%v/scan", lib["id"]), map[string]any{}, nil, 200, 202, 409)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var movies []map[string]any
		c.do("GET", "/api/v1/movies", nil, &movies)
		if len(movies) >= minMovies {
			return movies
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("scan did not produce %d movies", minMovies)
	return nil
}
