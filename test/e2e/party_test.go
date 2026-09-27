//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type partyConn struct {
	t      *testing.T
	name   string
	ws     *websocket.Conn
	mu     sync.Mutex
	msgs   chan map[string]any
	offset int64
}

func dialParty(t *testing.T, c *client, name, roomID string) *partyConn {
	t.Helper()
	var ticket map[string]any
	c.do("POST", "/api/v1/watch-together/rooms/"+roomID+"/ticket", map[string]any{}, &ticket, 200)
	path, _ := ticket["ws_url"].(string)
	if path == "" || ticket["member_id"] == "" {
		t.Fatalf("%s: ticket without ws_url or member_id: %v", name, ticket)
	}
	base, _ := url.Parse(c.base)
	wsURL := "ws://" + base.Host + path
	hdr := http.Header{}
	hdr.Set("Origin", base.Scheme+"://"+base.Host)
	for _, ck := range c.http.Jar.Cookies(base) {
		hdr.Add("Cookie", ck.Name+"="+ck.Value)
	}
	ws, res, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Fatalf("%s: dial %s: %v (%d)", name, path, err, status)
	}
	p := &partyConn{t: t, name: name, ws: ws, msgs: make(chan map[string]any, 256)}
	go func() {
		defer close(p.msgs)
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(data, &m) == nil {
				p.msgs <- m
			}
		}
	}()
	t.Cleanup(func() { _ = ws.Close() })
	return p
}

func (p *partyConn) send(m map[string]any) {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ws.WriteJSON(m); err != nil {
		p.t.Fatalf("%s: send %v: %v", p.name, m["type"], err)
	}
}

// wait returns the first message matching pred, discarding others.
func (p *partyConn) wait(timeout time.Duration, pred func(map[string]any) bool) map[string]any {
	p.t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m, ok := <-p.msgs:
			if !ok {
				p.t.Fatalf("%s: connection closed", p.name)
			}
			if pred(m) {
				return m
			}
		case <-deadline:
			return nil
		}
	}
}

func (p *partyConn) drain() {
	for {
		select {
		case <-p.msgs:
		default:
			return
		}
	}
}

// syncClock estimates the server clock offset from the lowest-RTT ping.
func (p *partyConn) syncClock() {
	p.t.Helper()
	bestRTT := int64(1 << 62)
	for i := 0; i < 5; i++ {
		t0 := time.Now().UnixMilli()
		p.send(map[string]any{"type": "ping", "t0": t0})
		m := p.wait(3*time.Second, func(m map[string]any) bool { return m["type"] == "pong" && int64(num(m["t0"])) == t0 })
		if m == nil {
			p.t.Fatalf("%s: no pong", p.name)
		}
		t1 := time.Now().UnixMilli()
		if rtt := t1 - t0; rtt < bestRTT {
			bestRTT = rtt
			p.offset = int64(num(m["server_ms"])) - (t0+t1)/2
		}
	}
}

func (p *partyConn) serverNow() int64 { return time.Now().UnixMilli() + p.offset }

type timeline struct {
	pos, at int64
	playing bool
}

func (tl timeline) at2(serverNow int64) int64 {
	if !tl.playing {
		return tl.pos
	}
	return tl.pos + serverNow - tl.at
}

func stateTimeline(m map[string]any) timeline {
	return timeline{pos: int64(num(m["position_ms"])), at: int64(num(m["server_ms"])), playing: m["playing"] == true}
}

// report sends a position driftMS away from the timeline, stamped on the
// server clock.
func (p *partyConn) report(tl timeline, driftMS int64, buffering bool) {
	now := p.serverNow()
	p.send(map[string]any{
		"type": "position", "position_ms": tl.at2(now) + driftMS, "playing": true,
		"buffering": buffering, "at_server_ms": now,
	})
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func isSync(m map[string]any) bool { return m["type"] == "sync" }

func partyUser(t *testing.T, admin *client, base, username string) *client {
	t.Helper()
	status, raw, _ := admin.try("POST", "/api/v1/users", map[string]any{"username": username, "password": "party-password-123", "display_name": username})
	if status != 201 && status != 409 {
		t.Fatalf("create %s: %d %s", username, status, truncate(raw))
	}
	c := newClient(t, base)
	c.ensureCSRF()
	c.do("POST", "/api/v1/auth/login", map[string]string{"username": username, "password": "party-password-123"}, nil, 200)
	c.csrf = ""
	c.ensureCSRF()
	return c
}

func TestPartySync(t *testing.T) {
	base := env(t, "VD_E2E_URL", "")
	admin := newClient(t, base)
	bootstrap(t, admin)
	movies := scanAndWait(t, admin, 1)
	movieID := fmt.Sprint(movies[0]["id"])

	var cfg configState
	admin.do("GET", "/api/v1/admin/config", nil, &cfg)
	if settingValue(cfg, "features.watch_together") == "0" {
		t.Skip("watch parties disabled on this deployment")
	}

	var room map[string]any
	admin.do("POST", "/api/v1/watch-together/rooms", map[string]any{"item_kind": "movie", "item_id": movieID}, &room, 200)
	roomID, code := fmt.Sprint(room["room_id"]), fmt.Sprint(room["invite_code"])

	u1 := partyUser(t, admin, base, "e2e-party-1")
	u2 := partyUser(t, admin, base, "e2e-party-2")
	var inv map[string]any
	u1.do("GET", "/api/v1/watch-together/invites/"+code, nil, &inv, 200)
	if inv["item_id"] != movieID || inv["room_id"] != roomID {
		t.Fatalf("invite for a signed-in user lacks the room details: %v", inv)
	}
	anon := newClient(t, base)
	var anonInv map[string]any
	anon.do("GET", "/api/v1/watch-together/invites/"+code, nil, &anonInv, 200)
	if _, leaked := anonInv["item_id"]; leaked {
		t.Fatalf("anonymous invite lookup exposed room details: %v", anonInv)
	}
	u1.do("POST", "/api/v1/watch-together/join", map[string]any{"code": code}, nil, 200)
	u2.do("POST", "/api/v1/watch-together/join", map[string]any{"invite_code": code}, nil, 200)

	host := dialParty(t, admin, "host", roomID)
	m1 := dialParty(t, u1, "m1", roomID)
	m2 := dialParty(t, u2, "m2", roomID)
	all := []*partyConn{host, m1, m2}
	for _, p := range all {
		if p.wait(5*time.Second, func(m map[string]any) bool { return m["type"] == "state" }) == nil {
			t.Fatalf("%s: no initial state", p.name)
		}
		p.syncClock()
		p.send(map[string]any{"type": "ready"})
	}
	t.Logf("clock offsets host=%d m1=%d m2=%d ms", host.offset, m1.offset, m2.offset)

	host.send(map[string]any{"type": "play", "position_ms": 30_000})
	st := m1.wait(3*time.Second, func(m map[string]any) bool { return m["type"] == "state" && m["reason"] == "play" })
	if st == nil || st["playing"] != true {
		t.Fatalf("host play not broadcast: %v", st)
	}
	tl := stateTimeline(st)

	m1.drain()
	m1.send(map[string]any{"type": "pause", "position_ms": 1000})
	if reply := m1.wait(2*time.Second, func(m map[string]any) bool { return m["type"] == "state" }); reply == nil || reply["playing"] != true {
		t.Fatalf("non-host pause changed the room: %v", reply)
	}

	m1.drain()
	m1.report(tl, 0, false)
	if ok := m1.wait(2*time.Second, isSync); ok == nil || ok["action"] != "ok" || abs(int64(num(ok["drift_ms"]))) > 150 {
		t.Fatalf("in-sync report: %v", ok)
	}
	m1.report(tl, 500, false)
	rate := m1.wait(2*time.Second, isSync)
	if rate == nil || rate["action"] != "rate" || num(rate["rate"]) >= 1 || num(rate["rate"]) < 0.95 {
		t.Fatalf("500 ms ahead should slow down gently: %v", rate)
	}
	if d := int64(num(rate["drift_ms"])); d < 350 || d > 650 {
		t.Fatalf("measured drift %d ms, want about 500", d)
	}
	t.Logf("500 ms drift measured as %d ms, corrected with rate %.3f", int64(num(rate["drift_ms"])), num(rate["rate"]))
	m1.report(tl, 2500, false)
	seek := m1.wait(2*time.Second, isSync)
	if seek == nil || seek["action"] != "seek" {
		t.Fatalf("2.5 s ahead should seek: %v", seek)
	}
	target := timeline{pos: int64(num(seek["target_ms"])), at: int64(num(seek["server_ms"])), playing: true}
	if diff := abs(target.at2(m1.serverNow()) - tl.at2(m1.serverNow())); diff > 150 {
		t.Fatalf("seek target off the room timeline by %d ms", diff)
	}

	m2.drain()
	m2.report(tl, -6000, true)
	if got := m2.wait(1500*time.Millisecond, isSync); got != nil {
		t.Fatalf("buffering member was corrected: %v", got)
	}
	pres := host.wait(4*time.Second, func(m map[string]any) bool {
		if m["type"] != "presence" {
			return false
		}
		for _, raw := range m["members"].([]any) {
			mm := raw.(map[string]any)
			if mm["display_name"] == "e2e-party-2" {
				return mm["buffering"] == true
			}
		}
		return false
	})
	if pres == nil {
		t.Fatal("presence never showed the buffering member")
	}
	for _, raw := range pres["members"].([]any) {
		mm := raw.(map[string]any)
		if mm["display_name"] == "e2e-party-2" && mm["eligible"] == true {
			t.Fatal("buffering member counted toward the quorum")
		}
	}

	// Wait out the host override window, then keep two of three viewers
	// 800 ms ahead of the timeline until the coordinator realigns.
	time.Sleep(5 * time.Second)
	for _, p := range all {
		p.drain()
	}
	var realigned map[string]any
	deadline := time.Now().Add(12 * time.Second)
	for realigned == nil && time.Now().Before(deadline) {
		host.report(tl, 800, false)
		m1.report(tl, 800, false)
		m2.report(tl, 0, false)
		realigned = m2.wait(500*time.Millisecond, func(m map[string]any) bool { return m["type"] == "state" && m["reason"] == "majority" })
	}
	if realigned == nil {
		t.Fatal("room never aligned to the stable majority")
	}
	newTL := stateTimeline(realigned)
	if shift := newTL.at2(m2.serverNow()) - tl.at2(m2.serverNow()); shift < 600 || shift > 1000 {
		t.Fatalf("timeline moved by %d ms, want about 800", shift)
	}
	m2.drain()
	m2.report(tl, 0, false)
	behind := m2.wait(2*time.Second, isSync)
	if behind == nil || behind["action"] != "rate" || num(behind["rate"]) <= 1 {
		t.Fatalf("minority behind the majority should speed up: %v", behind)
	}
	t.Logf("majority realigned the room; minority drift %d ms corrected with rate %.3f", int64(num(behind["drift_ms"])), num(behind["rate"]))

	var stats map[string]any
	state := host.wait(4*time.Second, func(m map[string]any) bool {
		if m["type"] != "presence" {
			return false
		}
		stats, _ = m["sync"].(map[string]any)["stats"].(map[string]any)
		return num(stats["realigns"]) >= 1
	})
	if state == nil {
		t.Fatalf("no presence recorded the realign: last stats %v", stats)
	}
	if num(stats["rate_corrections"]) < 1 || num(stats["seeks"]) < 1 || num(stats["realigns"]) < 1 {
		t.Fatalf("sync stats did not record corrections: %v", stats)
	}
	if !strings.Contains(fmt.Sprint(state["sync"]), "hard_ms") {
		t.Fatalf("sync info missing thresholds: %v", state["sync"])
	}
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
