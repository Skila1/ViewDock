package watchtogether

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/nodeauth"
)

const testCoordSecret = "0123456789abcdef0123456789abcdef"

func coordinatorServer(t *testing.T, h *Hub) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.With(CoordinatorGuard(nodeauth.NewVerifier(testCoordSecret), nil)).Route("/api/v1/coordinator", h.InternalRoutes)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func remoteFor(t *testing.T, srv *httptest.Server, secret string) *Remote {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return NewRemote(u, secret, nil)
}

func TestRemoteCoordinatorMatchesHub(t *testing.T) {
	h := newTestHub()
	c := remoteFor(t, coordinatorServer(t, h), testCoordSecret)
	ctx := context.Background()
	host := &auth.Principal{Kind: auth.KindUser, UserID: "u1", DisplayName: "Ada"}
	other := &auth.Principal{Kind: auth.KindUser, UserID: "u2", DisplayName: "Bo"}

	if _, err := c.Create(ctx, host, "movie", "missing"); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("unauthorized title: %v", err)
	}
	room, err := c.Create(ctx, host, "movie", "A")
	if err != nil || room.ID == "" || room.InviteCode == "" || room.HostID != host.ID() || room.OwnerID != host.ID() {
		t.Fatalf("create = %+v, %v", room, err)
	}
	if h.Room(room.ID) == nil {
		t.Fatal("room not hosted by the coordinator")
	}
	if got := c.Invite(room.InviteCode); got == nil || got.ID != room.ID {
		t.Fatalf("invite = %+v", got)
	}
	if c.Invite("nope") != nil {
		t.Fatal("unknown invite resolved")
	}
	st := c.State(room.ID)
	if st == nil || st["host"] != host.ID() || st["item_id"] != "A" {
		t.Fatalf("state = %v", st)
	}
	if _, ok := st["position_ms"].(int64); !ok {
		t.Fatalf("position_ms type %T", st["position_ms"])
	}
	if c.State("missing") != nil {
		t.Fatal("missing room has state")
	}
	if !c.PartyAccess(host.ID(), "movie", "A") || c.PartyAccess(host.ID(), "movie", "B") || c.PartyAccess(other.ID(), "movie", "A") {
		t.Fatal("party access differs from the hub")
	}
	if rooms := c.Rooms(); len(rooms) != 1 || rooms[0].ID != room.ID || rooms[0].Members != 1 {
		t.Fatalf("rooms = %+v", rooms)
	}
	if err := c.Control(room.ID, other.ID(), "pause"); err == nil {
		t.Fatal("non-member controlled the room")
	}
	if err := c.Control(room.ID, host.ID(), "play"); err != nil {
		t.Fatalf("host control: %v", err)
	}
	if st := h.State(room.ID); st["playing"] != true {
		t.Fatalf("control not applied: %v", st)
	}
	if c.ReclaimOwner(ctx, other, room.ID) || !c.ReclaimOwner(ctx, host, room.ID) {
		t.Fatal("owner reclaim differs from the hub")
	}
	snap := c.SyncSnapshot()
	if rooms, _ := snap["rooms"].(int); rooms != 1 {
		t.Fatalf("snapshot = %v", snap)
	}
}

func TestRemoteCoordinatorFailsClosed(t *testing.T) {
	h := newTestHub()
	srv := coordinatorServer(t, h)
	host := &auth.Principal{Kind: auth.KindUser, UserID: "u1"}
	room, err := h.Create(context.Background(), host, "movie", "A")
	if err != nil {
		t.Fatal(err)
	}

	wrong := remoteFor(t, srv, strings.Repeat("x", 32))
	if _, err := wrong.Create(context.Background(), host, "movie", "A"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong secret create: %v", err)
	}
	if wrong.PartyAccess(host.ID(), "movie", "A") || wrong.State(room.ID) != nil || wrong.SyncSnapshot() != nil {
		t.Fatal("wrong secret was accepted")
	}

	resp, err := http.Post(srv.URL+"/api/v1/coordinator/party-access", "application/json",
		strings.NewReader(`{"principal_id":"`+host.ID()+`","item_kind":"movie","item_id":"A"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned request: %d", resp.StatusCode)
	}

	down := remoteFor(t, srv, testCoordSecret)
	srv.Close()
	if _, err := down.Create(context.Background(), host, "movie", "A"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("coordinator down: %v", err)
	}
	if down.PartyAccess(host.ID(), "movie", "A") || down.Rooms() == nil || len(down.Rooms()) != 0 {
		t.Fatal("unreachable coordinator did not fail closed")
	}
}
