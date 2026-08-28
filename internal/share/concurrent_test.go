package share

import (
	"bytes"
	"context"
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

func shareHarness(t *testing.T) (*Service, *API) {
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
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = sqlDB.Exec(`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at)
		VALUES ('u1','admin','x','A','',1,0,'',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(sqlDB, fakeCat{ok: true})
	a := auth.New(sqlDB, config.Load(), settings.New(sqlDB), audit.New(sqlDB))
	return svc, NewAPI(svc, a)
}

func unlock(t *testing.T, api *API, token, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	api.Routes(r)
	req := httptest.NewRequest(http.MethodPost, "/share/"+token+"/unlock", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.GuestCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestMaxConcurrentIgnoresForgedAndUnrelatedCookies(t *testing.T) {
	svc, api := shareHarness(t)
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "u1", IsAdmin: true}
	raw, sh, err := svc.Create(context.Background(), admin, "movie", "m1", "", 1, "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	otherRaw, other, err := svc.Create(context.Background(), admin, "movie", "m1", "", 1, "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = otherRaw
	guest, _, err := svc.MintGuest(context.Background(), sh.ID, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	otherGuest, _, err := svc.MintGuest(context.Background(), other.ID, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	if rec := unlock(t, api, raw, "forged-cookie"); rec.Code != 429 {
		t.Fatalf("forged %d %s", rec.Code, rec.Body.String())
	}
	if rec := unlock(t, api, raw, otherGuest); rec.Code != 429 {
		t.Fatalf("unrelated share cookie %d %s", rec.Code, rec.Body.String())
	}
	if rec := unlock(t, api, raw, guest); rec.Code != 200 {
		t.Fatalf("valid returning %d %s", rec.Code, rec.Body.String())
	}
}

func TestMaxConcurrentExpiredGuestDoesNotBypass(t *testing.T) {
	svc, api := shareHarness(t)
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "u1", IsAdmin: true}
	raw, sh, err := svc.Create(context.Background(), admin, "movie", "m1", "", 1, "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	guest, _, err := svc.MintGuest(context.Background(), sh.ID, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := svc.DB.Exec(`UPDATE guest_sessions SET expires_at = ? WHERE token_hash = ?`, past, auth.HashToken(guest)); err != nil {
		t.Fatal(err)
	}
	if svc.ValidGuestForShare(context.Background(), guest, sh.ID) {
		t.Fatal("expired guest should be invalid")
	}
	if rec := unlock(t, api, raw, guest); rec.Code != 429 {
		t.Fatalf("expired cookie %d %s", rec.Code, rec.Body.String())
	}
}

func TestStaleReturningGuestDoesNotBypassCap(t *testing.T) {
	svc, api := shareHarness(t)
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "u1", IsAdmin: true}
	raw, sh, err := svc.Create(context.Background(), admin, "movie", "m1", "", 1, "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	stale, _, err := svc.MintGuest(context.Background(), sh.ID, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	if _, err := svc.DB.Exec(`UPDATE guest_sessions SET last_seen_at = ? WHERE token_hash = ?`, old, auth.HashToken(stale)); err != nil {
		t.Fatal(err)
	}
	if rec := unlock(t, api, raw, ""); rec.Code != 200 {
		t.Fatalf("new viewer while stale %d %s", rec.Code, rec.Body.String())
	}
	if rec := unlock(t, api, raw, stale); rec.Code != 429 {
		t.Fatalf("stale returning %d %s", rec.Code, rec.Body.String())
	}
}

func TestValidGuestForShareScope(t *testing.T) {
	svc, _ := shareHarness(t)
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "u1", IsAdmin: true}
	_, sh, err := svc.Create(context.Background(), admin, "movie", "m1", "", 1, "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := svc.Create(context.Background(), admin, "movie", "m1", "", 1, "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	guest, _, err := svc.MintGuest(context.Background(), sh.ID, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !svc.ValidGuestForShare(context.Background(), guest, sh.ID) {
		t.Fatal("own share")
	}
	if svc.ValidGuestForShare(context.Background(), guest, other.ID) {
		t.Fatal("other share")
	}
	if svc.ValidGuestForShare(context.Background(), "nope", sh.ID) {
		t.Fatal("forged")
	}
}
