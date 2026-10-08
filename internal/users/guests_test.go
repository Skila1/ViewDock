package users

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/settings"
)

type fixture struct {
	api    *API
	router chi.Router
	admin  auth.User
	owner  auth.User
	other  auth.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	kv := settings.New(sqlDB)
	_ = kv.Set(context.Background(), "setup.complete", "1")
	svc := auth.New(sqlDB, config.Load(), kv, audit.New(sqlDB))
	ctx := context.Background()
	admin, err := svc.CreateAdmin(ctx, "admin", "admin-pass-123", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateUser(ctx, "owner", "owner-pass-123", "Owner", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateUser(ctx, "other", "other-pass-123", "Other", false)
	if err != nil {
		t.Fatal(err)
	}
	api := New(sqlDB, svc)
	r := chi.NewRouter()
	api.Routes(r)
	return &fixture{api: api, router: r, admin: admin, owner: owner, other: other}
}

func (f *fixture) call(t *testing.T, as auth.User, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	p := &auth.Principal{Kind: auth.KindUser, UserID: as.ID, Username: as.Username, IsAdmin: as.IsAdmin}
	if as.IsAdmin {
		p.Permissions = []string{auth.PermUsersManage}
	}
	req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestHouseholdOwnerCannotForceMembership(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.call(t, f.owner, http.MethodPost, "/household", map[string]string{"name": "Home"}); code != 200 {
		t.Fatalf("create household %d", code)
	}
	if code, _ := f.call(t, f.owner, http.MethodPost, "/household/members", map[string]any{"user_id": f.other.ID}); code != http.StatusForbidden {
		t.Fatalf("owner forced a user in without consent: %d", code)
	}
	if code, _ := f.call(t, f.owner, http.MethodPost, "/household/invites", map[string]any{"role": "owner"}); code != http.StatusBadRequest {
		t.Fatalf("owner role accepted on invite: %d", code)
	}
	code, inv := f.call(t, f.owner, http.MethodPost, "/household/invites", map[string]any{"role": "child", "age_limit": 12})
	if code != http.StatusCreated {
		t.Fatalf("invite %d", code)
	}
	token := inv["token"].(string)
	if code, _ := f.call(t, f.other, http.MethodPost, "/household/invites/accept", map[string]string{"token": token}); code != 200 {
		t.Fatalf("accept %d", code)
	}
	if code, _ := f.call(t, f.admin, http.MethodPost, "/household/invites/accept", map[string]string{"token": token}); code != http.StatusNotFound {
		t.Fatalf("invite reused: %d", code)
	}
	if code, _ := f.call(t, f.owner, http.MethodPost, "/household/members", map[string]any{"user_id": f.other.ID, "role": "owner"}); code != http.StatusBadRequest {
		t.Fatalf("promoted member to owner: %d", code)
	}
	if code, _ := f.call(t, f.owner, http.MethodPost, "/household/members", map[string]any{"user_id": f.other.ID, "role": "adult"}); code != 200 {
		t.Fatalf("update existing member: %d", code)
	}
}

func TestGuestAccountsLifecycle(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.call(t, f.other, http.MethodPost, "/guests", map[string]any{"expires_in_hours": 2}); code != http.StatusForbidden {
		t.Fatalf("regular user issued a guest: %d", code)
	}
	if code, _ := f.call(t, f.admin, http.MethodPost, "/guests", map[string]any{"expires_in_hours": 10000}); code != http.StatusBadRequest {
		t.Fatalf("unbounded expiry accepted: %d", code)
	}
	if code, _ := f.call(t, f.owner, http.MethodPost, "/household", map[string]string{"name": "Home"}); code != 200 {
		t.Fatalf("create household %d", code)
	}
	code, g := f.call(t, f.owner, http.MethodPost, "/guests", map[string]any{"expires_in_hours": 2, "party_only": false, "library_ids": []string{"x"}})
	if code != http.StatusCreated {
		t.Fatalf("owner guest %d %v", code, g)
	}
	if g["party_only"] != true || g["password"] == "" {
		t.Fatalf("owner guest must be party-only with generated password: %v", g)
	}
	guestID := g["id"].(string)
	ctx := context.Background()
	u, err := f.api.Auth.GetUser(ctx, guestID)
	if err != nil || !u.Temporary || !u.PartyOnly {
		t.Fatalf("stored guest %+v %v", u, err)
	}
	if _, _, _, err := f.api.Auth.Login(ctx, g["username"].(string), g["password"].(string), "127.0.0.1", "test"); err != nil {
		t.Fatalf("guest login: %v", err)
	}

	code, list := f.call(t, f.other, http.MethodGet, "/guests", nil)
	if code != http.StatusForbidden {
		t.Fatalf("non-owner listed guests: %d %v", code, list)
	}
	code, list = f.call(t, f.owner, http.MethodGet, "/guests", nil)
	if code != 200 || len(list["items"].([]any)) != 1 {
		t.Fatalf("owner list %d %v", code, list)
	}

	_, _ = f.api.DB.Exec(`UPDATE users SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), guestID)
	if n := f.api.SweepExpiredGuests(ctx); n != 1 {
		t.Fatalf("swept %d", n)
	}
	if c, _ := f.api.Auth.Sessions.CountForUser(ctx, guestID); c != 0 {
		t.Fatalf("expired guest kept %d sessions", c)
	}
	if code, _ := f.call(t, f.owner, http.MethodDelete, "/guests/"+guestID, nil); code != 200 {
		t.Fatalf("revoke %d", code)
	}
	if _, err := f.api.Auth.GetUser(ctx, guestID); err == nil {
		t.Fatal("revoked guest still exists")
	}
}
