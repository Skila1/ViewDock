package share

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
)

type fakeCat struct{ ok bool }

func (f fakeCat) ItemTitle(context.Context, string, string) (string, error) { return "X", nil }
func (f fakeCat) Exists(context.Context, string, string) bool               { return f.ok }
func (f fakeCat) LibraryIDForItem(context.Context, string, string) (string, error) {
	return "lib", nil
}

func TestShareGateIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = sqlDB.Exec(`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at)
		VALUES ('u1','admin','x','A','',1,0,'',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}

	svc := New(sqlDB, fakeCat{ok: true})
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "u1", IsAdmin: true}
	raw, sh, err := svc.Create(context.Background(), admin, "movie", "m1", "", 0, "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = svc.LookupByToken(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	guestRaw, _, err := svc.MintGuest(context.Background(), sh.ID, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	var gid string
	if err := sqlDB.QueryRow(`SELECT id FROM guest_sessions WHERE token_hash = ?`, auth.HashToken(guestRaw)).Scan(&gid); err != nil {
		t.Fatal(err)
	}
	if err := svc.AllowStream(context.Background(), gid, "movie", "m1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CanStreamMedia(context.Background(), gid, "movie", "other"); err != ErrDenied {
		t.Fatalf("expected denied, got %v", err)
	}
	_ = svc.Revoke(context.Background(), sh.ID)
	if err := svc.CanStreamMedia(context.Background(), gid, "movie", "m1"); err != ErrGone {
		t.Fatalf("expected gone after revoke, got %v", err)
	}
}
