package watchtogether

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/share"
)

type loc struct {
	kind, id string
}

func (l loc) LocateItem(_ context.Context, k, id string) (*library.LocatedFile, error) {
	if k == l.kind && id == l.id {
		return &library.LocatedFile{ID: "f", LibraryID: "lib", ItemKind: k, ItemID: id}, nil
	}
	return nil, os.ErrNotExist
}
func (l loc) LocateFile(context.Context, string) (*library.LocatedFile, error) {
	return nil, os.ErrNotExist
}
func (l loc) Contains(string, string) error                  { return nil }
func (l loc) Open(context.Context, string) (*os.File, error) { return nil, os.ErrNotExist }

type grants struct{}

func (grants) CanRead(context.Context, string, string) bool                { return true }
func (grants) CanDownload(context.Context, string, string) bool            { return false }
func (grants) GrantedLibraryIDs(context.Context, string) ([]string, error) { return nil, nil }

type gate struct {
	deny error
}

func (g *gate) AllowStream(context.Context, string, string, string) error    { return g.deny }
func (g *gate) CanStreamMedia(context.Context, string, string, string) error { return g.deny }
func (g *gate) Heartbeat(context.Context, string) error                      { return g.deny }
func (g *gate) Release(context.Context, string)                              {}
func (g *gate) ShareTokenForGuest(context.Context, string) string            { return "sharetok" }

func TestUserGuestSync(t *testing.T) {
	g := &gate{}
	h := New(loc{kind: "movie", id: "A"}, grants{}, g)
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u1", DisplayName: "Ada"}
	guest := &auth.Principal{Kind: auth.KindGuestShare, GuestSessionID: "g1", MediaKind: "movie", MediaID: "A", DisplayName: "Guest"}
	room, err := h.Create(context.Background(), user, "movie", "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Join(context.Background(), guest, room.InviteCode); err != nil {
		t.Fatal(err)
	}
	h.Apply(room.ID, user.ID(), "play", 12_000, "", "")
	st := h.State(room.ID)
	if st["playing"] != true {
		t.Fatalf("%v", st)
	}
	if st["position_ms"].(int64) < 12_000 {
		t.Fatalf("pos %v", st["position_ms"])
	}
	members := st["members"].([]map[string]any)
	if len(members) != 2 {
		t.Fatalf("members %d", len(members))
	}
}

func TestGuestWrongItemDenied(t *testing.T) {
	h := New(loc{kind: "movie", id: "A"}, grants{}, &gate{})
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u1", DisplayName: "Ada"}
	room, err := h.Create(context.Background(), user, "movie", "A")
	if err != nil {
		t.Fatal(err)
	}
	guestB := &auth.Principal{Kind: auth.KindGuestShare, GuestSessionID: "gB", MediaKind: "movie", MediaID: "B"}
	if _, err := h.Join(context.Background(), guestB, room.InviteCode); err == nil {
		t.Fatal("guest for item B must not join room for A")
	}
}

func TestRevokeKicksGuest(t *testing.T) {
	g := &gate{}
	h := New(loc{kind: "movie", id: "A"}, grants{}, g)
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u1"}
	guest := &auth.Principal{Kind: auth.KindGuestShare, GuestSessionID: "g1", MediaKind: "movie", MediaID: "A"}
	room, _ := h.Create(context.Background(), user, "movie", "A")
	_, _ = h.Join(context.Background(), guest, room.InviteCode)
	g.deny = share.ErrGone
	if err := h.CheckGate(context.Background(), room, guest); err == nil {
		t.Fatal("expected deny")
	}
	h.KickGuest("g1")
	st := h.State(room.ID)
	members := st["members"].([]map[string]any)
	if len(members) != 1 {
		t.Fatalf("guest should be kicked: %d", len(members))
	}
}

func TestRoomRecoversFromDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rooms.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u1", DisplayName: "Ada"}
	first := NewWithOriginCheckerAndDB(nil, nil, &gate{}, nil, sqlDB, false)
	room, err := first.Create(context.Background(), user, "movie", "A")
	if err != nil {
		t.Fatal(err)
	}
	first.Apply(room.ID, user.ID(), "play", 12000, "", "")
	second := NewWithOriginCheckerAndDB(nil, nil, &gate{}, nil, sqlDB, false)
	recovered := second.Invite(room.InviteCode)
	if recovered == nil || recovered.ID != room.ID || recovered.PositionMS != 12000 || !recovered.Playing {
		t.Fatalf("recovered room = %#v", recovered)
	}
}

func TestContentRestrictionBlocksRoom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rated.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, root_path, content_type, created_at, updated_at) VALUES ('lib', 'L', '/x', 'movies', 't', 't')`,
		`INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('A', 'lib', 'Grim', 'grim', 't', 't', 17)`,
		`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, content_age_limit)
			VALUES ('kid', 'kid', 'x', 'K', '', 0, 0, '', 't', 't', 12), ('adult', 'adult', 'x', 'A', '', 0, 0, '', 't', 't', 0)`,
	} {
		if _, err := sqlDB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	h := NewWithOriginCheckerAndDB(loc{kind: "movie", id: "A"}, grants{}, &gate{}, nil, sqlDB, false)
	kid := &auth.Principal{Kind: auth.KindUser, UserID: "kid"}
	if _, err := h.Create(context.Background(), kid, "movie", "A"); err == nil {
		t.Fatal("restricted user created a room for a title above their limit")
	}
	adult := &auth.Principal{Kind: auth.KindUser, UserID: "adult"}
	room, err := h.Create(context.Background(), adult, "movie", "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Join(context.Background(), kid, room.InviteCode); err == nil {
		t.Fatal("restricted user joined a room for a title above their limit")
	}
}

func TestNoWatchTogetherTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	rows, err := sqlDB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%watch_together%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var n string
		_ = rows.Scan(&n)
		t.Fatalf("unexpected table %s", n)
	}
	if err := rows.Err(); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
}

func TestDriftResync(t *testing.T) {
	h := New(loc{kind: "movie", id: "A"}, grants{}, &gate{})
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u1"}
	room, _ := h.Create(context.Background(), user, "movie", "A")
	h.Apply(room.ID, user.ID(), "play", 0, "", "")
	out, ok := h.Apply(room.ID, user.ID(), "position", 10_000, "", "")
	if !ok || out == nil || out["type"] != "sync" || out["action"] != "seek" {
		t.Fatalf("drift %v", out)
	}
}

// An administrator joining a Discord channel's party takes it over: they
// become owner and host, and the previous owner does not take it back.
func TestHandOver(t *testing.T) {
	ctx := context.Background()
	h := New(loc{kind: "movie", id: "A"}, grants{}, &gate{})
	first := &auth.Principal{Kind: auth.KindUser, UserID: "u1", DisplayName: "First"}
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "u2", DisplayName: "Admin", IsAdmin: true}
	room, err := h.Create(ctx, first, "movie", "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandOver(ctx, admin, room.ID); err != nil {
		t.Fatal(err)
	}
	if st := h.State(room.ID); st["owner"] != admin.ID() || st["host"] != first.ID() {
		t.Fatalf("before joining, the admin owns the room and the first host keeps playing: %v", st)
	}
	if _, err := h.Join(ctx, admin, room.InviteCode); err != nil {
		t.Fatal(err)
	}
	if st := h.State(room.ID); st["host"] != admin.ID() {
		t.Fatalf("admin is host once in the room: %v", st)
	}
	if _, err := h.Join(ctx, first, room.InviteCode); err != nil {
		t.Fatal(err)
	}
	if h.ReclaimOwner(ctx, first, room.ID) || h.State(room.ID)["host"] != admin.ID() {
		t.Fatal("the previous owner took the room back")
	}
	// Handing over to a member makes them host at once.
	if err := h.HandOver(ctx, first, room.ID); err != nil || h.State(room.ID)["host"] != first.ID() {
		t.Fatalf("hand over to a member: %v %v", err, h.State(room.ID))
	}
	banned := &auth.Principal{Kind: auth.KindUser, UserID: "u3"}
	h.mu.Lock()
	room.Banned = map[string]bool{banned.ID(): true}
	h.mu.Unlock()
	partyOnly := &auth.Principal{Kind: auth.KindUser, UserID: "u4", PartyOnly: true}
	for _, p := range []*auth.Principal{banned, partyOnly, nil} {
		if err := h.HandOver(ctx, p, room.ID); err == nil {
			t.Fatalf("hand over to %+v must be refused", p)
		}
	}
	if err := h.HandOver(ctx, admin, "missing"); err == nil {
		t.Fatal("missing room")
	}
}
