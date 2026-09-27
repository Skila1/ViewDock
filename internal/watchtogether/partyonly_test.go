package watchtogether

import (
	"context"
	"testing"

	"github.com/viewdock/viewdock/internal/auth"
)

func TestPartyOnlyGuestScopedToJoinedRoom(t *testing.T) {
	h := New(loc{kind: "movie", id: "A"}, grants{}, &gate{})
	host := &auth.Principal{Kind: auth.KindUser, UserID: "host", DisplayName: "Host"}
	guest := &auth.Principal{Kind: auth.KindUser, UserID: "tmp", DisplayName: "Tmp", Temporary: true, PartyOnly: true}

	if _, err := h.Create(context.Background(), guest, "movie", "A"); err == nil {
		t.Fatal("party-only account created a room")
	}
	room, err := h.Create(context.Background(), host, "movie", "A")
	if err != nil {
		t.Fatal(err)
	}
	if h.PartyAccess(guest.ID(), "movie", "A") {
		t.Fatal("access before joining")
	}
	if _, err := h.MintTicket(guest, room.ID); err == nil {
		t.Fatal("ticket minted before joining")
	}
	if _, err := h.Join(context.Background(), guest, room.InviteCode); err != nil {
		t.Fatal(err)
	}
	if !h.PartyAccess(guest.ID(), "movie", "A") {
		t.Fatal("joined member lacks access to the room title")
	}
	if h.PartyAccess(guest.ID(), "movie", "B") {
		t.Fatal("access leaked to an unrelated title")
	}
	if _, err := h.MintTicket(guest, room.ID); err != nil {
		t.Fatalf("ticket after join: %v", err)
	}
	if err := h.authorize(context.Background(), guest, "movie", "B"); err == nil {
		t.Fatal("party-only guest authorized for a title outside the party")
	}
}
