package mesh

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/backend"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/nodeauth"
	"github.com/viewdock/viewdock/internal/secrets"
)

func testRouter(t *testing.T) (*backend.Store, *backend.Router) {
	t.Helper()
	path := t.TempDir() + "/mesh.db"
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	c, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := backend.New(sqlDB)
	store.Cipher = c
	return store, backend.NewRouter(store)
}

func addNode(t *testing.T, store *backend.Store, id, rawURL string, priority int) string {
	t.Helper()
	u, _ := url.Parse(rawURL)
	host, port, _ := strings.Cut(u.Host, ":")
	p, _ := strconv.Atoi(port)
	if _, err := store.Upsert(context.Background(), backend.Node{ID: id, Name: id, Host: host, Port: p, Scheme: "http",
		Role: backend.RoleMediaWorker, Priority: priority, Weight: 1, Enabled: true, Status: "healthy"}); err != nil {
		t.Fatal(err)
	}
	secret, _ := nodeauth.NewSecret()
	if err := store.SetSecret(context.Background(), id, secret); err != nil {
		t.Fatal(err)
	}
	return secret
}

// fakeWorker verifies node signatures the way a real worker does.
func fakeWorker(t *testing.T, secret *string, handle func(w http.ResponseWriter, r *http.Request, body []byte)) *httptest.Server {
	t.Helper()
	var v *nodeauth.Verifier
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			v = nodeauth.NewVerifier(*secret)
		}
		body, _ := io.ReadAll(r.Body)
		if err := v.Verify(r, body); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		handle(w, r, body)
	}))
}

func TestRewriteSession(t *testing.T) {
	raw := []byte(`{"id":"s1","stoken":"tok","urls":{"file":"/api/v1/playback/sessions/s1/file","playlist":"/hls/s1/index.m3u8?stoken=tok"}}`)
	out, sid, err := rewriteSession(raw, backend.Node{ID: "n1", Name: "Worker 1"})
	if err != nil || sid != "s1" {
		t.Fatal(sid, err)
	}
	urls := out["urls"].(map[string]any)
	if urls["file"] != "/mesh/n1/api/v1/playback/sessions/s1/file?stoken=tok" {
		t.Fatalf("file url %v", urls["file"])
	}
	if urls["playlist"] != "/mesh/n1/hls/s1/index.m3u8?stoken=tok" || urls["session"] != "/mesh/n1/api/v1/playback/sessions/s1" {
		t.Fatalf("urls %v", urls)
	}
	if _, _, err := rewriteSession([]byte(`{"id":"s1"}`), backend.Node{ID: "n1"}); err == nil {
		t.Fatal("session without a token accepted")
	}
}

func TestRelayForwardsSignedWithoutCredentials(t *testing.T) {
	store, router := testRouter(t)
	var secret string
	worker := fakeWorker(t, &secret, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("credentials forwarded to worker: %v", r.Header)
		}
		if r.URL.Path != "/api/v1/playback/sessions/s1/progress" || r.URL.Query().Get("stoken") != "tok" {
			t.Errorf("unexpected upstream %s", r.URL.String())
		}
		http.SetCookie(w, &http.Cookie{Name: "leak", Value: "1"})
		_, _ = w.Write(append([]byte("echo:"), body...))
	})
	defer worker.Close()
	secret = addNode(t, store, "n1", worker.URL, 10)
	d := NewDispatcher(router, config.Config{}, nil)
	h := d.Relay()

	req := httptest.NewRequest(http.MethodPut, "/n1/api/v1/playback/sessions/s1/progress?stoken=tok", strings.NewReader(`{"position_ms":5}`))
	req.Header.Set("Cookie", "vd_session=secret")
	req.Header.Set("Authorization", "Bearer vd_x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != `echo:{"position_ms":5}` {
		t.Fatalf("relay %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("worker cookie passed to the client")
	}

	for _, path := range []string{
		"/n1/api/v1/playback/sessions/s1/progress",
		"/n1/api/v1/admin/nodes?stoken=tok",
		"/n1/api/v1/node/playback/sessions?stoken=tok",
		"/n1/hls/s1/../../etc/passwd?stoken=tok",
		"/unknown/hls/s1/index.m3u8?stoken=tok",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusGone {
			t.Errorf("%s relayed with %d", path, rec.Code)
		}
	}
}

func TestRelayPostOnlyForTelemetry(t *testing.T) {
	store, router := testRouter(t)
	var secret string
	worker := fakeWorker(t, &secret, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/playback/sessions/s1/telemetry" {
			t.Errorf("unexpected upstream %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(body)
	})
	defer worker.Close()
	secret = addNode(t, store, "n1", worker.URL, 10)
	h := NewDispatcher(router, config.Config{}, nil).Relay()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/n1/api/v1/playback/sessions/s1/telemetry?stoken=tok", strings.NewReader(`{"events":[]}`)))
	if rec.Code != http.StatusAccepted || rec.Body.String() != `{"events":[]}` {
		t.Fatalf("telemetry relay %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/n1/api/v1/playback/sessions/s1/progress?stoken=tok", strings.NewReader(`{}`)))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST to progress relayed with %d", rec.Code)
	}
}

func TestRelayDeadWorkerIsGone(t *testing.T) {
	store, router := testRouter(t)
	var secret string
	worker := fakeWorker(t, &secret, func(w http.ResponseWriter, r *http.Request, body []byte) {})
	secret = addNode(t, store, "n1", worker.URL, 10)
	worker.Close()
	d := NewDispatcher(router, config.Config{}, nil)
	rec := httptest.NewRecorder()
	d.Relay().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/n1/hls/s1/seg3.m4s?stoken=tok", nil))
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "NODE_UNAVAILABLE") {
		t.Fatalf("dead worker: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := router.Select(context.Background(), backend.RouteRequest{Roles: playbackRoles}); err != backend.ErrNoHealthyNode {
		t.Fatalf("dead worker still routable: %v", err)
	}
	n, _ := store.Get(context.Background(), "n1")
	if n.Failures == 0 || n.LastError == "" {
		t.Fatalf("failure not recorded: %+v", n)
	}
}

func TestCreateSessionFailsOverToStandby(t *testing.T) {
	store, router := testRouter(t)
	var primarySecret, standbySecret string
	primary := fakeWorker(t, &primarySecret, func(w http.ResponseWriter, r *http.Request, body []byte) {})
	standby := fakeWorker(t, &standbySecret, func(w http.ResponseWriter, r *http.Request, body []byte) {
		var a Assertion
		if err := json.Unmarshal(body, &a); err != nil || a.Principal.UserID != "u1" || !a.PartyAccess {
			t.Errorf("assertion %s", body)
		}
		_, _ = w.Write([]byte(`{"id":"s9","stoken":"tok","urls":{"playlist":"/hls/s9/index.m3u8?stoken=tok"}}`))
	})
	defer standby.Close()
	primarySecret = addNode(t, store, "primary", primary.URL, 10)
	standbySecret = addNode(t, store, "standby", standby.URL, 5)
	primary.Close()

	d := NewDispatcher(router, config.Config{}, nil)
	d.Enabled = func() bool { return true }
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playback/sessions", nil)
	d.CreateSession(rec, req, &auth.Principal{Kind: "user", UserID: "u1"}, []byte(`{"item_kind":"movie","item_id":"m1"}`), true)
	if rec.Code != 200 {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if node, _ := out["node"].(map[string]any); node["id"] != "standby" {
		t.Fatalf("placed on %v", out["node"])
	}
	if d.placedOn("s9") != "standby" {
		t.Fatal("placement not remembered")
	}
	if got, _ := router.Select(context.Background(), backend.RouteRequest{Roles: playbackRoles}); got.ID != "standby" {
		t.Fatalf("dead primary still preferred: %s", got.ID)
	}
}

func TestWorkerGuardRejectsUnsigned(t *testing.T) {
	secret, _ := nodeauth.NewSecret()
	wk := &Worker{Verifier: nodeauth.NewVerifier(secret)}
	h := wk.Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/playback/sessions/s1/file?stoken=tok", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned request: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/node/playback/sessions", strings.NewReader(`{}`))
	_ = nodeauth.Sign(req, "n1", []byte("some-other-secret-value-0123456789"), []byte(`{}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/node/playback/sessions", strings.NewReader(`{"x":1}`))
	_ = nodeauth.Sign(req, "n1", []byte(secret), []byte(`{}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("body swapped after signing: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/node/playback/sessions", strings.NewReader(`{}`))
	_ = nodeauth.Sign(req, "n1", []byte(secret), []byte(`{}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("signed request: %d", rec.Code)
	}
}
