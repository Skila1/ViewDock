package auth

import (
	"context"
	"database/sql"
)

type GrantStore struct{ DB *sql.DB }

func NewGrantStore(db *sql.DB) *GrantStore { return &GrantStore{DB: db} }

func (g *GrantStore) CanRead(ctx context.Context, userID, libraryID string) bool {
	if g.admin(ctx, userID) {
		return true
	}
	var n int
	_ = g.DB.QueryRowContext(ctx, `
		SELECT 1 WHERE EXISTS (
			SELECT 1 FROM library_grants WHERE user_id = ? AND library_id = ?
		) OR EXISTS (
			SELECT 1 FROM library_role_grants rg
			JOIN user_roles ur ON ur.role_id = rg.role_id
			WHERE ur.user_id = ? AND rg.library_id = ?
		)
	`, userID, libraryID, userID, libraryID).Scan(&n)
	return n == 1
}

func (g *GrantStore) CanDownload(ctx context.Context, userID, libraryID string) bool {
	if g.admin(ctx, userID) {
		return true
	}
	var n int
	_ = g.DB.QueryRowContext(ctx, `
		SELECT 1 WHERE EXISTS (
			SELECT 1 FROM library_grants WHERE user_id = ? AND library_id = ? AND can_download = 1
		) OR EXISTS (
			SELECT 1 FROM library_role_grants rg
			JOIN user_roles ur ON ur.role_id = rg.role_id
			WHERE ur.user_id = ? AND rg.library_id = ? AND rg.can_download = 1
		)
	`, userID, libraryID, userID, libraryID).Scan(&n)
	return n == 1
}

func (g *GrantStore) GrantedLibraryIDs(ctx context.Context, userID string) ([]string, error) {
	if g.admin(ctx, userID) {
		rows, err := g.DB.QueryContext(ctx, `SELECT id FROM libraries`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		if out == nil {
			out = []string{}
		}
		return out, rows.Err()
	}
	rows, err := g.DB.QueryContext(ctx, `
		SELECT library_id FROM library_grants WHERE user_id = ?
		UNION
		SELECT rg.library_id FROM library_role_grants rg
		JOIN user_roles ur ON ur.role_id = rg.role_id
		WHERE ur.user_id = ?
	`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if out == nil {
		out = []string{}
	}
	return out, rows.Err()
}

func (g *GrantStore) Set(ctx context.Context, userID, libraryID string, download bool) error {
	d := 0
	if download {
		d = 1
	}
	_, err := g.DB.ExecContext(ctx, `
		INSERT INTO library_grants(user_id, library_id, can_download) VALUES (?, ?, ?)
		ON CONFLICT(user_id, library_id) DO UPDATE SET can_download = excluded.can_download
	`, userID, libraryID, d)
	return err
}

func (g *GrantStore) Delete(ctx context.Context, userID, libraryID string) error {
	_, err := g.DB.ExecContext(ctx, `DELETE FROM library_grants WHERE user_id = ? AND library_id = ?`, userID, libraryID)
	return err
}

func (g *GrantStore) SetRole(ctx context.Context, roleID, libraryID string, download bool) error {
	d := 0
	if download {
		d = 1
	}
	_, err := g.DB.ExecContext(ctx, `
		INSERT INTO library_role_grants(role_id, library_id, can_download) VALUES (?, ?, ?)
		ON CONFLICT(role_id, library_id) DO UPDATE SET can_download = excluded.can_download
	`, roleID, libraryID, d)
	return err
}

func (g *GrantStore) DeleteRole(ctx context.Context, roleID, libraryID string) error {
	_, err := g.DB.ExecContext(ctx, `DELETE FROM library_role_grants WHERE role_id = ? AND library_id = ?`, roleID, libraryID)
	return err
}

func (g *GrantStore) ListForLibrary(ctx context.Context, libraryID string) (users []map[string]any, roles []map[string]any, err error) {
	urows, err := g.DB.QueryContext(ctx, `
		SELECT g.user_id, u.username, u.display_name, g.can_download
		FROM library_grants g JOIN users u ON u.id = g.user_id
		WHERE g.library_id = ? ORDER BY u.username
	`, libraryID)
	if err != nil {
		return nil, nil, err
	}
	defer urows.Close()
	users = []map[string]any{}
	for urows.Next() {
		var id, un, dn string
		var dl int
		if err := urows.Scan(&id, &un, &dn, &dl); err != nil {
			return nil, nil, err
		}
		users = append(users, map[string]any{"user_id": id, "username": un, "display_name": dn, "can_download": dl == 1})
	}
	rrows, err := g.DB.QueryContext(ctx, `
		SELECT g.role_id, r.name, g.can_download
		FROM library_role_grants g JOIN roles r ON r.id = g.role_id
		WHERE g.library_id = ? ORDER BY r.name
	`, libraryID)
	if err != nil {
		return nil, nil, err
	}
	defer rrows.Close()
	roles = []map[string]any{}
	for rrows.Next() {
		var id, name string
		var dl int
		if err := rrows.Scan(&id, &name, &dl); err != nil {
			return nil, nil, err
		}
		roles = append(roles, map[string]any{"role_id": id, "name": name, "can_download": dl == 1})
	}
	return users, roles, nil
}

func (g *GrantStore) ListForUser(ctx context.Context, userID string) ([]map[string]any, error) {
	rows, err := g.DB.QueryContext(ctx, `
		SELECT g.library_id, l.name, g.can_download
		FROM library_grants g JOIN libraries l ON l.id = g.library_id
		WHERE g.user_id = ? ORDER BY l.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var dl int
		if err := rows.Scan(&id, &name, &dl); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"library_id": id, "name": name, "can_download": dl == 1})
	}
	return out, rows.Err()
}

func (g *GrantStore) admin(ctx context.Context, userID string) bool {
	var n int
	_ = g.DB.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id = ?`, userID).Scan(&n)
	return n == 1
}
