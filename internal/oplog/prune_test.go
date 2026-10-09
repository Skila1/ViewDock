package oplog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
)

func prunable(t *testing.T) *Store {
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
	s := &Store{DB: sqlDB}
	for i, at := range []string{"2025-01-01T00:00:00Z", "2026-03-01T12:00:00.5Z", "2026-10-01T00:00:00Z", "2026-10-09T08:00:00Z"} {
		s.insert(context.Background(), Entry{ID: string(rune('a' + i)), CreatedAt: at, Level: "info", Category: "app", Message: "m"})
	}
	return s
}

func TestLogsStayUntilPruned(t *testing.T) {
	s := prunable(t)
	s.Sweep(context.Background())
	st, err := s.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Rows != 4 || st.Oldest != "2025-01-01T00:00:00Z" || st.Newest != "2026-10-09T08:00:00Z" || st.Bytes <= 0 {
		t.Fatalf("without a retention every log stays: %+v", st)
	}
	s.SetRetentionDays(36500)
	s.Sweep(context.Background())
	if st, _ := s.Stats(context.Background()); st.Rows != 4 {
		t.Fatalf("a long retention keeps them: %+v", st)
	}
}

func TestPruneBeforeAndAll(t *testing.T) {
	s := prunable(t)
	n, err := s.Prune(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || n != 2 {
		t.Fatalf("before 1 October removes the two older rows, got %d %v", n, err)
	}
	if st, _ := s.Stats(context.Background()); st.Rows != 2 || st.Oldest != "2026-10-01T00:00:00Z" {
		t.Fatalf("left %+v", st)
	}
	if n, err := s.Prune(context.Background(), time.Time{}); err != nil || n != 2 {
		t.Fatalf("prune all %d %v", n, err)
	}
	if st, _ := s.Stats(context.Background()); st.Rows != 0 || st.Oldest != "" {
		t.Fatalf("left %+v", st)
	}
}

func TestPruneEndpoint(t *testing.T) {
	s := prunable(t)
	r := chi.NewRouter()
	s.Routes(r)
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "u1", Permissions: []string{auth.PermSettingsManage, auth.PermLogsRead}}
	reader := &auth.Principal{Kind: auth.KindUser, UserID: "u2", Permissions: []string{auth.PermLogsRead}}
	do := func(p *auth.Principal, method, url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, url, nil)
		r.ServeHTTP(rec, req.WithContext(auth.WithPrincipal(req.Context(), p)))
		return rec
	}
	if rec := do(reader, http.MethodDelete, "/admin/logs?all=true"); rec.Code != http.StatusForbidden {
		t.Fatalf("reading logs is not enough to delete them, got %d", rec.Code)
	}
	if rec := do(admin, http.MethodDelete, "/admin/logs"); rec.Code != 400 {
		t.Fatalf("a prune needs before or all, got %d", rec.Code)
	}
	rec := do(admin, http.MethodDelete, "/admin/logs?before=2026-03-02T00:00:00Z")
	var out struct{ Deleted int64 }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out.Deleted != 2 {
		t.Fatalf("prune before %d %s", rec.Code, rec.Body.String())
	}
	rec = do(admin, http.MethodGet, "/admin/logs/stats")
	var st Stats
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if rec.Code != 200 || st.Rows != 2 {
		t.Fatalf("stats %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(admin, http.MethodDelete, "/admin/logs?all=true"); rec.Code != 200 {
		t.Fatalf("prune all %d", rec.Code)
	}
	var actions int
	_ = s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_events WHERE action = 'logs.prune'`).Scan(&actions)
	if actions != 2 {
		t.Fatalf("each prune is audited, got %d", actions)
	}
}
