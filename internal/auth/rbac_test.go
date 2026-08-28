package auth

import (
	"context"
	"testing"
)

func TestRBACAssignAndHasPerm(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	if !admin.IsAdmin {
		t.Fatal("admin flag")
	}
	u, err := s.CreateUser(context.Background(), "sam", "secret12", "Sam", false)
	if err != nil {
		t.Fatal(err)
	}
	if u.IsAdmin {
		t.Fatal("user should not be admin")
	}
	found := false
	for _, p := range u.Permissions {
		if p == PermSharesCreate {
			found = true
		}
		if p == PermAdmin {
			t.Fatal("user has admin perm")
		}
	}
	if !found {
		t.Fatalf("user perms %v", u.Permissions)
	}
	p := &Principal{Kind: KindUser, UserID: u.ID, Permissions: u.Permissions}
	if !p.HasPerm(PermSharesCreate) || p.HasPerm(PermUsersManage) {
		t.Fatal("hasperm")
	}
	adminP := &Principal{Kind: KindUser, UserID: admin.ID, IsAdmin: true}
	if !adminP.HasPerm(PermUsersManage) {
		t.Fatal("admin bypass")
	}
}

func TestLastAdminGuard(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(context.Background(), "other", admin.ID, true); err != ErrLastAdmin {
		t.Fatalf("got %v", err)
	}
	if err := s.SetUserRoles(context.Background(), admin.ID, []string{RoleUser}); err != ErrLastAdmin {
		t.Fatalf("demote %v", err)
	}
}

func TestRoleLibraryGrant(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(context.Background(), "sam", "secret12", "Sam", false)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = admin, u
	if _, err := s.DB.Exec(`INSERT INTO libraries(id, name, root_path, content_type, uploads_enabled, created_at, updated_at)
		VALUES ('lib1', 'L', '/tmp', 'movies', 0, '2020-01-01T00:00:00Z', '2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Grants.SetRole(context.Background(), RoleUser, "lib1", false); err != nil {
		t.Fatal(err)
	}
	if !s.Grants.CanRead(context.Background(), u.ID, "lib1") {
		t.Fatal("role grant should allow read")
	}
	if s.Grants.CanDownload(context.Background(), u.ID, "lib1") {
		t.Fatal("download should be off")
	}
	if !s.Grants.CanRead(context.Background(), admin.ID, "lib1") {
		t.Fatal("admin read")
	}
}

func TestDiscordDisabled(t *testing.T) {
	s := testSvc(t)
	cfg := s.LoadDiscord(context.Background())
	if cfg.Ready() {
		t.Fatal("discord should be off by default")
	}
}
