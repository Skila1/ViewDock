//go:build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type configState struct {
	Version  int64            `json:"version"`
	Settings []map[string]any `json:"settings"`
}

func settingValue(st configState, key string) string {
	for _, s := range st.Settings {
		if s["key"] == key {
			return fmt.Sprint(s["value"])
		}
	}
	return ""
}

// TestRuntimeConfig covers acceptance test 8: routine settings change through
// the admin API at runtime, are validated and versioned, reject stale writes,
// take effect immediately, store secrets encrypted and can be rolled back.
func TestRuntimeConfig(t *testing.T) {
	base := env(t, "VD_E2E_URL", "")
	c := newClient(t, base)
	bootstrap(t, c)
	movies := scanAndWait(t, c, 2)
	movieID := fmt.Sprint(movies[0]["id"])

	baseline := map[string]any{"features.watch_together": nil, "playback.transcode_slots": nil, "tmdb.api_key": nil}
	reset := func() {
		var cur configState
		c.do("GET", "/api/v1/admin/config", nil, &cur, 200)
		c.do("PUT", "/api/v1/admin/config", map[string]any{"version": cur.Version, "values": baseline, "note": "e2e baseline"}, nil, 200)
	}
	reset()
	t.Cleanup(reset)
	var start configState
	c.do("GET", "/api/v1/admin/config", nil, &start, 200)
	v0 := start.Version

	c.do("PUT", "/api/v1/admin/config", map[string]any{"version": v0, "values": map[string]any{"playback.transcode_slots": "0"}}, nil, 400)

	var st configState
	c.do("PUT", "/api/v1/admin/config", map[string]any{"version": v0, "values": map[string]any{
		"features.watch_together": "false", "playback.transcode_slots": "1", "tmdb.api_key": "e2e-secret-value",
	}, "note": "e2e"}, &st, 200)
	if st.Version != v0+1 || settingValue(st, "features.watch_together") != "0" {
		t.Fatalf("apply: version %d, flag %q", st.Version, settingValue(st, "features.watch_together"))
	}
	c.do("PUT", "/api/v1/admin/config", map[string]any{"version": v0, "values": map[string]any{"playback.transcode_slots": "4"}}, nil, 409)

	_, raw, _ := c.try("GET", "/api/v1/admin/config", nil)
	if strings.Contains(string(raw), "e2e-secret-value") {
		t.Fatal("secret returned by the config API")
	}
	_, raw, _ = c.try("GET", "/api/v1/admin/config/history", nil)
	if strings.Contains(string(raw), "e2e-secret-value") {
		t.Fatal("secret returned by config history")
	}
	if pg := strings.TrimSpace(envOr("VD_E2E_PG_CONTAINER")); pg != "" {
		out, err := exec.Command("docker", "exec", pg, "psql", "-U", "viewdock", "-tAc", "SELECT value FROM server_settings WHERE key = 'tmdb.api_key'").CombinedOutput()
		if err != nil {
			t.Fatalf("psql: %v %s", err, out)
		}
		if !strings.HasPrefix(strings.TrimSpace(string(out)), "enc:v1:") {
			t.Fatalf("secret stored unencrypted: %q", strings.TrimSpace(string(out)))
		}
	}

	var sys map[string]any
	c.do("GET", "/api/v1/system", nil, &sys, 200)
	if f, _ := sys["features"].(map[string]any); f["watch_together"] != false {
		t.Fatalf("system features not updated: %v", sys["features"])
	}
	if peer := envOr("VD_E2E_URL2"); peer != "" {
		pc := newClient(t, peer)
		deadline := time.Now().Add(10 * time.Second)
		for {
			var ps map[string]any
			pc.do("GET", "/api/v1/system", nil, &ps, 200)
			if f, _ := ps["features"].(map[string]any); f["watch_together"] == false {
				t.Logf("peer process applied version %d", st.Version)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("peer process did not pick up the change: %v", ps["features"])
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	var denied map[string]any
	c.do("POST", "/api/v1/watch-together/rooms", map[string]any{"item_kind": "movie", "item_id": movieID}, &denied, 403)
	if denied["code"] != "feature_disabled" {
		t.Fatalf("watch party not gated: %v", denied)
	}

	var hd string
	for _, m := range movies {
		if strings.Contains(fmt.Sprint(m["title"]), "Comedy") {
			hd = fmt.Sprint(m["id"])
		}
	}
	if hd == "" {
		t.Fatal("720p fixture missing")
	}
	first := createSession(t, c, "movie", hd, 0, "480")
	if d, _ := first["decision"].(map[string]any); !strings.HasPrefix(fmt.Sprint(d["mode"]), "transcode") {
		t.Fatalf("720p at 480 did not transcode: %v", d["mode"])
	}
	status, raw, _ := c.try("POST", "/api/v1/users", map[string]any{"username": "e2e-slots", "password": "slots-password-123", "display_name": "Slots"})
	if status != 201 && status != 409 {
		t.Fatalf("create user: %d %s", status, truncate(raw))
	}
	other := newClient(t, base)
	other.ensureCSRF()
	other.do("POST", "/api/v1/auth/login", map[string]string{"username": "e2e-slots", "password": "slots-password-123"}, nil, 200)
	other.csrf = ""
	other.ensureCSRF()
	req := map[string]any{"item_kind": "movie", "item_id": hd, "start_ms": 0, "quality": "480",
		"client": map[string]any{"mse": true, "viewport_w": 1280, "viewport_h": 720}}
	var full map[string]any
	other.do("POST", "/api/v1/playback/sessions", req, &full, 429)
	if full["code"] != "LOAD_429" {
		t.Fatalf("slot limit: %v", full)
	}
	c.do("DELETE", sessionAPIBase(first), nil, nil, 200, 204)
	var freed map[string]any
	other.do("POST", "/api/v1/playback/sessions", req, &freed, 200)
	other.do("DELETE", sessionAPIBase(freed), nil, nil, 200, 204)

	var rolled configState
	c.do("POST", "/api/v1/admin/config/rollback", map[string]any{"target_version": v0, "version": st.Version}, &rolled, 200)
	if settingValue(rolled, "features.watch_together") != "1" || settingValue(rolled, "playback.transcode_slots") != settingValue(start, "playback.transcode_slots") {
		t.Fatalf("rollback did not restore version %d", v0)
	}
	c.do("POST", "/api/v1/watch-together/rooms", map[string]any{"item_kind": "movie", "item_id": movieID}, nil, 200)
}
