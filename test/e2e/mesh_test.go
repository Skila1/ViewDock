//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestMeshFailover covers acceptance tests 1, 4 and 5 with real worker
// containers: registration with primary and standby priorities, credentialed
// placement, relayed streaming, loss of the primary mid-playback with
// resumption on the standby at the same position, and a frontend that stays
// available throughout. Set VD_E2E_MESH=1; workers join VD_E2E_NETWORK.
func TestMeshFailover(t *testing.T) {
	if envOr("VD_E2E_MESH") != "1" {
		t.Skip("VD_E2E_MESH not set")
	}
	base := env(t, "VD_E2E_URL", "")
	network := env(t, "VD_E2E_NETWORK", "vd-e2e")
	image := env(t, "VD_E2E_IMAGE", "viewdock:dev")
	pgURL := env(t, "VD_E2E_PG_URL", "postgres://viewdock:secret@vd-e2e-pg:5432/viewdock?sslmode=disable")
	c := newClient(t, base)
	bootstrap(t, c)
	movies := scanAndWait(t, c, 2)
	var hd string
	for _, m := range movies {
		if strings.Contains(fmt.Sprint(m["title"]), "Comedy") {
			hd = fmt.Sprint(m["id"])
		}
	}

	docker := func(args ...string) string {
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	var nodes []string
	t.Cleanup(func() {
		for _, name := range []string{"vd-e2e-w1", "vd-e2e-w2", "vd-e2e-ctl", "vd-e2e-fe", "vd-e2e-coord"} {
			_ = exec.Command("docker", "rm", "-f", name).Run()
		}
		for _, id := range nodes {
			c.try("DELETE", "/api/v1/admin/nodes/"+id, nil)
		}
		var cur configState
		c.do("GET", "/api/v1/admin/config", nil, &cur, 200)
		c.do("PUT", "/api/v1/admin/config", map[string]any{"version": cur.Version, "values": map[string]any{"mesh.route_playback": nil}}, nil, 200)
	})

	c.do("POST", "/api/v1/admin/nodes", map[string]any{"name": "meta", "host": "169.254.169.254", "port": 80, "scheme": "http"}, nil, 400)
	c.do("POST", "/api/v1/admin/nodes", map[string]any{"name": "bad", "host": "vd-e2e-w1/../x", "port": 8080, "scheme": "http"}, nil, 400)

	startWorker := func(name string, priority int, publish string) string {
		var node map[string]any
		c.do("POST", "/api/v1/admin/nodes", map[string]any{
			"name": name, "host": name, "port": 8080, "scheme": "http", "role": "media-worker",
			"priority": priority, "weight": 1, "enabled": true, "status": "healthy",
		}, &node, 200)
		id := fmt.Sprint(node["id"])
		nodes = append(nodes, id)
		if node["status"] == "healthy" {
			t.Fatal("clients must not be able to mark a node healthy")
		}
		var cred map[string]any
		c.do("POST", "/api/v1/admin/nodes/"+id+"/credential", nil, &cred, 200)
		secret := fmt.Sprint(cred["secret"])
		var list struct {
			Items []map[string]any `json:"items"`
		}
		c.do("GET", "/api/v1/admin/nodes", nil, &list, 200)
		if raw := fmt.Sprint(list); strings.Contains(raw, secret) {
			t.Fatal("node secret returned by the node list")
		}
		_ = exec.Command("docker", "rm", "-f", name).Run()
		args := []string{"run", "-d", "--name", name, "--network", network,
			"-e", "VD_ROLE=worker", "-e", "VD_NODE_SECRET=" + secret,
			"-e", "VD_DATABASE_DRIVER=postgres", "-e", "VD_DATABASE_URL=" + pgURL,
			"-v", "vd-e2e-media:/media"}
		if publish != "" {
			args = append(args, "-p", publish+":8080")
		}
		docker(append(args, image)...)
		return id
	}
	w1 := startWorker("vd-e2e-w1", 10, "18091")
	w2 := startWorker("vd-e2e-w2", 5, "")

	waitHealthy := func(want map[string]bool) {
		deadline := time.Now().Add(45 * time.Second)
		for {
			var list struct {
				Items []map[string]any `json:"items"`
			}
			c.do("GET", "/api/v1/admin/nodes", nil, &list, 200)
			ok := true
			for _, n := range list.Items {
				if healthy, tracked := want[fmt.Sprint(n["id"])]; tracked && (n["status"] == "healthy") != healthy {
					ok = false
				}
			}
			if ok {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("nodes did not reach %v: %v", want, list.Items)
			}
			time.Sleep(time.Second)
		}
	}
	waitHealthy(map[string]bool{w1: true, w2: true})

	wc := newClient(t, "http://127.0.0.1:18091")
	if s, _, _ := wc.try("GET", "/healthz", nil); s != 200 {
		t.Fatalf("worker healthz %d", s)
	}
	for _, path := range []string{"/api/v1/node/status", "/api/v1/me", "/", "/hls/x/index.m3u8?stoken=x"} {
		if s, body, _ := wc.try("GET", path, nil); s != 401 && s != 404 {
			t.Fatalf("worker served unsigned %s: %d %s", path, s, truncate(body))
		}
	}

	var cur configState
	c.do("GET", "/api/v1/admin/config", nil, &cur, 200)
	c.do("PUT", "/api/v1/admin/config", map[string]any{"version": cur.Version, "values": map[string]any{"mesh.route_playback": "1"}}, nil, 200)

	var sess map[string]any
	c.do("POST", "/api/v1/playback/sessions", map[string]any{
		"item_kind": "movie", "item_id": hd, "start_ms": 60000, "quality": "480",
		"client": map[string]any{"mse": true, "viewport_w": 1280, "viewport_h": 720},
	}, &sess, 200)
	if node, _ := sess["node"].(map[string]any); node["id"] != w1 {
		t.Fatalf("primary not preferred: %v", sess["node"])
	}
	urls, _ := sess["urls"].(map[string]any)
	playlist := fmt.Sprint(urls["playlist"])
	if !strings.HasPrefix(playlist, "/mesh/"+w1+"/hls/") {
		t.Fatalf("playlist not relayed: %s", playlist)
	}
	body := waitPlaylist(t, c, playlist)
	seg := regexp.MustCompile(`(?m)^(seg[^\s]+)$`).FindStringSubmatch(body)
	if seg == nil {
		t.Fatalf("no segment in playlist: %s", truncate([]byte(body)))
	}
	segURL := playlist[:strings.LastIndex(playlist, "/")+1] + seg[1]
	if s, raw, _ := c.try("GET", segURL, nil); s != 200 || len(raw) == 0 {
		t.Fatalf("segment via relay: %d (%d bytes)", s, len(raw))
	}
	sessBase := fmt.Sprint(urls["session"])
	stoken := fmt.Sprint(sess["stoken"])
	c.do("PUT", sessBase+"/progress?stoken="+stoken, map[string]any{"position_ms": 75000, "duration_ms": 180000, "event": "pause"}, nil, 200)

	var timeline []map[string]any
	c.do("GET", "/api/v1/admin/diagnostics/flight-recorder/"+fmt.Sprint(sess["id"]), nil, &timeline, 200)
	fromWorker := 0
	for _, e := range timeline {
		if e["node"] == w1 {
			fromWorker++
		}
	}
	if fromWorker == 0 {
		t.Fatalf("worker timeline not visible on the control plane: %v", timeline)
	}
	var summaries struct {
		Items []map[string]any `json:"items"`
	}
	c.do("GET", "/api/v1/admin/resilience/sessions", nil, &summaries, 200)
	listed := false
	for _, s := range summaries.Items {
		if s["session_id"] == sess["id"] && s["node"] == w1 {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("worker session missing from the session list: %v", summaries.Items)
	}

	docker("kill", "vd-e2e-w1")
	killedAt := time.Now()
	s, raw, _ := c.try("GET", segURL, nil)
	if s != 410 || !strings.Contains(string(raw), "NODE_UNAVAILABLE") {
		t.Fatalf("lost worker not reported as gone: %d %s", s, truncate(raw))
	}
	if s, _, _ := c.try("GET", "/", nil); s != 200 {
		t.Fatalf("frontend unavailable after worker loss: %d", s)
	}
	c.do("GET", "/api/v1/system", nil, nil, 200)

	var resumed map[string]any
	c.do("POST", "/api/v1/playback/sessions", map[string]any{
		"item_kind": "movie", "item_id": hd, "start_ms": 75000, "quality": "480", "replace_session_id": sess["id"],
		"client": map[string]any{"mse": true, "viewport_w": 1280, "viewport_h": 720},
	}, &resumed, 200)
	if node, _ := resumed["node"].(map[string]any); node["id"] != w2 {
		t.Fatalf("standby not used after primary loss: %v", resumed["node"])
	}
	if from := fmt.Sprint(resumed["seekable_from_ms"]); from != "75000" {
		t.Fatalf("resumed at %s, want 75000 without rewind", from)
	}
	rurls, _ := resumed["urls"].(map[string]any)
	waitPlaylist(t, c, fmt.Sprint(rurls["playlist"]))
	t.Logf("failover to standby playable %v after the primary was killed", time.Since(killedAt).Round(time.Millisecond))

	// The control plane runs watch parties on a separate coordinator.
	coord := "vd-e2e-coord"
	coordSecret := "e2e-coordinator-secret-0123456789abcdef"
	_ = exec.Command("docker", "rm", "-f", coord).Run()
	docker("run", "-d", "--name", coord, "--network", network,
		"-e", "VD_ROLE=coordinator", "-e", "VD_COORDINATOR_SECRET="+coordSecret,
		"-e", "VD_DATABASE_DRIVER=postgres", "-e", "VD_DATABASE_URL="+pgURL,
		"-v", "vd-e2e-config:/config", image)
	ctl := "vd-e2e-ctl"
	_ = exec.Command("docker", "rm", "-f", ctl).Run()
	docker("run", "-d", "--name", ctl, "--network", network, "-p", "18082:8080",
		"-e", "VD_ROLE=control", "-e", "VD_DATABASE_DRIVER=postgres", "-e", "VD_DATABASE_URL="+pgURL,
		"-e", "VD_COORDINATOR_URL=http://"+coord+":8080", "-e", "VD_COORDINATOR_SECRET="+coordSecret,
		"-v", "vd-e2e-config:/config", "-v", "vd-e2e-media:/media", image)
	cc := newClient(t, "http://127.0.0.1:18082")
	deadline := time.Now().Add(30 * time.Second)
	for {
		if s, _, _ := cc.try("GET", "/readyz", nil); s == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control node did not become ready")
		}
		time.Sleep(500 * time.Millisecond)
	}
	bootstrap(t, cc)
	var viaControl map[string]any
	cc.do("POST", "/api/v1/playback/sessions", map[string]any{
		"item_kind": "movie", "item_id": hd, "quality": "480",
		"client": map[string]any{"mse": true, "viewport_w": 1280, "viewport_h": 720},
	}, &viaControl, 200)
	if node, _ := viaControl["node"].(map[string]any); node["id"] != w2 {
		t.Fatalf("control role did not place on a worker: %v", viaControl["node"])
	}
	coordinatedParty(t, cc, coord, hd)

	docker("kill", "vd-e2e-w2")
	waitHealthy(map[string]bool{w1: false, w2: false})
	var none map[string]any
	cc.do("POST", "/api/v1/playback/sessions", map[string]any{"item_kind": "movie", "item_id": hd, "quality": "480",
		"client": map[string]any{"mse": true}}, &none, 503)
	if none["code"] != "no_worker_available" {
		t.Fatalf("no workers: %v", none)
	}
	if s, _, _ := cc.try("GET", "/", nil); s != 200 {
		t.Fatalf("control frontend unavailable with every worker down: %d", s)
	}

	docker("start", "vd-e2e-w1")
	waitHealthy(map[string]bool{w1: true})
	var back map[string]any
	c.do("POST", "/api/v1/playback/sessions", map[string]any{"item_kind": "movie", "item_id": hd, "quality": "480",
		"client": map[string]any{"mse": true, "viewport_w": 1280, "viewport_h": 720}}, &back, 200)
	if node, _ := back["node"].(map[string]any); node["id"] != w1 {
		t.Fatalf("recovered primary not preferred again: %v", back["node"])
	}

	// An independently hosted frontend keeps serving the app shell while the
	// control plane behind it is down, and recovers with it.
	fe := "vd-e2e-fe"
	_ = exec.Command("docker", "rm", "-f", fe).Run()
	docker("run", "-d", "--name", fe, "--network", network, "-p", "18083:8080",
		"-e", "VD_ROLE=frontend", "-e", "VD_CONTROL_URL=http://"+ctl+":8080", image)
	fc := newClient(t, "http://127.0.0.1:18083")
	deadline = time.Now().Add(30 * time.Second)
	for {
		if s, _, _ := fc.try("GET", "/healthz", nil); s == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("frontend did not start")
		}
		time.Sleep(500 * time.Millisecond)
	}
	bootstrap(t, fc)
	fc.do("GET", "/api/v1/me", nil, nil, 200)
	// The control plane runs its own health monitor, which may have probed
	// the restarted primary before it was listening; wait for it to agree.
	var viaFrontend map[string]any
	deadline = time.Now().Add(45 * time.Second)
	for {
		s, raw, _ := fc.try("POST", "/api/v1/playback/sessions", map[string]any{"item_kind": "movie", "item_id": hd, "quality": "480",
			"client": map[string]any{"mse": true, "viewport_w": 1280, "viewport_h": 720}})
		if s == 200 {
			if err := json.Unmarshal(raw, &viaFrontend); err != nil {
				t.Fatalf("session via frontend: %v", err)
			}
			break
		}
		if s != 503 || time.Now().After(deadline) {
			t.Fatalf("session via frontend: %d %s", s, truncate(raw))
		}
		time.Sleep(time.Second)
	}
	furls, _ := viaFrontend["urls"].(map[string]any)
	waitPlaylist(t, fc, fmt.Sprint(furls["playlist"]))

	docker("stop", "-t", "2", ctl)
	if s, raw, _ := fc.try("GET", "/", nil); s != 200 || !strings.Contains(string(raw), "<html") {
		t.Fatalf("frontend shell unavailable with the control plane down: %d", s)
	}
	if s, raw, _ := fc.try("GET", "/sw.js", nil); s != 200 || len(raw) == 0 {
		t.Fatalf("service worker unavailable with the control plane down: %d", s)
	}
	var down map[string]any
	fc.do("GET", "/api/v1/system", nil, &down, 502)
	if down["code"] != "control_unavailable" {
		t.Fatalf("control outage through the frontend: %v", down)
	}
	docker("start", ctl)
	deadline = time.Now().Add(30 * time.Second)
	for {
		if s, _, _ := fc.try("GET", "/api/v1/me", nil); s == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("frontend did not recover with the control plane")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// coordinatedParty covers a watch party hosted by a separate coordinator
// container behind the control plane cc: rooms and their WebSocket are
// relayed, the dashboard reads the coordinator, an outage fails closed
// without affecting the control plane, and rooms survive a coordinator
// restart.
func coordinatedParty(t *testing.T, cc *client, coord, itemID string) {
	t.Helper()
	body := map[string]any{"item_kind": "movie", "item_id": itemID}
	var room map[string]any
	deadline := time.Now().Add(30 * time.Second)
	for {
		s, raw, _ := cc.try("POST", "/api/v1/watch-together/rooms", body)
		if s == 200 {
			if err := json.Unmarshal(raw, &room); err != nil {
				t.Fatal(err)
			}
			break
		}
		if s != 503 || time.Now().After(deadline) {
			t.Fatalf("room via coordinator: %d %s", s, truncate(raw))
		}
		time.Sleep(time.Second)
	}
	roomID := fmt.Sprint(room["room_id"])
	pc := dialParty(t, cc, "coordinated host", roomID)
	if st := pc.wait(10*time.Second, func(m map[string]any) bool { return m["type"] == "state" }); st == nil || st["room_id"] != roomID {
		t.Fatalf("no room state relayed from the coordinator: %v", st)
	}
	coordinator := func() map[string]any {
		var dash map[string]any
		cc.do("GET", "/api/v1/admin/resilience", nil, &dash, 200)
		sec, _ := dash["coordinator"].(map[string]any)
		return sec
	}
	if sec := coordinator(); sec["status"] != "ok" || num(sec["data"].(map[string]any)["rooms"]) < 1 {
		t.Fatalf("coordinator section: %v", sec)
	}

	out, err := exec.Command("docker", "stop", "-t", "2", coord).CombinedOutput()
	if err != nil {
		t.Fatalf("docker stop %s: %v %s", coord, err, out)
	}
	closed := time.After(10 * time.Second)
	for open := true; open; {
		select {
		case _, ok := <-pc.msgs:
			open = ok
		case <-closed:
			t.Fatal("party socket stayed open without a coordinator")
		}
	}
	var gone map[string]any
	cc.do("POST", "/api/v1/watch-together/rooms", body, &gone, 503)
	if gone["code"] != "coordinator_unavailable" {
		t.Fatalf("coordinator outage: %v", gone)
	}
	cc.do("GET", "/api/v1/system", nil, nil, 200)
	deadline = time.Now().Add(30 * time.Second)
	for coordinator()["status"] != "down" {
		if time.Now().After(deadline) {
			t.Fatalf("dashboard never reported the coordinator down: %v", coordinator())
		}
		time.Sleep(time.Second)
	}

	if out, err := exec.Command("docker", "start", coord).CombinedOutput(); err != nil {
		t.Fatalf("docker start %s: %v %s", coord, err, out)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		s, raw, _ := cc.try("POST", "/api/v1/watch-together/rooms/"+roomID+"/ticket", map[string]any{})
		if s == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("room not restored after the coordinator restarted: %d %s", s, truncate(raw))
		}
		time.Sleep(time.Second)
	}
	back := dialParty(t, cc, "rejoined host", roomID)
	if st := back.wait(10*time.Second, func(m map[string]any) bool { return m["type"] == "state" }); st == nil || st["room_id"] != roomID {
		t.Fatalf("no state after rejoining: %v", st)
	}
	t.Logf("room %s survived a coordinator restart behind the control plane", roomID)
}
