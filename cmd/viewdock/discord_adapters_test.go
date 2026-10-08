package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/discordbot/interactions"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/search"
	"github.com/viewdock/viewdock/internal/watchtogether"
)

type libGrants struct{ ids []string }

func (g libGrants) CanRead(_ context.Context, _, lib string) bool {
	for _, id := range g.ids {
		if id == lib {
			return true
		}
	}
	return false
}
func (libGrants) CanDownload(context.Context, string, string) bool { return false }
func (g libGrants) GrantedLibraryIDs(context.Context, string) ([]string, error) {
	return g.ids, nil
}

type noGate struct{}

func (noGate) AllowStream(context.Context, string, string, string) error    { return nil }
func (noGate) CanStreamMedia(context.Context, string, string, string) error { return nil }
func (noGate) Heartbeat(context.Context, string) error                      { return nil }
func (noGate) Release(context.Context, string)                              {}
func (noGate) ShareTokenForGuest(context.Context, string) string            { return "" }

func adapterFixture(t *testing.T) (*sql.DB, *library.Service, *watchtogether.Hub) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adapters.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	for _, q := range []string{
		`INSERT INTO libraries(id, name, root_path, content_type, created_at, updated_at) VALUES ('lib', 'L', '/x', 'movies', 't', 't'), ('other', 'O', '/o', 'movies', 't', 't')`,
		`INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('m1', 'lib', 'Signal Fire', 'signal fire', 't', 't', 17), ('m2', 'lib', 'Signal Kids', 'signal kids', 't', 't', 0), ('m3', 'other', 'Signal Hidden', 'signal hidden', 't', 't', 0)`,
		`INSERT INTO media_files(id, library_id, rel_path, abs_path, kind, movie_id, created_at, updated_at) VALUES ('f1', 'lib', 'a.mp4', '/x/a.mp4', 'movie', 'm1', 't', 't'), ('f2', 'lib', 'b.mp4', '/x/b.mp4', 'movie', 'm2', 't', 't')`,
		`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, content_age_limit)
			VALUES ('kid', 'kid', 'x', 'K', '', 0, 0, '', 't', 't', 12), ('host', 'host', 'x', 'H', '', 0, 0, '', 't', 't', 0)`,
	} {
		if _, err := sqlDB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	grants := libGrants{ids: []string{"lib"}}
	libs := library.NewService(sqlDB, grants, nil, nil, t.TempDir())
	hub := watchtogether.NewWithOriginCheckerAndDB(libs, grants, noGate{}, nil, sqlDB, false)
	return sqlDB, libs, hub
}

func TestDiscordCatalogAppliesGrantsAndRestrictions(t *testing.T) {
	sqlDB, libs, hub := adapterFixture(t)
	for id, title := range map[string]string{"m1": "Signal Fire", "m2": "Signal Kids", "m3": "Signal Hidden"} {
		if err := library.UpsertFTS(context.Background(), sqlDB, "movie", id, title, 2001, ""); err != nil {
			t.Fatal(err)
		}
	}
	for id, title := range map[string]string{"m1": "Signal Fire", "m2": "Signal Kids", "m3": "Signal Hidden"} {
		if err := library.UpsertFTS(context.Background(), sqlDB, "movie", id, title, 2001, ""); err != nil {
			t.Fatal(err)
		}
	}
	srch := search.New(sqlDB)
	cat := discordCatalog{search: srch, grants: libGrants{ids: []string{"lib"}}, libs: libs, hub: hub}
	ctx := context.Background()
	kid := &auth.Principal{Kind: auth.KindUser, UserID: "kid"}

	hits, err := cat.SearchTitles(ctx, kid, "signal", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "m2" {
		t.Fatalf("restricted search hits = %+v, want only m2", hits)
	}
	if _, err := cat.TitleOf(ctx, kid, "movie", "m1"); !errors.Is(err, interactions.ErrForbidden) {
		t.Fatalf("restricted title lookup err = %v", err)
	}
	if _, err := cat.TitleOf(ctx, kid, "movie", "m3"); !errors.Is(err, interactions.ErrForbidden) {
		t.Fatalf("ungranted title lookup err = %v", err)
	}
	host := &auth.Principal{Kind: auth.KindUser, UserID: "host"}
	if name, err := cat.TitleOf(ctx, host, "movie", "m1"); err != nil || name != "Signal Fire" {
		t.Fatalf("title = %q err %v", name, err)
	}
	partyOnly := &auth.Principal{Kind: auth.KindUser, UserID: "host", PartyOnly: true}
	if _, err := cat.SearchTitles(ctx, partyOnly, "signal", 10); !errors.Is(err, interactions.ErrForbidden) {
		t.Fatalf("party-only search err = %v", err)
	}
}

func TestDiscordPartiesControlIsHostOnly(t *testing.T) {
	_, _, hub := adapterFixture(t)
	parties := discordParties{hub: hub}
	ctx := context.Background()
	host := &auth.Principal{Kind: auth.KindUser, UserID: "host"}
	kid := &auth.Principal{Kind: auth.KindUser, UserID: "kid"}

	if _, _, err := parties.Create(ctx, kid, "movie", "m1"); !errors.Is(err, interactions.ErrForbidden) {
		t.Fatalf("restricted create err = %v", err)
	}
	roomID, code, err := parties.Create(ctx, host, "movie", "m2")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := parties.Resolve(code); !ok || got != roomID {
		t.Fatalf("resolve %q = %q %v", code, got, ok)
	}
	if _, err := hub.Join(ctx, kid, code); err != nil {
		t.Fatal(err)
	}
	if err := parties.Control(ctx, kid, roomID, interactions.ActionResume); !errors.Is(err, interactions.ErrNotHost) {
		t.Fatalf("member control err = %v", err)
	}
	if err := parties.Control(ctx, host, roomID, interactions.ActionResume); err != nil {
		t.Fatal(err)
	}
	if st := parties.State(roomID); st["playing"] != true || st["owner"] != host.ID() {
		t.Fatalf("state after resume = %v", st)
	}
	if err := parties.Control(ctx, host, "missing", interactions.ActionPause); !errors.Is(err, interactions.ErrRoomNotFound) {
		t.Fatalf("missing room err = %v", err)
	}
}
