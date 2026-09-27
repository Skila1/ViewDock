package runtimecfg

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/secrets"
	"github.com/viewdock/viewdock/internal/settings"
)

func testDefs() []Def {
	return []Def{
		{Key: "app.public_url", Kind: KindURL, Category: "General", Env: func() string { return "https://env.example" }},
		{Key: "tmdb.api_key", Kind: KindSecret, Category: "Metadata"},
		{Key: "playback.transcode_slots", Kind: KindInt, Default: "2", Min: 1, Max: 64, Category: "Playback"},
		{Key: "features.watch_together", Kind: KindBool, Default: "1", Category: "Features"},
	}
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

func newSvc(t *testing.T, sqlDB *sql.DB, key []byte) *Service {
	t.Helper()
	kv := settings.New(sqlDB)
	c, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}
	kv.UseCipher(c)
	s := New(sqlDB, kv, audit.New(sqlDB), testDefs())
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApplyValidateEncryptPropagateRollback(t *testing.T) {
	ctx := context.Background()
	sqlDB := testDB(t)
	key := make([]byte, 32)
	a := newSvc(t, sqlDB, key)
	b := newSvc(t, sqlDB, key)

	views, v0 := a.Views()
	for _, v := range views {
		if v.Key == "app.public_url" && (v.Source != "environment" || v.Value != "https://env.example") {
			t.Fatalf("env bootstrap not applied: %+v", v)
		}
	}
	if a.Int("playback.transcode_slots") != 2 {
		t.Fatal("default not applied")
	}

	var slots []string
	a.Bind("playback.transcode_slots", func(v string) { slots = append(slots, v) })

	var ve *ValidationError
	if _, err := a.Apply(ctx, "admin", "", map[string]Change{"playback.transcode_slots": {Value: "0"}}, v0, ""); !errors.As(err, &ve) {
		t.Fatalf("out of range accepted: %v", err)
	}
	if _, err := a.Apply(ctx, "admin", "", map[string]Change{"app.public_url": {Value: "https://u:p@x.example"}}, v0, ""); !errors.As(err, &ve) {
		t.Fatalf("credentials in URL accepted: %v", err)
	}
	if _, err := a.Apply(ctx, "admin", "", map[string]Change{"nope": {Value: "1"}}, v0, ""); !errors.As(err, &ve) {
		t.Fatalf("unknown key accepted: %v", err)
	}

	v1, err := a.Apply(ctx, "admin", "", map[string]Change{
		"playback.transcode_slots": {Value: "6"},
		"tmdb.api_key":             {Value: "tmdb-secret"},
	}, v0, "tune")
	if err != nil {
		t.Fatal(err)
	}
	if v1 != v0+1 {
		t.Fatalf("version %d -> %d", v0, v1)
	}
	if _, err := b.Apply(ctx, "admin", "", map[string]Change{"playback.transcode_slots": {Value: "3"}}, v0, ""); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale write accepted: %v", err)
	}
	if strings.Join(slots, ",") != "2,6" {
		t.Fatalf("hook calls %v", slots)
	}

	raw, _ := a.KV.GetRaw(ctx, "tmdb.api_key")
	if !secrets.IsEncrypted(raw) || strings.Contains(raw, "tmdb-secret") {
		t.Fatalf("secret stored in plaintext: %q", raw)
	}
	if got, _ := a.KV.Get(ctx, "tmdb.api_key"); got != "tmdb-secret" {
		t.Fatalf("transparent decrypt: %q", got)
	}
	views, _ = a.Views()
	for _, v := range views {
		if v.Key == "tmdb.api_key" && (v.Value != "" || !v.Set) {
			t.Fatalf("secret exposed in view: %+v", v)
		}
	}
	hist, err := a.History(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range hist {
		if e.Key == "tmdb.api_key" && e.Value != "" {
			t.Fatal("secret exposed in history")
		}
	}

	if err := b.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if b.Int("playback.transcode_slots") != 6 || b.String("tmdb.api_key") != "tmdb-secret" || b.Version() != v1 {
		t.Fatal("second process did not observe the change")
	}

	v2, err := a.Apply(ctx, "admin", "", map[string]Change{"playback.transcode_slots": {Value: "9"}, "features.watch_together": {Value: "false"}}, v1, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Bool("features.watch_together") {
		t.Fatal("bool not applied")
	}
	v3, err := a.Rollback(ctx, "admin", "", v1, v2)
	if err != nil {
		t.Fatal(err)
	}
	if a.Int("playback.transcode_slots") != 6 || !a.Bool("features.watch_together") || a.String("tmdb.api_key") != "tmdb-secret" {
		t.Fatalf("rollback to v%d incomplete", v1)
	}
	if _, err := a.Rollback(ctx, "admin", "", v0, v3); err != nil {
		t.Fatal(err)
	}
	if a.Int("playback.transcode_slots") != 2 || a.String("tmdb.api_key") != "" {
		t.Fatal("rollback to the initial version did not restore bootstrap values")
	}

	wrong := newSvcNoLoad(sqlDB, make([]byte, 32))
	wrong.KV.UseCipher(mustCipher(t, []byte(strings.Repeat("k", 32))))
	if _, err := a.Apply(ctx, "admin", "", map[string]Change{"tmdb.api_key": {Value: "again"}, "playback.transcode_slots": {Value: "5"}}, a.Version(), ""); err != nil {
		t.Fatal(err)
	}
	if err := wrong.Load(ctx); err == nil {
		t.Fatal("secret decrypted with the wrong master key")
	}
	if wrong.Int("playback.transcode_slots") != 5 || wrong.String("tmdb.api_key") != "" {
		t.Fatal("an unreadable secret must not block the other settings")
	}
}

func TestEncryptPlaintextSecrets(t *testing.T) {
	ctx := context.Background()
	sqlDB := testDB(t)
	kv := settings.New(sqlDB)
	_ = kv.Set(ctx, "tmdb.api_key", "legacy-plain")
	kv.UseCipher(mustCipher(t, make([]byte, 32)))
	s := New(sqlDB, kv, nil, testDefs())
	n, err := s.EncryptPlaintextSecrets(ctx)
	if err != nil || n != 1 {
		t.Fatalf("encrypted %d %v", n, err)
	}
	raw, _ := kv.GetRaw(ctx, "tmdb.api_key")
	if !secrets.IsEncrypted(raw) {
		t.Fatal("legacy secret left in plaintext")
	}
	if v, _ := kv.Get(ctx, "tmdb.api_key"); v != "legacy-plain" {
		t.Fatalf("value changed: %q", v)
	}
	if n, _ := s.EncryptPlaintextSecrets(ctx); n != 0 {
		t.Fatal("re-encrypted an encrypted value")
	}
}

func newSvcNoLoad(sqlDB *sql.DB, key []byte) *Service {
	kv := settings.New(sqlDB)
	c, _ := secrets.New(key)
	kv.UseCipher(c)
	return New(sqlDB, kv, nil, testDefs())
}

func mustCipher(t *testing.T, key []byte) *secrets.Cipher {
	t.Helper()
	c, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
