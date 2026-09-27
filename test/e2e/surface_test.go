//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestAPISurface exercises read and write endpoints across modules so dialect
// or wiring faults surface as 5xx responses rather than hiding behind the SPA.
func TestAPISurface(t *testing.T) {
	c := newClient(t, env(t, "VD_E2E_URL", ""))
	bootstrap(t, c)
	movies := scanAndWait(t, c, 1)
	movieID := fmt.Sprint(movies[0]["id"])

	gets := []string{
		"/api/v1/system", "/api/v1/me", "/api/v1/me/preferences", "/api/v1/me/sessions", "/api/v1/me/identities",
		"/api/v1/libraries", "/api/v1/movies", "/api/v1/movies/" + movieID, "/api/v1/series",
		"/api/v1/search?q=signal", "/api/v1/search/smart?q=unwatched+2000s+comedy",
		"/api/v1/playback/continue", "/api/v1/household",
		"/api/v1/users", "/api/v1/invites", "/api/v1/admin/roles", "/api/v1/admin/settings",
		"/api/v1/admin/integrations/discord", "/api/v1/admin/api-keys", "/api/v1/admin/logs",
		"/api/v1/admin/streams", "/api/v1/admin/stats", "/api/v1/admin/nodes",
		"/api/v1/admin/diagnostics/sources", "/api/v1/admin/updates", "/api/v1/admin/audit",
	}
	for _, path := range gets {
		status, raw, err := c.try("GET", path, nil)
		if err != nil || status >= 500 {
			t.Errorf("GET %s: status %d err %v body %s", path, status, err, truncate(raw))
		}
	}
	mounted := []string{
		"/api/v1/admin/resilience", "/api/v1/admin/resilience/sessions", "/api/v1/admin/resilience/reliability",
		"/api/v1/admin/backups", "/api/v1/admin/households", "/api/v1/content-restriction", "/api/v1/offline/policy",
		"/api/v1/admin/integrations/discord/interactions", "/api/v1/auth/discord/activity",
	}
	for _, path := range mounted {
		status, raw, err := c.try("GET", path, nil)
		if err != nil || status != 200 {
			t.Errorf("GET %s: status %d err %v body %s", path, status, err, truncate(raw))
		}
	}
	var dash map[string]any
	c.do("GET", "/api/v1/admin/resilience", nil, &dash, 200)
	for _, section := range []string{"database", "coordinator", "storage"} {
		sec, _ := dash[section].(map[string]any)
		if sec == nil || sec["status"] != "ok" {
			t.Errorf("resilience %s section not ok: %v", section, sec)
		}
	}
	// Collections are shared across accounts, so only library managers may
	// change them.
	var col map[string]any
	c.do("POST", "/api/v1/collections", map[string]any{"name": "E2E shelf"}, &col, 201)
	c.do("DELETE", "/api/v1/collections/"+fmt.Sprint(col["id"]), nil, nil, 204)

	// Discord calls the interactions endpoint without cookies or CSRF tokens;
	// with no application public key configured it must refuse as disabled.
	resp, err := http.Post(env(t, "VD_E2E_URL", "")+"/api/v1/integrations/discord/interactions", "application/json", strings.NewReader(`{"type":1}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "discord_disabled") {
		t.Errorf("anonymous interactions POST: %d %s", resp.StatusCode, truncate(body))
	}

	c.do("PUT", "/api/v1/me/preferences", map[string]any{"audio_lang": "en", "subtitle_mode": "auto", "autoplay": true}, nil, 200)
	c.do("PATCH", "/api/v1/me", map[string]any{"display_name": "Admin E2E"}, nil, 200)

	var user map[string]any
	status, raw, _ := c.try("POST", "/api/v1/users", map[string]any{"username": "e2e-member", "password": "member-password-123", "display_name": "Member"})
	if status >= 500 {
		t.Fatalf("create user: %d %s", status, truncate(raw))
	}
	_ = user

	status, raw, _ = c.try("POST", "/api/v1/household", map[string]any{"name": "E2E Home"})
	if status >= 500 {
		t.Errorf("create household: %d %s", status, truncate(raw))
	}
	status, raw, _ = c.try("POST", "/api/v1/household/invites", map[string]any{"days": 2})
	if status >= 500 {
		t.Errorf("household invite: %d %s", status, truncate(raw))
	}

	var room map[string]any
	c.do("POST", "/api/v1/watch-together/rooms", map[string]any{"item_kind": "movie", "item_id": movieID}, &room, 200)
	roomID := fmt.Sprint(room["room_id"])
	c.do("POST", "/api/v1/watch-together/rooms/"+roomID+"/ticket", map[string]any{}, nil, 200)
	c.do("POST", "/api/v1/watch-together/rooms/"+roomID+"/queue", map[string]any{"item_kind": "movie", "item_id": movieID, "title": "Next"}, nil, 200)
	c.do("POST", "/api/v1/watch-together/rooms/"+roomID+"/vote", map[string]any{"item_id": movieID}, nil, 200)

	status, raw, _ = c.try("POST", "/api/v1/admin/nodes", map[string]any{"name": "e2e-node", "host": "127.0.0.1", "port": 8080, "scheme": "http", "role": "media-worker"})
	if status >= 500 {
		t.Errorf("register node: %d %s", status, truncate(raw))
	}
}
