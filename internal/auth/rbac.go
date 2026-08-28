package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	RoleAdministrator = "sys-administrator"
	RoleUser          = "sys-user"

	PermAdmin           = "admin"
	PermUsersManage     = "users.manage"
	PermRolesManage     = "roles.manage"
	PermLibrariesManage = "libraries.manage"
	PermMediaUpload     = "media.upload"
	PermMediaDelete     = "media.delete"
	PermSharesCreate    = "shares.create"
	PermSharesManage    = "shares.manage"
	PermStreamsInspect  = "streams.inspect"
	PermSettingsManage  = "settings.manage"
)

var ErrLastAdmin = errors.New("cannot remove the last administrator")

type Role struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	IsSystem    bool     `json:"is_system"`
	MemberCount int      `json:"member_count"`
	Permissions []string `json:"permissions"`
}

type Permission struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Service) PermissionsFor(ctx context.Context, userID string) []string {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT p.name
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		JOIN permissions p ON p.id = rp.permission_id
		WHERE ur.user_id = ?
	`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return out
		}
		out = append(out, n)
	}
	return out
}

func (s *Service) RoleIDsFor(ctx context.Context, userID string) []string {
	rows, err := s.DB.QueryContext(ctx, `SELECT role_id FROM user_roles WHERE user_id = ?`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return out
		}
		out = append(out, id)
	}
	return out
}

func (s *Service) RoleNamesFor(ctx context.Context, userID string) []string {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.name FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ?
		ORDER BY r.name
	`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return out
		}
		out = append(out, n)
	}
	return out
}

func (s *Service) AssignRole(ctx context.Context, userID, roleID string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO user_roles(user_id, role_id) VALUES (?, ?)`, userID, roleID)
	if err != nil {
		return err
	}
	return s.syncIsAdmin(ctx, userID)
}

func (s *Service) AssignRoleByName(ctx context.Context, userID, name string) error {
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM roles WHERE name = ?`, name).Scan(&id); err != nil {
		return err
	}
	return s.AssignRole(ctx, userID, id)
}

func (s *Service) SetUserRoles(ctx context.Context, userID string, roleIDs []string) error {
	if err := s.guardLastAdmin(ctx, userID, false, roleIDs); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, id := range roleIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_roles(user_id, role_id) VALUES (?, ?)`, userID, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.syncIsAdmin(ctx, userID)
}

func (s *Service) syncIsAdmin(ctx context.Context, userID string) error {
	admin := 0
	for _, p := range s.PermissionsFor(ctx, userID) {
		if p == PermAdmin {
			admin = 1
			break
		}
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET is_admin = ?, updated_at = ? WHERE id = ?`,
		admin, time.Now().UTC().Format(time.RFC3339), userID)
	return err
}

func (s *Service) AdministratorCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users u
		WHERE u.disabled = 0 AND (
			u.is_admin = 1
			OR EXISTS (
				SELECT 1 FROM user_roles ur
				JOIN role_permissions rp ON rp.role_id = ur.role_id
				JOIN permissions p ON p.id = rp.permission_id
				WHERE ur.user_id = u.id AND p.name = 'admin'
			)
		)
	`).Scan(&n)
	return n, err
}

func (s *Service) userIsAdmin(ctx context.Context, userID string) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id = ?`, userID).Scan(&n)
	if n == 1 {
		return true
	}
	for _, p := range s.PermissionsFor(ctx, userID) {
		if p == PermAdmin {
			return true
		}
	}
	return false
}

func (s *Service) rolesGrantAdmin(ctx context.Context, roleIDs []string) bool {
	if len(roleIDs) == 0 {
		return false
	}
	args := make([]any, 0, len(roleIDs))
	ph := make([]string, 0, len(roleIDs))
	for _, id := range roleIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		args = append(args, id)
		ph = append(ph, "?")
	}
	if len(args) == 0 {
		return false
	}
	var n int
	q := `SELECT COUNT(*) FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id
		WHERE p.name = 'admin' AND rp.role_id IN (` + strings.Join(ph, ",") + `)`
	_ = s.DB.QueryRowContext(ctx, q, args...).Scan(&n)
	return n > 0
}

func (s *Service) guardLastAdmin(ctx context.Context, userID string, nextDisabled bool, nextRoleIDs []string) error {
	if !s.userIsAdmin(ctx, userID) {
		return nil
	}
	staysAdmin := !nextDisabled && (nextRoleIDs == nil || s.rolesGrantAdmin(ctx, nextRoleIDs))
	if staysAdmin {
		return nil
	}
	n, err := s.AdministratorCount(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

func (s *Service) ListPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, description FROM permissions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Permission
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.ID, &p.Name, &p.Description); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []Permission{}
	}
	return out, rows.Err()
}

func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.id, r.name, r.description, r.is_system,
			(SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = r.id)
		FROM roles r ORDER BY r.is_system DESC, r.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		var r Role
		var sys int
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &sys, &r.MemberCount); err != nil {
			return nil, err
		}
		r.IsSystem = sys == 1
		r.Permissions = s.rolePerms(ctx, r.ID)
		out = append(out, r)
	}
	if out == nil {
		out = []Role{}
	}
	return out, rows.Err()
}

func (s *Service) rolePerms(ctx context.Context, roleID string) []string {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT p.name FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = ? ORDER BY p.name
	`, roleID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return out
		}
		out = append(out, n)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func (s *Service) GetRole(ctx context.Context, id string) (Role, error) {
	var r Role
	var sys int
	err := s.DB.QueryRowContext(ctx, `
		SELECT r.id, r.name, r.description, r.is_system,
			(SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = r.id)
		FROM roles r WHERE r.id = ?
	`, id).Scan(&r.ID, &r.Name, &r.Description, &sys, &r.MemberCount)
	if err != nil {
		return Role{}, err
	}
	r.IsSystem = sys == 1
	r.Permissions = s.rolePerms(ctx, r.ID)
	return r, nil
}

func (s *Service) CreateRole(ctx context.Context, name, desc string, perms []string) (Role, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Role{}, errors.New("name required")
	}
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO roles(id, name, description, is_system, created_at) VALUES (?, ?, ?, 0, ?)
	`, id, name, strings.TrimSpace(desc), now)
	if err != nil {
		return Role{}, err
	}
	if err := s.replaceRolePerms(ctx, id, perms, false); err != nil {
		return Role{}, err
	}
	return s.GetRole(ctx, id)
}

func (s *Service) UpdateRole(ctx context.Context, id, desc string, perms []string) (Role, error) {
	r, err := s.GetRole(ctx, id)
	if err != nil {
		return Role{}, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE roles SET description = ? WHERE id = ?`, strings.TrimSpace(desc), id)
	if err != nil {
		return Role{}, err
	}
	if perms != nil && r.Name != "Administrator" {
		if err := s.replaceRolePerms(ctx, id, perms, false); err != nil {
			return Role{}, err
		}
	}
	return s.GetRole(ctx, id)
}

func (s *Service) DeleteRole(ctx context.Context, id string) error {
	r, err := s.GetRole(ctx, id)
	if err != nil {
		return err
	}
	if r.IsSystem {
		return errors.New("cannot delete a built-in group")
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, id)
	return err
}

func (s *Service) replaceRolePerms(ctx context.Context, roleID string, perms []string, allowAdmin bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, roleID); err != nil {
		return err
	}
	for _, name := range perms {
		name = strings.TrimSpace(name)
		if name == "" || (name == PermAdmin && !allowAdmin) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO role_permissions(role_id, permission_id)
			SELECT ?, id FROM permissions WHERE name = ?
		`, roleID, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) AddRoleMembers(ctx context.Context, roleID string, userIDs []string) error {
	for _, uid := range userIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if err := s.AssignRole(ctx, uid, roleID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RemoveRoleMember(ctx context.Context, roleID, userID string) error {
	next := make([]string, 0)
	for _, id := range s.RoleIDsFor(ctx, userID) {
		if id != roleID {
			next = append(next, id)
		}
	}
	return s.SetUserRoles(ctx, userID, next)
}

func (s *Service) RoleMembers(ctx context.Context, roleID string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT u.id, u.username, u.display_name
		FROM user_roles ur JOIN users u ON u.id = ur.user_id
		WHERE ur.role_id = ? ORDER BY u.username
	`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, un, dn string
		if err := rows.Scan(&id, &un, &dn); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "username": un, "display_name": dn})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, rows.Err()
}

func HasPerm(p *Principal, name string) bool {
	if p == nil || !p.IsUser() {
		return false
	}
	return p.HasPerm(name)
}
