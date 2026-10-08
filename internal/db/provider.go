package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/viewdock/viewdock/migrations"
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

type ProviderConfig struct {
	Dialect       Dialect
	SQLitePath    string
	PostgresURL   string
	BusyTimeoutMS int
	MaxOpenConns  int
	MaxIdleConns  int
}

type Store struct {
	SQL     *sql.DB
	Dialect Dialect
}

type Queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func OpenProvider(ctx context.Context, cfg ProviderConfig) (*Store, error) {
	dialect := cfg.Dialect
	if dialect == "" {
		if cfg.PostgresURL != "" {
			dialect = DialectPostgres
		} else {
			dialect = DialectSQLite
		}
	}
	if dialect == DialectPostgres {
		if strings.TrimSpace(cfg.PostgresURL) == "" {
			return nil, errors.New("postgres provider requires a connection URL")
		}
		if err := MigrateProvider(ctx, cfg); err != nil {
			return nil, err
		}
		sqlDB, err := openPostgres(context.Background(), cfg.PostgresURL)
		if err != nil {
			return nil, err
		}
		if cfg.MaxOpenConns == 0 {
			cfg.MaxOpenConns = 20
		}
		if cfg.MaxIdleConns == 0 {
			cfg.MaxIdleConns = 5
		}
		configurePool(sqlDB, cfg)
		sqlDB.SetConnMaxIdleTime(5 * time.Minute)
		if err := sqlDB.PingContext(ctx); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
		return &Store{SQL: sqlDB, Dialect: DialectPostgres}, nil
	}
	if dialect != DialectSQLite {
		return nil, errors.New("unsupported database provider: " + string(dialect))
	}
	if err := Migrate(cfg.SQLitePath); err != nil {
		return nil, err
	}
	sqlDB, err := Open(cfg.SQLitePath, cfg.BusyTimeoutMS)
	if err != nil {
		return nil, err
	}
	configurePool(sqlDB, cfg)
	return &Store{SQL: sqlDB, Dialect: DialectSQLite}, nil
}

func MigrateProvider(ctx context.Context, cfg ProviderConfig) error {
	if cfg.Dialect == DialectPostgres || (cfg.Dialect == "" && strings.TrimSpace(cfg.PostgresURL) != "") {
		return MigratePostgres(ctx, cfg.PostgresURL)
	}
	return Migrate(cfg.SQLitePath)
}

func MigratePostgres(ctx context.Context, dsn string) error {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return errors.New("postgres migration requires a connection URL")
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if err := sqlDB.PingContext(ctx); err != nil {
		return err
	}
	if _, err := sqlDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	entries, err := migrationNames()
	if err != nil {
		return err
	}
	for _, name := range entries {
		var exists int
		scanErr := sqlDB.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = $1`, name).Scan(&exists)
		if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
			return scanErr
		}
		if scanErr == nil {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return err
		}
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range splitSQLStatements(translateSQLiteForPostgres(string(body))) {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if insertIgnore.MatchString(stmt) {
				stmt = insertIgnore.ReplaceAllString(stmt, "INSERT INTO")
				if !strings.Contains(strings.ToUpper(stmt), "ON CONFLICT") {
					stmt += " ON CONFLICT DO NOTHING"
				}
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("migration %s statement %q: %w", name, stmt, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES ($1, NOW())`, name); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func splitSQLStatements(sql string) []string {
	var uncommented strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		uncommented.WriteString(line)
		uncommented.WriteByte('\n')
	}
	sql = uncommented.String()
	var out []string
	var current strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		switch ch {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ';':
			if !inSingle && !inDouble {
				stmt := strings.TrimSpace(current.String())
				if stmt != "" {
					out = append(out, stmt)
				}
				current.Reset()
				continue
			}
		}
		current.WriteByte(ch)
	}
	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

func translateSQLiteForPostgres(sql string) string {
	q := strings.TrimSpace(sql)
	if start := strings.Index(q, "CREATE VIRTUAL TABLE media_fts USING fts5("); start >= 0 {
		if end := strings.Index(q[start:], ");"); end >= 0 {
			end += start + 2
			q = q[:start] + `CREATE TABLE IF NOT EXISTS media_fts (item_kind TEXT NOT NULL, item_id TEXT NOT NULL, title TEXT NOT NULL, year TEXT NOT NULL, extra TEXT NOT NULL);` + q[end:]
		}
	}
	q = strings.ReplaceAll(q, "COLLATE NOCASE", "")
	q = strings.ReplaceAll(q, "BLOB", "BYTEA")
	q = strings.ReplaceAll(q, "datetime('now')", pgNowText)
	q = strings.ReplaceAll(q, "strftime('%Y-%m-%dT%H:%M:%SZ', 'now')", pgNowISO)
	q = strings.ReplaceAll(q, "strftime('%Y-%m-%dT%H:%M:%S', 'now')", pgNowISONZ)
	q = strings.ReplaceAll(q, "CREATE VIRTUAL TABLE media_fts USING fts5", "CREATE TABLE IF NOT EXISTS media_fts")
	q = strings.ReplaceAll(q, "CREATE VIRTUAL TABLE", "CREATE TABLE IF NOT EXISTS")
	q = strings.ReplaceAll(q, "USING fts5", "")
	return q
}

func configurePool(sqlDB *sql.DB, cfg ProviderConfig) {
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
}

func (s *Store) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.SQL.ExecContext(ctx, s.query(query), args...)
}

func (s *Store) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.SQL.QueryContext(ctx, s.query(query), args...)
}

func (s *Store) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.SQL.QueryRowContext(ctx, s.query(query), args...)
}

func (s *Store) query(query string) string {
	if s == nil || s.Dialect != DialectPostgres {
		return query
	}
	return RewritePlaceholders(query)
}

func RewritePlaceholders(query string) string {
	var out strings.Builder
	out.Grow(len(query))
	parameter := 0
	inSingle, inDouble, inBacktick := false, false, false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if ch == '\\' && inSingle && i+1 < len(query) {
			out.WriteByte(ch)
			i++
			out.WriteByte(query[i])
			continue
		}
		switch ch {
		case '\'':
			if !inDouble && !inBacktick {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle && !inBacktick {
				inDouble = !inDouble
			}
		case '`':
			if !inSingle && !inDouble {
				inBacktick = !inBacktick
			}
		case '?':
			if !inSingle && !inDouble && !inBacktick {
				parameter++
				out.WriteString("$")
				out.WriteString(strconv.Itoa(parameter))
				continue
			}
		}
		out.WriteByte(ch)
	}
	return out.String()
}
