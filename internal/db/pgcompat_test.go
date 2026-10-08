package db

import (
	"strings"
	"testing"
)

func TestTranslateSQLite(t *testing.T) {
	cases := []struct{ in, want string }{
		{`SELECT a FROM t WHERE id = ? AND b = ?`, `SELECT a FROM t WHERE id = $1 AND b = $2`},
		{`INSERT OR IGNORE INTO user_roles(user_id, role_id) VALUES (?, ?)`, `INSERT INTO user_roles(user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`},
		{`INSERT OR IGNORE INTO x(a) VALUES (?) ON CONFLICT(a) DO NOTHING`, `INSERT INTO x(a) VALUES ($1) ON CONFLICT(a) DO NOTHING`},
		{`UPDATE m SET updated_at = datetime('now') WHERE id = ?`, `UPDATE m SET updated_at = ` + pgNowText + ` WHERE id = $1`},
		{`SELECT printf('%02d', e.season) FROM e`, `SELECT lpad(CAST(e.season AS TEXT), 2, '0') FROM e`},
		{`SELECT 1 FROM l WHERE message LIKE ? OR c NOT LIKE ?`, `SELECT 1 FROM l WHERE message ILIKE $1 OR c NOT ILIKE $2`},
		{`SELECT '?' FROM t WHERE a = ?`, `SELECT '?' FROM t WHERE a = $1`},
	}
	for _, c := range cases {
		if got := TranslateSQLite(c.in); strings.TrimSpace(got) != c.want {
			t.Errorf("TranslateSQLite(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}
