//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestCoreFlow covers acceptance test 11: setup, library scan, FFmpeg probing,
// search, playback session creation, HLS delivery, progress and teardown.
func TestCoreFlow(t *testing.T) {
	c := newClient(t, env(t, "VD_E2E_URL", ""))
	bootstrap(t, c)
	movies := scanAndWait(t, c, 2)
	movie := movies[0]
	for _, m := range movies {
		if strings.Contains(fmt.Sprint(m["title"]), "Comedy") {
			movie = m
		}
	}
	id := fmt.Sprint(movie["id"])

	title := fmt.Sprint(movie["title"])
	word := strings.Fields(title)[0]
	var hits []map[string]any
	c.do("GET", "/api/v1/search?q="+word, nil, &hits)
	if len(hits) == 0 {
		t.Fatalf("search %q returned no hits", word)
	}

	direct := createSession(t, c, "movie", id, 0, "auto")
	if direct["delivery"] == "direct" {
		durls, _ := direct["urls"].(map[string]any)
		status, raw, err := c.try("GET", fmt.Sprint(durls["file"]), nil)
		if err != nil || status != 200 || len(raw) < 1024 {
			t.Fatalf("direct file status=%d len=%d err=%v", status, len(raw), err)
		}
	}
	c.do("DELETE", sessionAPIBase(direct), nil, nil, 204)

	sess := createSession(t, c, "movie", id, 30000, "480")
	if sess["delivery"] != "hls" {
		t.Fatalf("480p request on a 720p source should transcode: %+v", sess["decision"])
	}
	urls, _ := sess["urls"].(map[string]any)
	playlist := fmt.Sprint(urls["playlist"])
	if playlist == "" || playlist == "<nil>" {
		t.Fatalf("session has no playlist: %+v", sess)
	}
	body := waitPlaylist(t, c, playlist)
	if !strings.Contains(body, "#EXTM3U") {
		t.Fatalf("playlist body: %s", body)
	}
	progressBase := sessionAPIBase(sess)
	c.do("PUT", progressBase+"/progress", map[string]any{"position_ms": 5000, "duration_ms": 60000}, nil, 200)
	c.do("DELETE", progressBase, nil, nil, 204)

	var cont []map[string]any
	c.do("GET", "/api/v1/playback/continue", nil, &cont)
}

func createSession(t *testing.T, c *client, kind, id string, startMS int64, quality string) map[string]any {
	t.Helper()
	var sess map[string]any
	c.do("POST", "/api/v1/playback/sessions", map[string]any{
		"item_kind": kind, "item_id": id, "start_ms": startMS, "quality": quality,
		"client": map[string]any{"mse": true, "hls_native": false, "hevc": false, "av1": false, "viewport_w": 1280, "viewport_h": 720},
	}, &sess, 200)
	return sess
}

// sessionAPIBase returns the per-session API base, which may live on a worker node.
func sessionAPIBase(sess map[string]any) string {
	urls, _ := sess["urls"].(map[string]any)
	if base, ok := urls["session"].(string); ok && base != "" {
		return base
	}
	return fmt.Sprintf("/api/v1/playback/sessions/%v", sess["id"])
}

func waitPlaylist(t *testing.T, c *client, playlist string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		status, raw, err := c.try("GET", playlist, nil)
		if err == nil && status == 200 {
			return string(raw)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("playlist %s never became ready", playlist)
	return ""
}
