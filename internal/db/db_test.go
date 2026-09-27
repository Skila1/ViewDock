package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateOpenFTS5Vacuum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "viewdock.db")
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	var fk, journal string
	if err := sqlDB.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != "1" {
		t.Fatalf("foreign_keys=%s", fk)
	}
	if err := sqlDB.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode=%s", journal)
	}

	if _, err := sqlDB.Exec(`CREATE VIRTUAL TABLE t_fts USING fts5(x)`); err != nil {
		t.Fatalf("FTS5: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO t_fts(x) VALUES ('hello viewdock')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM t_fts WHERE t_fts MATCH 'viewdock'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("match count=%d", n)
	}

	dest := filepath.Join(dir, "backup.db")
	if err := VacuumInto(context.Background(), sqlDB, dest); err != nil {
		t.Fatalf("VACUUM INTO: %v", err)
	}
	st, err := os.Stat(dest)
	if err != nil || st.Size() == 0 {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "viewdock.db")
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`INSERT INTO server_settings(key, value) VALUES ('instance_name', 'Test')`); err != nil {
		t.Fatal(err)
	}
	var v string
	if err := sqlDB.QueryRow(`SELECT value FROM server_settings WHERE key='instance_name'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "Test" {
		t.Fatalf("got %q", v)
	}
}
