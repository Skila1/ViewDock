package labs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
)

type recAudit struct{ actions []string }

func (a *recAudit) Event(_ context.Context, _, action, _, _, detail string) {
	a.actions = append(a.actions, action+" "+detail)
}

func newAPI(t *testing.T, p *auth.Principal) (http.Handler, *Broadcaster, *recAudit) {
	t.Helper()
	b, runner, _, _ := setup(t, Selection{RoomID: "room-1"})
	runner.onStart = func(_ int, p *fakeProc) { p.progress(1) }
	_ = saveAck(context.Background(), b.Store, nil)
	b.Resolver = fakeResolver{"live.example.com": {"203.0.113.10"}}
	aud := &recAudit{}
	api := &API{B: b, Audit: aud, Cfg: config.Config{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if p != nil {
				req = req.WithContext(auth.WithPrincipal(req.Context(), p))
			}
			next.ServeHTTP(w, req)
		})
	})
	api.Routes(r)
	t.Cleanup(b.Close)
	return r, b, aud
}

func do(t *testing.T, h http.Handler, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "198.51.100.7:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

var admin = &auth.Principal{Kind: auth.KindUser, UserID: "admin", IsAdmin: true}

func TestLabsAPIRequiresAdmin(t *testing.T) {
	for _, p := range []*auth.Principal{nil, {Kind: auth.KindUser, UserID: "u", Permissions: []string{auth.PermSettingsManage}}} {
		h, _, _ := newAPI(t, p)
		for _, route := range [][2]string{{"GET", "/admin/labs/vcam"}, {"POST", "/admin/labs/vcam/start"}, {"PUT", "/admin/labs/vcam/config"}, {"GET", "/admin/labs/vcam/preview"}} {
			if rec, _ := do(t, h, route[0], route[1], map[string]any{}); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
				t.Errorf("%v %s as %+v = %d", route[0], route[1], p, rec.Code)
			}
		}
	}
}

func TestLabsAPIFlow(t *testing.T) {
	h, b, aud := newAPI(t, admin)

	rec, out := do(t, h, "GET", "/admin/labs/vcam", nil)
	if rec.Code != 200 || out["acknowledgment"] != nil || out["health"].(map[string]any)["state"] != StateDisabled {
		t.Fatalf("initial = %d %v", rec.Code, out)
	}
	if !strings.Contains(out["notice"].(map[string]any)["text"].(string), "Terms of Service") {
		t.Fatal("notice text missing")
	}

	cfgBody := map[string]any{"mode": "rtmp", "width": 1280, "height": 720, "fps": 30, "video_kbps": 3500, "audio_kbps": 160, "threads": 2,
		"selection": map[string]any{"room_id": "room-1"}, "output_url": "rtmp://live.example.com/app/sk_live_SECRET99"}
	if rec, out := do(t, h, "PUT", "/admin/labs/vcam/config", cfgBody); rec.Code != http.StatusForbidden || out["code"] != "labs_not_acknowledged" {
		t.Fatalf("config before ack = %d %v", rec.Code, out)
	}
	if rec, _ := do(t, h, "POST", "/admin/labs/vcam/start", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("start before ack = %d", rec.Code)
	}
	if rec, _ := do(t, h, "POST", "/admin/labs/vcam/acknowledge", map[string]any{"notice_version": NoticeVersion, "accept": false}); rec.Code != http.StatusBadRequest {
		t.Fatalf("ack without accept = %d", rec.Code)
	}
	if rec, _ := do(t, h, "POST", "/admin/labs/vcam/acknowledge", map[string]any{"notice_version": "old", "accept": true}); rec.Code != http.StatusConflict {
		t.Fatalf("ack of old notice = %d", rec.Code)
	}
	if rec, _ := do(t, h, "POST", "/admin/labs/vcam/acknowledge", map[string]any{"notice_version": NoticeVersion, "accept": true}); rec.Code != 200 {
		t.Fatalf("ack = %d", rec.Code)
	}

	bad := map[string]any{}
	for k, v := range cfgBody {
		bad[k] = v
	}
	bad["output_url"] = "rtmp://169.254.169.254/latest/meta-data"
	if rec, out := do(t, h, "PUT", "/admin/labs/vcam/config", bad); rec.Code != http.StatusBadRequest || out["code"] != "labs_output" {
		t.Fatalf("metadata output = %d %v", rec.Code, out)
	}
	rec, out = do(t, h, "PUT", "/admin/labs/vcam/config", cfgBody)
	if rec.Code != 200 {
		t.Fatalf("config = %d %v", rec.Code, out)
	}
	if strings.Contains(rec.Body.String(), "SECRET99") {
		t.Fatal("config response leaks the stream key")
	}
	view := out["config"].(map[string]any)
	if view["output_url_set"] != true || view["output_display"] != "rtmp://live.example.com/[redacted]" {
		t.Fatalf("config view = %v", view)
	}
	// Omitting output_url keeps the saved one.
	delete(cfgBody, "output_url")
	cfgBody["fps"] = 25
	if rec, out := do(t, h, "PUT", "/admin/labs/vcam/config", cfgBody); rec.Code != 200 || out["config"].(map[string]any)["output_url_set"] != true {
		t.Fatalf("config keep output = %d %v", rec.Code, out)
	}

	if rec, out := do(t, h, "POST", "/admin/labs/vcam/start", nil); rec.Code != 200 {
		t.Fatalf("start = %d %v", rec.Code, out)
	}
	eventually(t, "running", func() bool { return b.Health().State == StateRunning })
	if rec, _ := do(t, h, "POST", "/admin/labs/vcam/start", nil); rec.Code != http.StatusConflict {
		t.Fatalf("double start = %d", rec.Code)
	}
	if rec, out := do(t, h, "GET", "/admin/labs/vcam/preview?kind=outgoing", nil); rec.Code != http.StatusNotFound || out["code"] != "no_preview" {
		t.Fatalf("missing preview = %d", rec.Code)
	}
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F'}
	if err := os.WriteFile(filepath.Join(b.WorkDir, PreviewOutgoing), jpeg, 0o600); err != nil {
		t.Fatal(err)
	}
	rec, _ = do(t, h, "GET", "/admin/labs/vcam/preview?kind=outgoing", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Cache-Control") != "no-store" || !bytes.Equal(rec.Body.Bytes(), jpeg) {
		t.Fatalf("preview = %d %v", rec.Code, rec.Header())
	}
	if rec, _ := do(t, h, "GET", "/admin/labs/vcam/preview?kind=../../etc/passwd", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("preview kind traversal = %d", rec.Code)
	}
	if rec, out := do(t, h, "GET", "/admin/labs/vcam/health", nil); rec.Code != 200 || out["state"] != StateRunning {
		t.Fatalf("health = %d %v", rec.Code, out)
	}

	if rec, _ := do(t, h, "DELETE", "/admin/labs/vcam/acknowledge", nil); rec.Code != 200 {
		t.Fatalf("withdraw = %d", rec.Code)
	}
	if b.Running() {
		t.Fatal("withdrawing the acknowledgment left the broadcaster running")
	}
	if _, out := do(t, h, "GET", "/admin/labs/vcam", nil); out["health"].(map[string]any)["state"] != StateDisabled {
		t.Fatalf("after withdraw = %v", out["health"])
	}
	joined := strings.Join(aud.actions, "|")
	for _, a := range []string{"labs.vcam.acknowledge", "labs.vcam.config", "labs.vcam.start", "labs.vcam.disable"} {
		if !strings.Contains(joined, a) {
			t.Fatalf("missing audit %s in %s", a, joined)
		}
	}
	if strings.Contains(joined, "SECRET99") {
		t.Fatal("audit detail leaks the stream key")
	}
}
