package auth

import (
	"context"
	"errors"
	"strings"
)

const PermAdministratorsManage = "administrators.manage"

var (
	ErrPrivilegeCeiling     = errors.New("cannot grant privileges above your own")
	ErrAdministratorsManage = errors.New("administrator management required")
)

func (p *Principal) CanManageAdministrators() bool {
	return p != nil && p.HasPerm(PermAdministratorsManage)
}

func (s *Service) RoleIsPrivileged(ctx context.Context, roleID string) bool {
	if roleID == RoleAdministrator || roleID == RoleSuperadmin {
		return true
	}
	for _, name := range s.rolePerms(ctx, roleID) {
		if name == PermAdmin || name == PermAdministratorsManage {
			return true
		}
	}
	return false
}

func (s *Service) AssertCanGrantPermissions(ctx context.Context, actor *Principal, perms []string) error {
	if actor == nil || !actor.IsUser() {
		return ErrPrivilegeCeiling
	}
	for _, name := range perms {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if (name == PermAdmin || name == PermAdministratorsManage) && !actor.CanManageAdministrators() {
			return ErrAdministratorsManage
		}
		if !actor.HasPerm(name) {
			return ErrPrivilegeCeiling
		}
	}
	return nil
}

func (s *Service) AssertCanModifyUser(ctx context.Context, actor *Principal, targetUserID string) error {
	if actor == nil || !actor.IsUser() {
		return ErrPrivilegeCeiling
	}
	if targetUserID == "" || targetUserID == actor.UserID {
		return nil
	}
	if s.IsSuperadmin(ctx, targetUserID) && !s.IsSuperadmin(ctx, actor.UserID) {
		return ErrProtectedUser
	}
	if s.userIsAdmin(ctx, targetUserID) && !actor.CanManageAdministrators() {
		return ErrAdministratorsManage
	}
	return nil
}

func (s *Service) AssertCanAssignRoles(ctx context.Context, actor *Principal, targetUserID string, roleIDs []string) error {
	if actor == nil || !actor.IsUser() {
		return ErrPrivilegeCeiling
	}
	if targetUserID != "" && targetUserID != actor.UserID && s.userIsAdmin(ctx, targetUserID) && !actor.CanManageAdministrators() {
		return ErrAdministratorsManage
	}
	for _, id := range roleIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if id == RoleSuperadmin && !s.IsSuperadmin(ctx, actor.UserID) {
			return ErrProtectedUser
		}
		if s.RoleIsPrivileged(ctx, id) && !actor.CanManageAdministrators() {
			return ErrAdministratorsManage
		}
		if err := s.AssertCanGrantPermissions(ctx, actor, s.rolePerms(ctx, id)); err != nil {
			return err
		}
	}
	if s.rolesGrantAdmin(ctx, roleIDs) && !actor.CanManageAdministrators() {
		return ErrAdministratorsManage
	}
	return nil
}

func (s *Service) SetUserRolesAs(ctx context.Context, actor *Principal, userID string, roleIDs []string) error {
	if err := s.AssertCanAssignRoles(ctx, actor, userID, roleIDs); err != nil {
		return err
	}
	return s.SetUserRoles(ctx, userID, roleIDs)
}

func (s *Service) AddRoleMembersAs(ctx context.Context, actor *Principal, roleID string, userIDs []string) error {
	for _, uid := range userIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if err := s.AssertCanAssignRoles(ctx, actor, uid, []string{roleID}); err != nil {
			return err
		}
		if err := s.AssignRole(ctx, uid, roleID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RemoveRoleMemberAs(ctx context.Context, actor *Principal, roleID, userID string) error {
	if err := s.AssertCanModifyUser(ctx, actor, userID); err != nil {
		return err
	}
	if s.RoleIsPrivileged(ctx, roleID) && !actor.CanManageAdministrators() {
		return ErrAdministratorsManage
	}
	return s.RemoveRoleMember(ctx, roleID, userID)
}

func CeilingHTTPStatus(err error) int {
	if errors.Is(err, ErrAdministratorsManage) || errors.Is(err, ErrPrivilegeCeiling) || errors.Is(err, ErrProtectedUser) || errors.Is(err, ErrLocalLoginDisabled) || errors.Is(err, ErrLocalSignupDisabled) {
		return 403
	}
	if errors.Is(err, ErrLastAdmin) {
		return 409
	}
	return 400
}
