package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func makeTemporary(t *testing.T, s *Service, username string, expires time.Time, maxSessions int) {
	t.Helper()
	hash, err := HashPassword("guest-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.DB.Exec(`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, has_password, is_temporary, expires_at, max_sessions, party_only)
		VALUES (?, ?, ?, ?, '', 0, 0, '', ?, ?, 1, 1, ?, ?, 1)`, username+"-id", username, hash, username, now, now, expires.UTC().Format(time.RFC3339), maxSessions); err != nil {
		t.Fatal(err)
	}
}

func TestTemporaryAccountExpiryAndSessionLimit(t *testing.T) {
	s := testSvc(t)
	ctx := context.Background()
	makeTemporary(t, s, "expired", time.Now().Add(-time.Minute), 2)
	if _, _, _, err := s.Login(ctx, "expired", "guest-pass-123", "127.0.0.1", "test"); err == nil {
		t.Fatal("expired temporary account signed in")
	}

	makeTemporary(t, s, "limited", time.Now().Add(time.Hour), 1)
	_, exp, _, err := s.Login(ctx, "limited", "guest-pass-123", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("first login: %v", err)
	}
	if exp.After(time.Now().Add(time.Hour + time.Minute)) {
		t.Fatalf("session outlives account: %v", exp)
	}
	if _, _, _, err := s.Login(ctx, "limited", "guest-pass-123", "127.0.0.1", "test"); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("second login: %v", err)
	}
	u, err := s.GetUser(ctx, "limited-id")
	if err != nil {
		t.Fatal(err)
	}
	if !u.Temporary || !u.PartyOnly || u.MaxSessions != 1 {
		t.Fatalf("fields not loaded: %+v", u)
	}
}
