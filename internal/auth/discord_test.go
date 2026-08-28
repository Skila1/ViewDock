package auth

import (
	"context"
	"testing"
)

func TestUpsertDiscordDoesNotClaimSoleAdmin(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpsertDiscordUser(context.Background(), DiscordProfile{ID: "999888777", Username: "eve", Global: "Eve"})
	if err == nil {
		t.Fatal("unknown Discord user must not log in when registration is off")
	}
	if id := s.DiscordUserID(context.Background(), admin.ID); id != "" {
		t.Fatalf("admin was linked to %s", id)
	}
	got, err := s.GetUser(context.Background(), admin.ID)
	if err != nil || got.ID != admin.ID {
		t.Fatal("admin account changed")
	}
}

func TestUpsertDiscordRegistrationCreatesNewUser(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.LoadDiscord(context.Background())
	cfg.RegistrationEnabled = true
	if err := s.SaveDiscord(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	u, err := s.UpsertDiscordUser(context.Background(), DiscordProfile{ID: "111", Username: "eve", Global: "Eve"})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == admin.ID {
		t.Fatal("discord registration linked the existing admin")
	}
	if u.IsAdmin {
		t.Fatal("new discord user must not be admin")
	}
	if s.DiscordUserID(context.Background(), admin.ID) != "" {
		t.Fatal("admin should stay unlinked")
	}
	if s.DiscordUserID(context.Background(), u.ID) != "111" {
		t.Fatal("new user should own the discord identity")
	}
}

func TestUpsertDiscordAdminIDsCreatesNewAdmin(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.LoadDiscord(context.Background())
	cfg.AdminDiscordIDs = "555"
	if err := s.SaveDiscord(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	u, err := s.UpsertDiscordUser(context.Background(), DiscordProfile{ID: "555", Username: "listed"})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == admin.ID {
		t.Fatal("admin_discord_ids must not hijack the existing admin")
	}
	if !u.IsAdmin {
		t.Fatal("listed discord id should create a new administrator")
	}
}

func TestLinkDiscordRequiresExistingUser(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkDiscord(context.Background(), admin.ID, DiscordProfile{ID: "42", Username: "admin"}); err != nil {
		t.Fatal(err)
	}
	if s.DiscordUserID(context.Background(), admin.ID) != "42" {
		t.Fatal("authenticated link should attach")
	}
}
