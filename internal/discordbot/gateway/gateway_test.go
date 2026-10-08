package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type frame struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

type fakeConn struct {
	path   string
	ws     *websocket.Conn
	mu     sync.Mutex
	in     chan frame
	closed chan int
	noAck  *atomic.Bool
}

func (c *fakeConn) send(t *testing.T, v any) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ws.WriteJSON(v); err != nil {
		t.Errorf("fake gateway write: %v", err)
	}
}

func (c *fakeConn) dispatch(t *testing.T, name string, seq int64, d any) {
	c.send(t, map[string]any{"op": 0, "t": name, "s": seq, "d": d})
}

// expect returns the next frame with op, skipping heartbeats unless op is a heartbeat.
func (c *fakeConn) expect(t *testing.T, op int) frame {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case f := <-c.in:
			if f.Op == op {
				return f
			}
			if f.Op != opHeartbeat {
				t.Fatalf("expected op %d, got op %d (%s)", op, f.Op, f.D)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for op %d on %s", op, c.path)
		}
	}
}

func (c *fakeConn) closeCode(t *testing.T) int {
	t.Helper()
	select {
	case code := <-c.closed:
		return code
	case <-time.After(3 * time.Second):
		t.Fatal("client did not close the connection")
		return 0
	}
}

type fakeGateway struct {
	srv   *httptest.Server
	conns chan *fakeConn
	noAck atomic.Bool
	count atomic.Int32
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	g := &fakeGateway{conns: make(chan *fakeConn, 8)}
	up := websocket.Upgrader{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		g.count.Add(1)
		c := &fakeConn{path: r.URL.Path, ws: ws, in: make(chan frame, 64), closed: make(chan int, 1), noAck: &g.noAck}
		c.mu.Lock()
		_ = ws.WriteJSON(map[string]any{"op": opHello, "d": map[string]any{"heartbeat_interval": 60}})
		c.mu.Unlock()
		g.conns <- c
		for {
			var f frame
			if err := ws.ReadJSON(&f); err != nil {
				var ce *websocket.CloseError
				if errors.As(err, &ce) {
					c.closed <- ce.Code
				} else {
					c.closed <- -1
				}
				return
			}
			if f.Op == opHeartbeat && !c.noAck.Load() {
				c.mu.Lock()
				_ = ws.WriteJSON(map[string]any{"op": opHeartbeatAck})
				c.mu.Unlock()
			}
			c.in <- f
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGateway) url() string {
	return "ws" + strings.TrimPrefix(g.srv.URL, "http") + "/?v=10&encoding=json"
}

func (g *fakeGateway) next(t *testing.T) *fakeConn {
	t.Helper()
	select {
	case c := <-g.conns:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("no gateway connection")
		return nil
	}
}

func (g *fakeGateway) none(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case c := <-g.conns:
		t.Fatalf("unexpected gateway connection to %s", c.path)
	case <-time.After(d):
	}
}

type tokenBox struct {
	mu sync.Mutex
	v  string
}

func (b *tokenBox) get() string { b.mu.Lock(); defer b.mu.Unlock(); return b.v }
func (b *tokenBox) set(v string) {
	b.mu.Lock()
	b.v = v
	b.mu.Unlock()
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newManager(t *testing.T, g *fakeGateway, token string) (*Manager, *tokenBox, *syncBuffer) {
	t.Helper()
	box := &tokenBox{v: token}
	logs := &syncBuffer{}
	m := New(box.get, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	m.URL = g.url()
	m.backoff = func(int) time.Duration { return 10 * time.Millisecond }
	m.invalidWait = func() time.Duration { return 10 * time.Millisecond }
	t.Cleanup(m.Stop)
	return m, box, logs
}

func waitState(t *testing.T, m *Manager, state string) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s := m.Status(); s.State == state {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state %q never reached, last %#v", state, m.Status())
	return Status{}
}

func ready(t *testing.T, g *fakeGateway, c *fakeConn, seq int64) {
	t.Helper()
	c.dispatch(t, "READY", seq, map[string]any{
		"session_id":         "session-1",
		"resume_gateway_url": "ws" + strings.TrimPrefix(g.srv.URL, "http") + "/resume",
		"user":               map[string]any{"id": "400000000000000001", "username": "ViewDock"},
	})
}

func TestIdentifyPresenceHeartbeatAndStop(t *testing.T) {
	g := newFakeGateway(t)
	m, _, logs := newManager(t, g, "token-alpha-secret")
	m.Start(context.Background())
	c := g.next(t)

	var id struct {
		Token      string            `json:"token"`
		Intents    *int              `json:"intents"`
		Properties map[string]string `json:"properties"`
		Presence   presence          `json:"presence"`
	}
	if err := json.Unmarshal(c.expect(t, opIdentify).D, &id); err != nil {
		t.Fatal(err)
	}
	if id.Token != "token-alpha-secret" || id.Intents == nil || *id.Intents != 0 {
		t.Fatalf("identify must send the token and no intents: %#v", id)
	}
	if id.Presence.Status != "online" || len(id.Presence.Activities) != 1 ||
		id.Presence.Activities[0].Name != "movies & TV" || id.Presence.Activities[0].Type != activityWatching {
		t.Fatalf("presence %#v", id.Presence)
	}

	ready(t, g, c, 7)
	s := waitState(t, m, StateOnline)
	if s.Presence != "online" || s.Activity != "Watching movies & TV" || s.BotName != "ViewDock" || s.LastConnectedAt == nil {
		t.Fatalf("status %#v", s)
	}
	if beat := c.expect(t, opHeartbeat); string(beat.D) != "7" {
		t.Fatalf("heartbeat must carry the last sequence, got %s", beat.D)
	}

	m.Stop()
	if code := c.closeCode(t); code != websocket.CloseNormalClosure {
		t.Fatalf("shutdown must close with 1000 so the bot goes offline, got %d", code)
	}
	if s := m.Status(); s.State != StateStopped || s.Presence != "offline" {
		t.Fatalf("after stop %#v", s)
	}
	raw, _ := json.Marshal(m.Status())
	if strings.Contains(string(raw), "token-alpha-secret") || strings.Contains(logs.String(), "token-alpha-secret") {
		t.Fatal("the bot token must never appear in status or logs")
	}
}

func TestReconnectRequestResumesSession(t *testing.T) {
	g := newFakeGateway(t)
	m, _, _ := newManager(t, g, "token-alpha")
	m.Start(context.Background())
	c := g.next(t)
	c.expect(t, opIdentify)
	ready(t, g, c, 5)
	waitState(t, m, StateOnline)

	c.send(t, map[string]any{"op": opReconnect})
	if code := c.closeCode(t); code == websocket.CloseNormalClosure || code == websocket.CloseGoingAway {
		t.Fatalf("a reconnect must keep the session resumable, closed with %d", code)
	}
	r := g.next(t)
	if r.path != "/resume" {
		t.Fatalf("resume must use resume_gateway_url, got %s", r.path)
	}
	var res struct {
		Token     string `json:"token"`
		SessionID string `json:"session_id"`
		Seq       int64  `json:"seq"`
	}
	if err := json.Unmarshal(r.expect(t, opResume).D, &res); err != nil {
		t.Fatal(err)
	}
	if res.Token != "token-alpha" || res.SessionID != "session-1" || res.Seq != 5 {
		t.Fatalf("resume %#v", res)
	}
	r.dispatch(t, "RESUMED", 6, nil)
	if s := waitState(t, m, StateOnline); s.BotName != "ViewDock" {
		t.Fatalf("bot identity must survive a resume: %#v", s)
	}
}

func TestNetworkDropResumes(t *testing.T) {
	g := newFakeGateway(t)
	m, _, _ := newManager(t, g, "token-alpha")
	m.Start(context.Background())
	c := g.next(t)
	c.expect(t, opIdentify)
	ready(t, g, c, 3)
	waitState(t, m, StateOnline)

	_ = c.ws.UnderlyingConn().Close()
	r := g.next(t)
	r.expect(t, opResume)
	if s := m.Status(); s.LastError == "" && s.State != StateOnline {
		t.Fatalf("a lost connection should be reported, got %#v", s)
	}
}

func TestInvalidSessionIdentifiesAgain(t *testing.T) {
	g := newFakeGateway(t)
	m, _, _ := newManager(t, g, "token-alpha")
	m.Start(context.Background())
	c := g.next(t)
	c.expect(t, opIdentify)
	ready(t, g, c, 2)
	waitState(t, m, StateOnline)

	c.send(t, map[string]any{"op": opInvalidSession, "d": false})
	r := g.next(t)
	if r.path == "/resume" {
		t.Fatal("a non-resumable session must start from the entry point")
	}
	r.expect(t, opIdentify)
}

func TestZombieConnectionIsReplaced(t *testing.T) {
	g := newFakeGateway(t)
	m, _, _ := newManager(t, g, "token-alpha")
	m.Start(context.Background())
	c := g.next(t)
	c.expect(t, opIdentify)
	ready(t, g, c, 4)
	waitState(t, m, StateOnline)

	g.noAck.Store(true)
	if code := c.closeCode(t); code == websocket.CloseNormalClosure {
		t.Fatal("a zombie connection must be closed resumably")
	}
	g.noAck.Store(false)
	g.next(t).expect(t, opResume)
}

func TestRejectedTokenWaitsForNewToken(t *testing.T) {
	g := newFakeGateway(t)
	m, box, logs := newManager(t, g, "token-revoked-secret")
	m.Start(context.Background())
	c := g.next(t)
	c.expect(t, opIdentify)
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4004, "Authentication failed."), time.Now().Add(time.Second))

	s := waitState(t, m, StateFailed)
	if !s.TokenRejected || s.CloseCode != 4004 || !strings.Contains(s.LastError, "rejected the bot token") {
		t.Fatalf("status %#v", s)
	}
	g.none(t, 200*time.Millisecond)

	box.set("token-new")
	m.Reload()
	r := g.next(t)
	var id struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(r.expect(t, opIdentify).D, &id)
	if id.Token != "token-new" {
		t.Fatalf("identify after a token change used %q", id.Token)
	}
	if strings.Contains(logs.String(), "token-revoked-secret") {
		t.Fatal("the bot token must never appear in logs")
	}
}

func TestDisabledWithoutTokenUntilConfigured(t *testing.T) {
	g := newFakeGateway(t)
	m, box, _ := newManager(t, g, "")
	m.Start(context.Background())
	waitState(t, m, StateDisabled)
	g.none(t, 150*time.Millisecond)

	box.set("token-alpha")
	m.Reload()
	g.next(t).expect(t, opIdentify)
}

func TestReloadRestartsOnlyWhenTokenChanges(t *testing.T) {
	g := newFakeGateway(t)
	m, box, _ := newManager(t, g, "token-alpha")
	m.Start(context.Background())
	c := g.next(t)
	c.expect(t, opIdentify)
	ready(t, g, c, 1)
	waitState(t, m, StateOnline)

	m.Reload()
	g.none(t, 200*time.Millisecond)
	if s := m.Status(); s.State != StateOnline {
		t.Fatalf("an unchanged token must keep the session, got %#v", s)
	}

	box.set("token-beta")
	m.Reload()
	if code := c.closeCode(t); code != websocket.CloseNormalClosure {
		t.Fatalf("the old session must be ended with 1000, got %d", code)
	}
	r := g.next(t)
	var id struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(r.expect(t, opIdentify).D, &id)
	if id.Token != "token-beta" {
		t.Fatalf("new session identified with %q", id.Token)
	}
	if n := g.count.Load(); n != 2 {
		t.Fatalf("expected exactly 2 connections, got %d", n)
	}

	box.set("")
	m.Reload()
	r.closeCode(t)
	waitState(t, m, StateDisabled)
}

func TestUnreachableGatewayBacksOff(t *testing.T) {
	m := New(func() string { return "token-alpha" }, slog.New(slog.NewTextHandler(&syncBuffer{}, nil)))
	m.URL = "ws://127.0.0.1:1/?v=10&encoding=json"
	var attempts atomic.Int32
	m.backoff = func(n int) time.Duration {
		attempts.Store(int32(n))
		return 20 * time.Millisecond
	}
	m.Start(context.Background())
	defer m.Stop()
	s := waitState(t, m, StateReconnecting)
	if !strings.Contains(s.LastError, "Could not reach the Discord Gateway") || s.NextRetryAt == nil {
		t.Fatalf("status %#v", s)
	}
	deadline := time.Now().Add(2 * time.Second)
	for attempts.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if attempts.Load() < 3 {
		t.Fatal("reconnect attempts must continue with growing attempt numbers")
	}
}

func TestResumeURLKeepsScheme(t *testing.T) {
	m := New(nil, nil)
	sess := &session{id: "s", resumeURL: "ws://evil.example"}
	if got := m.endpoint(sess); got != DefaultURL {
		t.Fatalf("a resume URL must not downgrade from wss, got %s", got)
	}
	sess.resumeURL = "wss://gateway-us-east1-b.discord.gg"
	if got := m.endpoint(sess); got != "wss://gateway-us-east1-b.discord.gg/?encoding=json&v=10" {
		t.Fatalf("resume URL %s", got)
	}
}

func TestDefaultBackoffIsCapped(t *testing.T) {
	if d := defaultBackoff(1); d < time.Second || d > 1200*time.Millisecond {
		t.Fatalf("first retry %s", d)
	}
	if d := defaultBackoff(30); d < maxBackoff || d > maxBackoff+maxBackoff/5 {
		t.Fatalf("capped retry %s", d)
	}
}
