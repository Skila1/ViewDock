package auth

import (
	"context"
	"testing"
)

func managerActor(t *testing.T, s *Service, perms ...string) (*Principal, User) {
	t.Helper()
	u, err := s.CreateUser(context.Background(), "mgr-"+perms[0], "secret12", "Mgr", false)
	if err != nil {
		t.Fatal(err)
	}
	role, err := s.CreateRole(context.Background(), "Delegated-"+u.Username, "", perms)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserRoles(context.Background(), u.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	u, err = s.GetUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &Principal{Kind: KindUser, UserID: u.ID, Permissions: u.Permissions, IsAdmin: u.IsAdmin}, u
}

func TestUsersManageCannotPromoteAdmin(t *testing.T) {
	s := testSvc(t)
	if _, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin"); err != nil {
		t.Fatal(err)
	}
	actor, u := managerActor(t, s, PermUsersManage, PermRolesManage)
	if actor.CanManageAdministrators() {
		t.Fatal("manager should not manage administrators")
	}
	if err := s.AssertCanAssignRoles(context.Background(), actor, u.ID, []string{RoleAdministrator}); err != ErrAdministratorsManage {
		t.Fatalf("self-promote via role: %v", err)
	}
	if err := s.SetUserRolesAs(context.Background(), actor, u.ID, []string{RoleAdministrator}); err != ErrAdministratorsManage {
		t.Fatalf("SetUserRolesAs: %v", err)
	}
	if err := s.AddRoleMembersAs(context.Background(), actor, RoleAdministrator, []string{u.ID}); err != ErrAdministratorsManage {
		t.Fatalf("AddRoleMembersAs: %v", err)
	}
	got, _ := s.GetUser(context.Background(), u.ID)
	if got.IsAdmin {
		t.Fatal("manager became admin")
	}
}

func TestUsersManageCannotModifyAdministrator(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := managerActor(t, s, PermUsersManage)
	if err := s.AssertCanModifyUser(context.Background(), actor, admin.ID); err != ErrProtectedUser {
		t.Fatalf("superadmin: %v", err)
	}
	second, err := s.CreateUser(context.Background(), "mod", "secret12", "Mod", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AssertCanModifyUser(context.Background(), actor, second.ID); err != ErrAdministratorsManage {
		t.Fatalf("got %v", err)
	}
	if err := s.SetDisabled(context.Background(), actor, second.ID, true); err != ErrAdministratorsManage {
		t.Fatalf("disable admin: %v", err)
	}
}

func TestUsersManageCannotGrantUnheldPermission(t *testing.T) {
	s := testSvc(t)
	if _, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin"); err != nil {
		t.Fatal(err)
	}
	actor, _ := managerActor(t, s, PermUsersManage)
	if err := s.AssertCanGrantPermissions(context.Background(), actor, []string{PermSettingsManage}); err != ErrPrivilegeCeiling {
		t.Fatalf("got %v", err)
	}
	if err := s.AssertCanAssignRoles(context.Background(), actor, "", []string{RoleUser}); err != ErrPrivilegeCeiling {
		t.Fatalf("assign User role without its perms: %v", err)
	}
}

func TestRolesManageCannotJoinAdministrator(t *testing.T) {
	s := testSvc(t)
	if _, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin"); err != nil {
		t.Fatal(err)
	}
	actor, u := managerActor(t, s, PermRolesManage)
	other, err := s.CreateUser(context.Background(), "sam", "secret12", "Sam", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddRoleMembersAs(context.Background(), actor, RoleAdministrator, []string{other.ID}); err != ErrAdministratorsManage {
		t.Fatalf("promote other: %v", err)
	}
	if err := s.AddRoleMembersAs(context.Background(), actor, RoleAdministrator, []string{u.ID}); err != ErrAdministratorsManage {
		t.Fatalf("self join: %v", err)
	}
}

func TestCannotPatchSystemRolePermissions(t *testing.T) {
	s := testSvc(t)
	if _, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateRole(context.Background(), RoleUser, "Users", []string{PermAdministratorsManage}); err == nil {
		t.Fatal("sys-user permissions must be immutable")
	}
	if _, err := s.UpdateRole(context.Background(), RoleAdministrator, "Admins", []string{PermUsersManage}); err == nil {
		t.Fatal("administrator permissions must be immutable")
	}
}

func TestAdminCanAssignAdministrator(t *testing.T) {
	s := testSvc(t)
	admin, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	actor := &Principal{Kind: KindUser, UserID: admin.ID, IsAdmin: true, Permissions: admin.Permissions}
	u, err := s.CreateUser(context.Background(), "sam", "secret12", "Sam", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserRolesAs(context.Background(), actor, u.ID, []string{RoleAdministrator}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetUser(context.Background(), u.ID)
	if !got.IsAdmin {
		t.Fatal("admin should be able to promote")
	}
}
