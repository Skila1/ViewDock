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
func (l loc) LocateFile(context.Context, string) (*library.LocatedFile, error) { return nil, os.ErrNotExist }
func (l loc) Contains(string, string) error                                    { return nil }
func (l loc) Open(context.Context, string) (*os.File, error)                   { return nil, os.ErrNotExist }

type grants struct{}

func (grants) CanRead(context.Context, string, string) bool                       { return true }
func (grants) CanDownload(context.Context, string, string) bool                   { return false }
func (grants) GrantedLibraryIDs(context.Context, string) ([]string, error)        { return nil, nil }

type gate struct {
	deny error
}

func (g *gate) AllowStream(context.Context, string, string, string) error     { return g.deny }
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
	if !ok || out == nil || out["type"] != "resync" {
		t.Fatalf("drift %v", out)
	}
}
