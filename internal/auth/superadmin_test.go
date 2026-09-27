package auth

import (
	"context"
	"testing"
)

func TestCreateAdminIsSuperadmin(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsSuperadmin(context.Background(), admin.ID) {
		t.Fatal("original admin must be Superadmin")
	}
	if s.OriginalAdminID(context.Background()) != admin.ID {
		t.Fatal("original admin id")
	}
	names := s.RoleNamesFor(context.Background(), admin.ID)
	hasSA, hasAdm := false, false
	for _, n := range names {
		if n == "Superadmin" {
			hasSA = true
		}
		if n == "Administrator" {
			hasAdm = true
		}
	}
	if !hasSA || !hasAdm {
		t.Fatalf("want Superadmin + Administrator, got %v", names)
	}
}

func TestDeleteRegularUser(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(context.Background(), "sam", "secret12", "Sam", false)
	if err != nil {
		t.Fatal(err)
	}
	actor := &Principal{Kind: KindUser, UserID: admin.ID, IsAdmin: true, Permissions: []string{PermAdmin, PermUsersManage, PermAdministratorsManage}}
	if err := s.DeleteUser(context.Background(), actor, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUser(context.Background(), u.ID); err == nil {
		t.Fatal("user should be gone")
	}
	other := &Principal{Kind: KindUser, UserID: "other-admin", IsAdmin: true, Permissions: []string{PermAdmin, PermUsersManage, PermAdministratorsManage}}
	if err := s.DeleteUser(context.Background(), other, admin.ID); err != ErrProtectedUser {
		t.Fatalf("superadmin delete %v", err)
	}
}

func TestLocalLoginBlockedWhenDiscordReady(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(context.Background(), "sam", "secret12", "Sam", false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.LoadDiscord(context.Background())
	cfg.LoginEnabled = true
	cfg.ClientID = "cid"
	cfg.Secret = "sec"
	if err := s.SaveDiscord(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Login(context.Background(), "sam", "secret12", "", ""); err != ErrLocalLoginDisabled {
		t.Fatalf("regular local login %v", err)
	}
	if _, _, _, err := s.Login(context.Background(), "admin", "secret12", "", ""); err != ErrLocalLoginDisabled {
		t.Fatalf("superadmin local login %v", err)
	}
	if err := s.AssociateSuperadminDiscord(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	if s.DiscordUserID(context.Background(), admin.ID) != "42" {
		t.Fatal("superadmin discord id")
	}
	if _, err := s.CreateUser(context.Background(), "pat", "secret12", "Pat", false); err != ErrLocalSignupDisabled {
		t.Fatalf("signup %v", err)
	}
	_ = u
}

func TestDiscordLoginScope(t *testing.T) {
	if DiscordLoginScope(DiscordRegistration{}) != "identify" {
		t.Fatal("default")
	}
	if DiscordLoginScope(DiscordRegistration{GuildEnabled: true, GuildID: "1"}) != "identify guilds" {
		t.Fatal("guilds")
	}
	got := DiscordLoginScope(DiscordRegistration{RoleEnabled: true, RoleID: "2"})
	if got != "identify guilds guilds.members.read" {
		t.Fatalf("roles %q", got)
	}
	if err := CheckDiscordRegistration(context.Background(), "", DiscordRegistration{}); err != nil {
		t.Fatal(err)
	}
}
