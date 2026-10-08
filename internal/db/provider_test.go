package db

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

func TestRewritePlaceholders(t *testing.T) {
	query := "SELECT * FROM items WHERE id = ? AND note = '?' AND quoted = \"?\" AND other = ?"
	want := "SELECT * FROM items WHERE id = $1 AND note = '?' AND quoted = \"?\" AND other = $2"
	if got := RewritePlaceholders(query); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRewritePlaceholdersNumbersAllParameters(t *testing.T) {
	query := strings.Repeat("? ", 12)
	got := RewritePlaceholders(query)
	if !strings.Contains(got, "$10") || !strings.Contains(got, "$12") {
		t.Fatalf("parameter numbering: %q", got)
	}
}

func TestOpenProviderRejectsMissingPostgresURL(t *testing.T) {
	if _, err := OpenProvider(nil, ProviderConfig{Dialect: DialectPostgres}); err == nil {
		t.Fatal("expected missing postgres URL error")
	}
}

func TestOpenProviderPostgresMigration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("VD_PG_TEST_URL"))
	if dsn == "" {
		t.Skip("VD_PG_TEST_URL not set")
	}
	store, err := OpenProvider(context.Background(), ProviderConfig{Dialect: DialectPostgres, PostgresURL: dsn})
	if err != nil {
		t.Fatalf("OpenProvider: %v", err)
	}
	defer store.SQL.Close()
	if _, err := store.SQL.ExecContext(context.Background(), "CREATE TABLE IF NOT EXISTS test_postgres_migration (id TEXT PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := store.SQL.ExecContext(context.Background(), "INSERT INTO test_postgres_migration(id, value) VALUES ($1, $2)", "k1", "ok"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var v string
	if err := store.SQL.QueryRowContext(context.Background(), "SELECT value FROM test_postgres_migration WHERE id = $1", "k1").Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			t.Fatal("migration round-trip missing row")
		}
		t.Fatalf("scan: %v", err)
	}
	if v != "ok" {
		t.Fatalf("wrong value: %q", v)
	}
}
