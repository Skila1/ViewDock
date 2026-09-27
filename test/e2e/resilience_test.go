//go:build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"testing"
	"time"
)

// TestPartyOnlyGuest verifies a temporary party-only account sees no catalogue
// and can stream only the title of a watch party it joined.
func TestPartyOnlyGuest(t *testing.T) {
	base := env(t, "VD_E2E_URL", "")
	admin := newClient(t, base)
	bootstrap(t, admin)
	movies := scanAndWait(t, admin, 2)
	inParty, outside := fmt.Sprint(movies[0]["id"]), fmt.Sprint(movies[1]["id"])

	var g map[string]any
	admin.do("POST", "/api/v1/guests", map[string]any{"expires_in_hours": 1, "party_only": true, "max_sessions": 1}, &g, 201)
	guestID := fmt.Sprint(g["id"])
	defer admin.do("DELETE", "/api/v1/guests/"+guestID, nil, nil, 200, 404)

	var room map[string]any
	admin.do("POST", "/api/v1/watch-together/rooms", map[string]any{"item_kind": "movie", "item_id": inParty}, &room, 200)

	guest := newClient(t, base)
	guest.ensureCSRF()
	guest.do("POST", "/api/v1/auth/login", map[string]any{"username": g["username"], "password": g["password"]}, nil, 200)
	guest.csrf = ""
	guest.ensureCSRF()

	second := newClient(t, base)
	second.ensureCSRF()
	second.do("POST", "/api/v1/auth/login", map[string]any{"username": g["username"], "password": g["password"]}, nil, 429)

	var visible []map[string]any
	guest.do("GET", "/api/v1/movies", nil, &visible)
	if len(visible) != 0 {
		t.Fatalf("party-only guest sees %d catalogue titles", len(visible))
	}
	guest.do("POST", "/api/v1/watch-together/rooms", map[string]any{"item_kind": "movie", "item_id": inParty}, nil, 404)
	guest.do("POST", "/api/v1/playback/sessions", map[string]any{"item_kind": "movie", "item_id": inParty, "client": map[string]any{"mse": true}}, nil, 404)

	guest.do("POST", "/api/v1/watch-together/join", map[string]any{"invite_code": room["invite_code"]}, nil, 200)
	sess := createSession(t, guest, "movie", inParty, 0, "")
	guest.do("DELETE", sessionAPIBase(sess), nil, nil, 200, 204)
	guest.do("POST", "/api/v1/playback/sessions", map[string]any{"item_kind": "movie", "item_id": outside, "client": map[string]any{"mse": true}}, nil, 404)

	admin.do("DELETE", "/api/v1/guests/"+guestID, nil, nil, 200)
	guest.do("GET", "/api/v1/me", nil, nil, 401)
}

// TestDatabaseOutage pauses the Postgres container and checks that ViewDock
// reports the outage precisely, keeps serving the client and recovers.
// Requires VD_E2E_PG_CONTAINER and a docker CLI on the host.
func TestDatabaseOutage(t *testing.T) {
	base := env(t, "VD_E2E_URL", "")
	pg := env(t, "VD_E2E_PG_CONTAINER", "")
	c := newClient(t, base)
	bootstrap(t, c)
	c.do("GET", "/api/v1/me", nil, nil, 200)

	if out, err := exec.Command("docker", "pause", pg).CombinedOutput(); err != nil {
		t.Fatalf("docker pause: %v %s", err, out)
	}
	paused := true
	defer func() {
		if paused {
			_ = exec.Command("docker", "unpause", pg).Run()
		}
	}()

	var body map[string]any
	status := c.do("GET", "/api/v1/system", nil, &body, 503)
	if body["code"] != "database_unavailable" {
		t.Fatalf("system during outage: %d %v", status, body)
	}
	c.do("GET", "/readyz", nil, nil, 503)
	c.do("GET", "/healthz", nil, nil, 200)
	if status, _, err := c.try("GET", "/", nil); err != nil || status != 200 {
		t.Fatalf("app shell during outage: %d %v", status, err)
	}
	if status, raw, _ := c.try("GET", "/api/v1/me", nil); status == 401 || status == 200 && len(raw) == 0 {
		t.Fatalf("signed-in session was rejected during the outage: %d", status)
	}

	if out, err := exec.Command("docker", "unpause", pg).CombinedOutput(); err != nil {
		t.Fatalf("docker unpause: %v %s", err, out)
	}
	paused = false
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, _, err := c.try("GET", "/api/v1/system", nil)
		if err == nil && status == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not recover: %d %v", status, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	c.do("GET", "/api/v1/me", nil, nil, 200)
	c.do("GET", "/readyz", nil, nil, 200)
}
