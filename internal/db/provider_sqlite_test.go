package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenProviderMigratesSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	store, err := OpenProvider(context.Background(), ProviderConfig{Dialect: DialectSQLite, SQLitePath: path, BusyTimeoutMS: 5000})
	if err != nil {
		t.Fatal(err)
	}
	defer store.SQL.Close()
	var n int
	if err := store.SQL.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("a fresh SQLite database opened through OpenProvider has no schema")
	}
}
