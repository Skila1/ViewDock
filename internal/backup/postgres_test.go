package backup

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
)

// pgSchema opens a migrated PostgreSQL provider inside a throwaway schema of
// the VD_PG_TEST_URL database and drops the schema afterwards.
func pgSchema(t *testing.T, dsn, name string) *db.Store {
	t.Helper()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if _, err := admin.Exec(`CREATE SCHEMA ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + name + ` CASCADE`) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	store, err := db.OpenProvider(context.Background(), db.ProviderConfig{Dialect: db.DialectPostgres, PostgresURL: u.String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.SQL.Close() })
	return store
}

func TestPostgresLogicalBackupRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("VD_PG_TEST_URL"))
	if dsn == "" {
		t.Skip("VD_PG_TEST_URL not set")
	}
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	src := pgSchema(t, dsn, "vd_backup_src_"+suffix)
	seedRows(t, src.SQL, false)

	cfg := config.Config{ConfigDir: t.TempDir()}
	svc := New(src.SQL, cfg, audit.New(src.SQL), nil)
	if svc.Dialect != db.DialectPostgres {
		t.Fatalf("dialect %s", svc.Dialect)
	}
	svc.Settings = func() Settings { return Settings{Retention: 3, Destination: DestinationLocal} }
	m, err := svc.Create(ctx, TriggerManual, "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindLogicalJSON || m.Dialect != string(db.DialectPostgres) {
		t.Fatalf("manifest %+v", m)
	}
	store, _, err := svc.destination(svc.settings())
	if err != nil {
		t.Fatal(err)
	}

	tgt := pgSchema(t, dsn, "vd_backup_tgt_"+suffix)
	rep, err := Validate(ctx, store, m.ID, tgt.SQL, db.DialectPostgres, "", "")
	if err != nil || !rep.Valid || !rep.CurrentEmpty || rep.RestoreMode != ModeRows {
		t.Fatalf("validate %+v %v", rep, err)
	}
	res, err := Restore(ctx, store, m.ID, Target{Dialect: db.DialectPostgres, DB: tgt.SQL}, svc.StagingDir(), RestoreOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeRows || res.Rows == 0 {
		t.Fatalf("result %+v", res)
	}
	assertRestoredRows(t, tgt.SQL, false)
	if _, err := Restore(ctx, store, m.ID, Target{Dialect: db.DialectPostgres, DB: tgt.SQL}, svc.StagingDir(), RestoreOptions{}); err == nil {
		t.Fatal("restore into non-empty PostgreSQL database accepted without force")
	}

	// The same logical export restores into SQLite, the migration path
	// between providers.
	lite := openTestDB(t, filepath.Join(t.TempDir(), "from-pg.db"))
	if _, err := Restore(ctx, store, m.ID, Target{Dialect: db.DialectSQLite, DB: lite}, svc.StagingDir(), RestoreOptions{}); err != nil {
		t.Fatalf("postgres to sqlite: %v", err)
	}
	assertRestoredRows(t, lite, false)
}
