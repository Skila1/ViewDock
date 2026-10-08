package oplog

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
)

func testStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return New(sqlDB), sqlDB
}

func waitRows(t *testing.T, s *Store, f Filter, n int) []Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		list, err := s.List(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) >= n || time.Now().After(deadline) {
			return list
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestErrorReportStoredAndRedacted(t *testing.T) {
	s, _ := testStore(t)
	body := `{
		"message": "Stream is still starting.\nRetry in a moment.",
		"code": "ATTACH_FAILED",
		"stage": "startup",
		"context": {"item_id": "m1", "ready_state": 0, "mse": true, "nested": {"x": 1}, "Bad Key": "x", "session_token": "abc"},
		"trace": "GET /api/v1/media-sources/stream/SECRETGRANT/Videos/1/master.m3u8?api_key=zzz 200"
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/error-reports", strings.NewReader(body))
	req.Header.Set("User-Agent", "ViewDockTest/1.0")
	s.handleReport(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report %d %s", rec.Code, rec.Body.String())
	}
	var out struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.ID == "" {
		t.Fatal("report id missing")
	}

	list := waitRows(t, s, Filter{Category: CategoryClientError, Limit: 5}, 1)
	if len(list) != 1 {
		t.Fatalf("expected one report, got %d", len(list))
	}
	e := list[0]
	if e.ID != out.ID || e.Level != "error" || e.Message != "Stream is still starting. Retry in a moment." {
		t.Fatalf("unexpected entry %+v", e)
	}
	d := e.Details
	if d["code"] != "ATTACH_FAILED" || d["stage"] != "startup" || d["item_id"] != "m1" || d["mse"] != true || d["ua"] != "ViewDockTest/1.0" {
		t.Fatalf("details %+v", d)
	}
	if _, ok := d["nested"]; ok {
		t.Fatal("nested values must be dropped")
	}
	if _, ok := d["Bad Key"]; ok {
		t.Fatal("invalid keys must be dropped")
	}
	if d["session_token"] != "[redacted]" {
		t.Fatalf("token key not redacted: %v", d["session_token"])
	}
	trace, _ := d["trace"].(string)
	if strings.Contains(trace, "SECRETGRANT") || strings.Contains(trace, "zzz") {
		t.Fatalf("trace kept secrets: %s", trace)
	}
}

func TestErrorReportRejectsEmptyMessage(t *testing.T) {
	s, _ := testStore(t)
	rec := httptest.NewRecorder()
	s.handleReport(rec, httptest.NewRequest(http.MethodPost, "/error-reports", strings.NewReader(`{"message":"   "}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty message should 400, got %d", rec.Code)
	}
}

func TestAuditListRequiresLogsRead(t *testing.T) {
	s, sqlDB := testStore(t)
	aud := audit.New(sqlDB)
	aud.Event(context.Background(), "u1", "api_key.create", "debug", "10.0.0.1", "logs.read")
	aud.Event(context.Background(), "u1", "backup.create", "b1", "", "token=abc")

	r := chi.NewRouter()
	s.Routes(r)

	get := func(p *auth.Principal, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if p != nil {
			req = req.WithContext(auth.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := get(nil, "/admin/audit"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous audit read should 401, got %d", rec.Code)
	}
	viewer := &auth.Principal{Kind: auth.KindUser, UserID: "u2"}
	if rec := get(viewer, "/admin/audit"); rec.Code != http.StatusForbidden {
		t.Fatalf("user without logs.read should 403, got %d", rec.Code)
	}
	key := &auth.Principal{Kind: auth.KindUser, UserID: "u1", APIKey: true, Permissions: []string{auth.PermLogsRead}}
	rec := get(key, "/admin/audit?action=backup.")
	if rec.Code != http.StatusOK {
		t.Fatalf("logs.read key should read audit, got %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []audit.Event `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].Action != "backup.create" {
		t.Fatalf("action prefix filter: %+v", out.Items)
	}
	if strings.Contains(out.Items[0].Detail, "abc") {
		t.Fatalf("audit detail not redacted: %s", out.Items[0].Detail)
	}
}
