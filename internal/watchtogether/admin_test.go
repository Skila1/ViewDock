package watchtogether

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
)

func TestPanelVisibilityIsHostOnly(t *testing.T) {
	base := time.Now()
	p := newParty(t, 2, base)
	if p.h.State(p.room.ID)["panel"] != PanelEveryone {
		t.Fatalf("default panel = %v", p.h.State(p.room.ID)["panel"])
	}
	hidden, bogus := PanelHidden, "sideways"
	if out := p.send(t, p.ids[1], clientMsg{Type: "settings", Panel: &hidden}, base); out.all != nil || p.room.Panel == PanelHidden {
		t.Fatal("a guest changed the panel")
	}
	if out := p.send(t, p.ids[0], clientMsg{Type: "settings", Panel: &bogus}, base); out.all != nil {
		t.Fatal("an unknown panel value was accepted")
	}
	out := p.send(t, p.ids[0], clientMsg{Type: "settings", Panel: &hidden}, base)
	if out.all == nil || out.all["panel"] != PanelHidden || out.all["shared_control"] != false {
		t.Fatalf("host panel change = %v", out.all)
	}
}

func TestAdminKickBanAndEnd(t *testing.T) {
	base := time.Now()
	p := newParty(t, 3, base)
	ctx := context.Background()
	kicked := &auth.Principal{Kind: auth.KindUser, UserID: p.ids[1]}
	banned := &auth.Principal{Kind: auth.KindUser, UserID: p.ids[2]}

	if err := p.h.Kick(p.room.ID, "nobody", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("kick unknown member err = %v", err)
	}
	if err := p.h.Kick(p.room.ID, kicked.ID(), false); err != nil {
		t.Fatal(err)
	}
	if p.room.Members[kicked.ID()] != nil {
		t.Fatal("kicked member still in room")
	}
	if _, err := p.h.Join(ctx, kicked, p.room.InviteCode); err != nil {
		t.Fatalf("a kick without ban must allow rejoining: %v", err)
	}

	if err := p.h.Kick(p.room.ID, banned.ID(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.h.Join(ctx, banned, p.room.InviteCode); err == nil {
		t.Fatal("banned member rejoined")
	}
	if _, err := p.h.MintTicket(banned, p.room.ID); err == nil {
		t.Fatal("banned member got a ticket")
	}

	rooms := p.h.AdminRooms()
	if len(rooms) != 1 || rooms[0].Banned != 1 || len(rooms[0].Members) != 2 {
		t.Fatalf("admin rooms = %+v", rooms)
	}

	if err := p.h.EndRoom(p.room.ID); err != nil {
		t.Fatal(err)
	}
	if p.h.Room(p.room.ID) != nil || p.h.Invite(p.room.InviteCode) != nil {
		t.Fatal("ended room still reachable")
	}
	if err := p.h.EndRoom(p.room.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("end twice err = %v", err)
	}
}
