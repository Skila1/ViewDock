package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/storage"
)

const invalidUTF8 = "bad\xff\xfeutf8"

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

func seed(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	seedRows(t, sqlDB, true)
}

// seedRows inserts one user and an encrypted setting. PostgreSQL TEXT cannot
// hold invalid UTF-8, so that row is SQLite only.
func seedRows(t *testing.T, sqlDB *sql.DB, invalidText bool) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id, username, password_hash, display_name, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, 1, ?, ?)`,
			[]any{"u1", "admin", "$argon2id$hash", "Admin", now, now}},
		{`INSERT INTO server_settings(key, value) VALUES (?, ?)`, []any{"tmdb.api_key", "enc:v1:c2VjcmV0LWNpcGhlcnRleHQ"}},
	}
	if invalidText {
		stmts = append(stmts, struct {
			sql  string
			args []any
		}{`INSERT INTO server_settings(key, value) VALUES (?, ?)`, []any{"test.binary_text", invalidUTF8}})
	}
	for _, q := range stmts {
		if _, err := sqlDB.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
}

type testEnv struct {
	cfg config.Config
	src *sql.DB
	svc *Service
}

func newEnv(t *testing.T, retention int) *testEnv {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{ConfigDir: filepath.Join(root, "config"), DatabasePath: filepath.Join(root, "config", "viewdock.db")}
	src := openTestDB(t, cfg.DatabasePath)
	seed(t, src)
	svc := New(src, cfg, audit.New(src), nil)
	svc.Settings = func() Settings { return Settings{Retention: retention, Destination: DestinationLocal} }
	svc.KeyID = func() string { return "key-fp" }
	var tick atomic.Int64
	base := time.Date(2026, 9, 27, 7, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base.Add(time.Duration(tick.Add(1)) * time.Minute) }
	return &testEnv{cfg: cfg, src: src, svc: svc}
}

func (e *testEnv) store(t *testing.T) storage.Store {
	t.Helper()
	store, _, err := e.svc.destination(e.svc.settings())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestApplicationTablesFollowMigrations(t *testing.T) {
	tables, err := applicationTables()
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, name := range tables {
		idx[name] = i
	}
	for _, want := range []string{"server_settings", "users", "sessions", "libraries", "movies", "media_fts", "backend_nodes", "config_history"} {
		if _, ok := idx[want]; !ok {
			t.Fatalf("missing table %s in %v", want, tables)
		}
	}
	for _, skip := range []string{"schema_migrations", "request_idempotency", "login_states"} {
		if _, ok := idx[skip]; ok {
			t.Fatalf("table %s should be excluded", skip)
		}
	}
	if idx["users"] > idx["sessions"] || idx["libraries"] > idx["movies"] || idx["series"] > idx["episodes"] {
		t.Fatalf("parents must precede children: %v", tables)
	}
	latest, err := LatestSchemaVersion()
	if err != nil || latest < 23 {
		t.Fatalf("latest schema %d %v", latest, err)
	}
}

func TestCellEncodingRoundTrip(t *testing.T) {
	in := []any{nil, int64(-42), int64(math.MaxInt64), 3.0, 2.5, math.Inf(1), []byte{0, 1, 255}, invalidUTF8, "plain", true}
	cells := make([]any, len(in))
	for i, v := range in {
		cells[i] = encodeCell(v)
	}
	line, err := json.Marshal(cells)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var raw []any
	if err := dec.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	for i, v := range raw {
		got, err := decodeCell(v)
		if err != nil {
			t.Fatalf("cell %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, in[i]) {
			t.Fatalf("cell %d: got %#v want %#v", i, got, in[i])
		}
	}
	if _, err := decodeCell(map[string]any{"x": "y"}); err == nil {
		t.Fatal("unknown cell encoding accepted")
	}
}

func TestIDValidation(t *testing.T) {
	id, err := newID(time.Now())
	if err != nil || !ValidID(id) {
		t.Fatalf("generated id %q invalid: %v", id, err)
	}
	for _, bad := range []string{"", "..", "../vd-20260927T071000Z-1a2b3c4d", "vd-20260927T071000Z-1a2b3c4d/..", "vd-20260927T071000Z-1A2B3C4D", `vd-20260927T071000Z-1a2b3c4d\x`, "vd-2026-09-27"} {
		if ValidID(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSnapshotBackupLifecycle(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, 2)
	var ids []string
	for i := 0; i < 3; i++ {
		m, err := e.svc.Create(ctx, TriggerManual, "u1", "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
		latest, _ := LatestSchemaVersion()
		if m.Kind != KindSQLiteSnapshot || m.SchemaVersion != latest || m.MasterKeyID != "key-fp" || len(m.Files) != 1 || m.Notice == "" {
			t.Fatalf("manifest %+v", m)
		}
	}
	list, err := e.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != ids[2] || list[1].ID != ids[1] {
		t.Fatalf("retention kept %v, created %v", list, ids)
	}
	if _, err := e.svc.Manifest(ctx, ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned backup still present: %v", err)
	}

	rep, err := e.svc.Validate(ctx, ids[2], "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Valid || rep.FilesChecked != 1 || rep.CurrentEmpty || rep.RestoreMode != ModeFile || rep.MasterKeyMatches == nil || !*rep.MasterKeyMatches {
		t.Fatalf("report %+v", rep)
	}

	snap := filepath.Join(e.svc.LocalDir(), ids[2], snapshotName)
	f, err := os.OpenFile(snap, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("tamper"))
	_ = f.Close()
	rep, err = e.svc.Validate(ctx, ids[2], "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid || len(rep.Problems) == 0 {
		t.Fatalf("tampered backup reported valid: %+v", rep)
	}

	if err := e.svc.Delete(ctx, ids[2], "u1", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(ctx, ids[2], "u1", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := e.svc.Delete(ctx, "../../etc", "u1", ""); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("traversal delete: %v", err)
	}
	var n int
	if err := e.src.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'backup.create'`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("audit create events %d %v", n, err)
	}
	if err := e.src.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action IN ('backup.prune', 'backup.delete', 'backup.validate')`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("audit events %d %v", n, err)
	}
	if entries, _ := os.ReadDir(e.svc.StagingDir()); len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
}

func TestCreateRefusesConcurrentRun(t *testing.T) {
	e := newEnv(t, 3)
	if !e.svc.begin() {
		t.Fatal("begin")
	}
	defer e.svc.end()
	if _, err := e.svc.Create(context.Background(), TriggerManual, "u1", ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
}

func TestPruneRemovesAbandonedUploads(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, 5)
	store := e.store(t)
	partial := "vd-20200101T000000Z-00000000/viewdock.db"
	if err := store.Put(ctx, partial, strings.NewReader("x"), 1, ""); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(e.svc.LocalDir(), filepath.FromSlash(partial)), old, old); err != nil {
		t.Fatal(err)
	}
	e.svc.now = time.Now
	if _, err := e.svc.Create(ctx, TriggerManual, "u1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.svc.LocalDir(), filepath.FromSlash(partial))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned upload kept: %v", err)
	}
}

func TestLogicalExportRestoresIntoEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, 5)
	e.svc.Logical = true
	m, err := e.svc.Create(ctx, TriggerManual, "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindLogicalJSON || len(m.Tables) == 0 || len(m.Tables) != len(m.Files) {
		t.Fatalf("manifest %+v", m)
	}
	var users *Table
	for i := range m.Tables {
		if m.Tables[i].Name == "users" {
			users = &m.Tables[i]
		}
	}
	if users == nil || users.Rows != 1 {
		t.Fatalf("users table entry %+v", users)
	}

	target := openTestDB(t, filepath.Join(t.TempDir(), "target.db"))
	store := e.store(t)
	res, err := Restore(ctx, store, m.ID, Target{Dialect: db.DialectSQLite, DB: target}, e.svc.StagingDir(), RestoreOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeRows || res.Rows == 0 {
		t.Fatalf("result %+v", res)
	}
	assertRestored(t, target)

	if _, err := Restore(ctx, store, m.ID, Target{Dialect: db.DialectSQLite, DB: target}, e.svc.StagingDir(), RestoreOptions{}); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("restore into non-empty db: %v", err)
	}
	if _, err := Restore(ctx, store, m.ID, Target{Dialect: db.DialectSQLite, DB: target}, e.svc.StagingDir(), RestoreOptions{Force: true}); err != nil {
		t.Fatalf("forced restore: %v", err)
	}
	assertRestored(t, target)
}

func TestSnapshotRestoresRowsIntoOpenDatabase(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, 5)
	m, err := e.svc.Create(ctx, TriggerManual, "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	target := openTestDB(t, filepath.Join(t.TempDir(), "target.db"))
	res, err := Restore(ctx, e.store(t), m.ID, Target{Dialect: db.DialectSQLite, DB: target}, e.svc.StagingDir(), RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeRows {
		t.Fatalf("mode %s", res.Mode)
	}
	assertRestored(t, target)
}

func TestSnapshotFileRestore(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, 5)
	m, err := e.svc.Create(ctx, TriggerManual, "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	store := e.store(t)
	path := filepath.Join(t.TempDir(), "restored", "viewdock.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Restore(ctx, store, m.ID, Target{Dialect: db.DialectSQLite, SQLitePath: path}, e.svc.StagingDir(), RestoreOptions{Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeFile || res.SafetyCopy != "" {
		t.Fatalf("result %+v", res)
	}
	check := func() {
		restored, err := db.Open(path, 5000)
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		assertRestored(t, restored)
	}
	check()
	if _, err := Restore(ctx, store, m.ID, Target{Dialect: db.DialectSQLite, SQLitePath: path}, e.svc.StagingDir(), RestoreOptions{}); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("expected ErrNotEmpty, got %v", err)
	}
	res, err = Restore(ctx, store, m.ID, Target{Dialect: db.DialectSQLite, SQLitePath: path}, e.svc.StagingDir(), RestoreOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(res.SafetyCopy); err != nil {
		t.Fatalf("safety copy missing: %v", err)
	}
	check()
}

func assertRestored(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	assertRestoredRows(t, sqlDB, true)
}

func assertRestoredRows(t *testing.T, sqlDB *sql.DB, invalidText bool) {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM users WHERE id = 'u1' AND username = 'admin'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("user not restored: %d %v", n, err)
	}
	var secret, binary string
	if err := sqlDB.QueryRow(`SELECT value FROM server_settings WHERE key = 'tmdb.api_key'`).Scan(&secret); err != nil {
		t.Fatal(err)
	}
	if secret != "enc:v1:c2VjcmV0LWNpcGhlcnRleHQ" {
		t.Fatalf("encrypted secret changed: %q", secret)
	}
	if invalidText {
		if err := sqlDB.QueryRow(`SELECT value FROM server_settings WHERE key = 'test.binary_text'`).Scan(&binary); err != nil || binary != invalidUTF8 {
			t.Fatalf("non-UTF-8 text changed: %q %v", binary, err)
		}
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM permissions`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("seed permissions missing after restore: %d %v", n, err)
	}
}

func TestManifestRejectsTampering(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, 5)
	m, err := e.svc.Create(ctx, TriggerManual, "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.svc.LocalDir(), m.ID, manifestName)
	write := func(mut func(*Manifest)) {
		c := m
		c.Files = append([]File(nil), m.Files...)
		mut(&c)
		raw, _ := json.Marshal(c)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, mut := range map[string]func(*Manifest){
		"traversal file": func(c *Manifest) { c.Files[0].Name = "../../escape.db" },
		"other id":       func(c *Manifest) { c.ID = "vd-20200101T000000Z-00000000" },
		"bad checksum":   func(c *Manifest) { c.Files[0].SHA256 = "zz" },
		"future format":  func(c *Manifest) { c.FormatVersion = FormatVersion + 1 },
	} {
		write(mut)
		if _, err := loadManifest(ctx, e.store(t), m.ID); !errors.Is(err, ErrManifest) {
			t.Fatalf("%s: expected ErrManifest, got %v", name, err)
		}
	}
	write(func(c *Manifest) { c.SchemaVersion = 99999 })
	target := openTestDB(t, filepath.Join(t.TempDir(), "t.db"))
	if _, err := Restore(ctx, e.store(t), m.ID, Target{Dialect: db.DialectSQLite, DB: target}, e.svc.StagingDir(), RestoreOptions{}); !errors.Is(err, ErrSchemaNewer) {
		t.Fatalf("newer schema: %v", err)
	}
}

func TestSchedulePlan(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, 5)
	hours := 0
	e.svc.Settings = func() Settings { return Settings{ScheduleHours: hours, Retention: 5, Destination: DestinationLocal} }
	if _, ok := e.svc.plan(ctx); ok {
		t.Fatal("schedule should be disabled")
	}
	hours = 6
	wait, ok := e.svc.plan(ctx)
	if !ok || wait != startupDelay {
		t.Fatalf("first run wait %v %v", wait, ok)
	}
	m, err := e.svc.Create(ctx, TriggerManual, "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	wait, ok = e.svc.plan(ctx)
	expected := m.CreatedAt.Add(6 * time.Hour).Sub(e.svc.now())
	if !ok || wait < expected-2*time.Minute || wait > expected+2*time.Minute {
		t.Fatalf("wait %v, expected about %v", wait, expected)
	}
	fresh := New(e.src, e.cfg, nil, nil)
	fresh.Settings = e.svc.Settings
	fresh.now = e.svc.now
	if _, ok := fresh.plan(ctx); !ok || fresh.lastAt.IsZero() {
		t.Fatalf("history not read from destination: %v", fresh.lastAt)
	}
	st := fresh.Status()
	if st.NextRun == nil || st.ScheduleHours != 6 || st.Destination != DestinationLocal {
		t.Fatalf("status %+v", st)
	}
}

func TestSettingsSources(t *testing.T) {
	vals := map[string]string{KeySchedule: "12", KeyRetention: "3", KeyDestination: "s3", KeyS3Endpoint: "minio:9000", KeyS3Bucket: "b",
		KeyS3AccessKey: "a", KeyS3SecretKey: "s", KeyS3UseSSL: "1", KeyS3PathStyle: "0", KeyS3Prefix: "/p/"}
	s := SettingsFrom(func(k string) string { return vals[k] }).normalized()
	if s.ScheduleHours != 12 || s.Retention != 3 || s.Destination != DestinationS3 || !s.S3.UseSSL || s.S3.PathStyle || s.S3Prefix != "p" {
		t.Fatalf("settings %+v", s)
	}
	t.Setenv("VD_BACKUP_S3_BUCKET", "env-bucket")
	t.Setenv("VD_BACKUP_S3_USE_SSL", "true")
	env := EnvSettings(config.Config{StorageEndpoint: "fallback:9000", StorageAccessKey: "ak", StorageSecretKey: "sk"})
	if env.S3.Bucket != "env-bucket" || env.S3.Endpoint != "fallback:9000" || env.S3.AccessKey != "ak" || !env.S3.UseSSL || !env.S3.PathStyle ||
		env.Retention != DefaultRetention || env.ScheduleHours != DefaultScheduleHours || env.Destination != DestinationLocal || env.S3Prefix != DefaultS3Prefix {
		t.Fatalf("env settings %+v", env)
	}
	if len(ConfigDefs(config.Config{})) == 0 {
		t.Fatal("no config defs")
	}
	bad := New(nil, config.Config{ConfigDir: t.TempDir()}, nil, nil)
	bad.Settings = func() Settings { return Settings{Destination: DestinationS3} }
	if err := bad.CheckDestination(context.Background()); !errors.Is(err, ErrDestination) {
		t.Fatalf("incomplete s3 settings: %v", err)
	}
}

func TestHandlers(t *testing.T) {
	e := newEnv(t, 5)
	r := chi.NewRouter()
	var principal *auth.Principal
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if principal != nil {
				req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Route("/api/v1", e.svc.Routes)
	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	if rec := do(http.MethodGet, "/api/v1/admin/backups"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list %d", rec.Code)
	}
	principal = &auth.Principal{Kind: auth.KindUser, UserID: "u2", Permissions: []string{auth.PermSettingsManage}}
	if rec := do(http.MethodPost, "/api/v1/admin/backups"); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin create %d", rec.Code)
	}
	principal = &auth.Principal{Kind: auth.KindUser, UserID: "u1", IsAdmin: true}

	rec := do(http.MethodPost, "/api/v1/admin/backups")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	var created Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || !ValidID(created.ID) || created.Size == 0 {
		t.Fatalf("created %+v %v", created, err)
	}

	rec = do(http.MethodGet, "/api/v1/admin/backups")
	var list struct {
		Items  []Summary `json:"items"`
		Status Status    `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || len(list.Items) != 1 || list.Status.Retention != 5 || list.Status.MasterKeyID != "key-fp" {
		t.Fatalf("list %d %s", rec.Code, rec.Body)
	}

	for _, path := range []string{"/api/v1/admin/backups/..%2F..%2Fetc/validate", "/api/v1/admin/backups/not-an-id/validate"} {
		if rec := do(http.MethodPost, path); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
	if rec := do(http.MethodGet, "/api/v1/admin/backups/vd-20200101T000000Z-00000000/download"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing download %d", rec.Code)
	}

	rec = do(http.MethodPost, "/api/v1/admin/backups/"+created.ID+"/validate")
	var rep Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil || rec.Code != 200 || !rep.Valid {
		t.Fatalf("validate %d %s", rec.Code, rec.Body)
	}

	rec = do(http.MethodGet, "/api/v1/admin/backups/"+created.ID+"/download")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("download %d %v", rec.Code, rec.Header())
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	if len(names) != 2 || names[0] != created.ID+"/manifest.json" || names[1] != created.ID+"/viewdock.db" {
		t.Fatalf("archive entries %v", names)
	}

	rec = do(http.MethodPost, "/api/v1/admin/backups/destination/check")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("check %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodDelete, "/api/v1/admin/backups/"+created.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete %d", rec.Code)
	}
	if rec := do(http.MethodDelete, "/api/v1/admin/backups/"+created.ID); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete %d", rec.Code)
	}
}

func TestCLIRestoreDrill(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, 5)
	m, err := e.svc.Create(ctx, TriggerManual, "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	newHost := t.TempDir()
	cfg := config.Config{ConfigDir: filepath.Join(newHost, "config"), DatabasePath: filepath.Join(newHost, "config", "viewdock.db"), DatabaseDriver: "sqlite", BusyTimeoutMS: 5000}
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := RunCLI(ctx, args, cfg, &out, &errOut)
		return code, out.String() + errOut.String()
	}
	if code, out := run("list", "--dir", e.svc.LocalDir()); code != 0 || !strings.Contains(out, m.ID) {
		t.Fatalf("list %d %s", code, out)
	}
	if code, out := run("verify", m.ID, "--dir", e.svc.LocalDir()); code != 0 || !strings.Contains(out, "valid:           true") {
		t.Fatalf("verify %d %s", code, out)
	}
	if code, out := run("restore", "--dir", e.svc.LocalDir(), m.ID); code != 0 || !strings.Contains(out, "file restore") {
		t.Fatalf("restore %d %s", code, out)
	}
	restored, err := db.Open(cfg.DatabasePath, 5000)
	if err != nil {
		t.Fatal(err)
	}
	assertRestored(t, restored)
	_ = restored.Close()
	if code, out := run("restore", m.ID, "--dir", e.svc.LocalDir()); code == 0 || !strings.Contains(out, "not empty") {
		t.Fatalf("restore over data %d %s", code, out)
	}
	if code, _ := run("restore", "../x", "--dir", e.svc.LocalDir()); code != 2 {
		t.Fatalf("invalid id exit %d", code)
	}
	if code, out := run("create", "--logical", "--dir", filepath.Join(newHost, "exports")); code != 0 || !strings.Contains(out, KindLogicalJSON) {
		t.Fatalf("create %d %s", code, out)
	}
}
