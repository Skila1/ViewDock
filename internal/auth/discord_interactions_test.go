package auth

import (
	"context"
	"errors"
	"testing"
)

func TestPrincipalForDiscord(t *testing.T) {
	s := testSvc(t)
	ctx := context.Background()
	if _, err := s.CreateAdmin(ctx, "admin", "secret12", "Admin"); err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(ctx, "sam", "secret12", "Sam", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrincipalForDiscord(ctx, "111111111111111111"); !errors.Is(err, ErrDiscordNotLinked) {
		t.Fatalf("unlinked = %v", err)
	}
	if err := s.linkDiscord(ctx, u.ID, DiscordProfile{ID: "111111111111111111", Username: "sam"}); err != nil {
		t.Fatal(err)
	}
	p, err := s.PrincipalForDiscord(ctx, "111111111111111111")
	if err != nil || !p.IsUser() || p.UserID != u.ID || p.IsAdmin || p.DisplayName != "Sam" {
		t.Fatalf("linked = %+v, %v", p, err)
	}
	if err := s.SetDisabled(ctx, nil, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrincipalForDiscord(ctx, "111111111111111111"); !errors.Is(err, ErrDiscordNotLinked) {
		t.Fatalf("disabled account = %v", err)
	}
	if _, err := s.PrincipalForDiscord(ctx, ""); !errors.Is(err, ErrDiscordNotLinked) {
		t.Fatalf("empty id = %v", err)
	}
}
