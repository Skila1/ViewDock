package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"

	"github.com/viewdock/viewdock/migrations"
)

func Open(path string, busyTimeoutMS int) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	abs = filepath.ToSlash(abs)
	q := url.Values{}
	q.Set("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	dsn := "file:" + abs + "?" + q.Encode() + "&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(8)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(0)
	if _, err := sqlDB.Exec(fmt.Sprintf(`
		PRAGMA foreign_keys=ON;
		PRAGMA journal_mode=WAL;
		PRAGMA busy_timeout=%d;
		PRAGMA cache_size=-16000;
	`, busyTimeoutMS)); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}

func Migrate(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	abs = filepath.ToSlash(abs)
	sqlDB, err := sql.Open("sqlite", "file:"+abs+"?_pragma=busy_timeout(20000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	driver, err := sqlite.WithInstance(sqlDB, &sqlite.Config{})
	if err != nil {
		return err
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

func WithTx(ctx context.Context, sqlDB *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func VacuumInto(ctx context.Context, sqlDB *sql.DB, dest string) error {
	if strings.ContainsAny(dest, `'";`) {
		return fmt.Errorf("invalid backup path")
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	_, err = sqlDB.ExecContext(ctx, "VACUUM INTO '"+filepath.ToSlash(abs)+"'")
	return err
}
