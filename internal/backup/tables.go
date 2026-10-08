package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/migrations"
)

var (
	tableNamePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	columnPattern    = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

	createTableRe = regexp.MustCompile(`(?i)\bCREATE\s+(?:VIRTUAL\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["` + "`" + `]?([A-Za-z0-9_]+)`)
	dropTableRe   = regexp.MustCompile(`(?i)\bDROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?["` + "`" + `]?([A-Za-z0-9_]+)`)
	renameTableRe = regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+["` + "`" + `]?([A-Za-z0-9_]+)["` + "`" + `]?\s+RENAME\s+TO\s+["` + "`" + `]?([A-Za-z0-9_]+)`)
	migrationNum  = regexp.MustCompile(`^([0-9]+)_`)
)

// transientTables hold short-lived request state that is meaningless after a
// restore; they are not exported and are left untouched by row restores.
var transientTables = map[string]bool{
	"request_idempotency": true,
	"login_states":        true,
}

// applicationTables returns the tables created by the embedded migrations in
// creation order (parents before children), minus migration bookkeeping and
// transient tables.
func applicationTables() ([]string, error) {
	names, err := upMigrations()
	if err != nil {
		return nil, err
	}
	order := map[string]int{}
	live := map[string]bool{}
	seen := 0
	mark := func(name string) {
		name = strings.ToLower(name)
		if _, ok := order[name]; !ok {
			order[name] = seen
			seen++
		}
		live[name] = true
	}
	for _, name := range names {
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return nil, err
		}
		sqlText := stripSQLComments(string(body))
		type event struct {
			pos  int
			kind string
			a, b string
		}
		var events []event
		for _, m := range createTableRe.FindAllStringSubmatchIndex(sqlText, -1) {
			events = append(events, event{m[0], "create", sqlText[m[2]:m[3]], ""})
		}
		for _, m := range dropTableRe.FindAllStringSubmatchIndex(sqlText, -1) {
			events = append(events, event{m[0], "drop", sqlText[m[2]:m[3]], ""})
		}
		for _, m := range renameTableRe.FindAllStringSubmatchIndex(sqlText, -1) {
			events = append(events, event{m[0], "rename", sqlText[m[2]:m[3]], sqlText[m[4]:m[5]]})
		}
		sort.Slice(events, func(i, j int) bool { return events[i].pos < events[j].pos })
		for _, e := range events {
			switch e.kind {
			case "create":
				mark(e.a)
			case "drop":
				live[strings.ToLower(e.a)] = false
			case "rename":
				live[strings.ToLower(e.a)] = false
				mark(e.b)
			}
		}
	}
	var out []string
	for name, ok := range live {
		if ok && name != "schema_migrations" && !transientTables[name] && tableNamePattern.MatchString(name) {
			out = append(out, name)
		}
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out, nil
}

func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func upMigrations() ([]string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// LatestSchemaVersion is the highest migration number embedded in this build.
func LatestSchemaVersion() (int, error) {
	names, err := upMigrations()
	if err != nil {
		return 0, err
	}
	latest := 0
	for _, n := range names {
		if v := parseMigrationNumber(n); v > latest {
			latest = v
		}
	}
	return latest, nil
}

func parseMigrationNumber(s string) int {
	s = strings.TrimSpace(s)
	if m := migrationNum.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// schemaVersion reads the applied migration version. SQLite databases are
// migrated by golang-migrate (one row, numeric version, dirty flag); the
// PostgreSQL migrator records one row per applied file name.
func schemaVersion(ctx context.Context, q db.Queryer, dialect db.Dialect) (int, error) {
	if dialect == db.DialectPostgres {
		rows, err := q.QueryContext(ctx, `SELECT version FROM schema_migrations`)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		latest := 0
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return 0, err
			}
			if n := parseMigrationNumber(v); n > latest {
				latest = n
			}
		}
		return latest, rows.Err()
	}
	var version int64
	var dirty bool
	err := q.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if dirty {
		return 0, fmt.Errorf("schema migration %d is marked dirty", version)
	}
	return int(version), nil
}

func existingTables(ctx context.Context, q db.Queryer, dialect db.Dialect) (map[string]bool, error) {
	query := `SELECT name FROM sqlite_master WHERE type = 'table'`
	if dialect == db.DialectPostgres {
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()`
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)] = true
	}
	return out, rows.Err()
}

func tableColumns(ctx context.Context, q db.Queryer, table string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT * FROM `+quoteIdent(table)+` WHERE 1 = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return rows.Columns()
}

// isEmpty treats a database without user accounts as empty: that is the state
// of a fresh install before first-run setup, which only holds seed rows.
func isEmpty(ctx context.Context, q db.Queryer, dialect db.Dialect) (bool, error) {
	tables, err := existingTables(ctx, q, dialect)
	if err != nil {
		return false, err
	}
	if !tables["users"] {
		return true, nil
	}
	var n int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// quoteIdent is only applied to names that already passed tableNamePattern or
// columnPattern.
func quoteIdent(name string) string { return `"` + name + `"` }
