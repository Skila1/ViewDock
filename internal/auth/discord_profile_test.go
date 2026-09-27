package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/discordbot"
)

func TestDiscordProfileFilledByBot(t *testing.T) {
	ctx := context.Background()
	s := testSvc(t)
	admin, err := s.CreateAdmin(ctx, "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	const discordID = "123456789012345678"
	if err := s.AssociateSuperadminDiscord(ctx, discordID); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/users/"+discordID || r.Header.Get("Authorization") != "Bot bot-token" {
			http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"id":"` + discordID + `","username":"skila","global_name":"Skila","avatar":"abc123"}`))
	}))
	defer srv.Close()
	s.DiscordBot = &discordbot.Client{Token: "bot-token", BaseURL: srv.URL, HTTP: srv.Client()}

	ids, err := s.ListIdentities(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0]["provider_username"] != "skila" || ids[0]["avatar_hash"] != "abc123" {
		t.Fatalf("identity not filled: %v", ids)
	}
	if _, err := s.ListIdentities(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("bot lookups = %d, want 1 within the TTL", calls.Load())
	}
}

func TestDiscordSignInRefreshesProfile(t *testing.T) {
	ctx := context.Background()
	s := testSvc(t)
	if _, err := s.CreateAdmin(ctx, "admin", "secret12", "Admin"); err != nil {
		t.Fatal(err)
	}
	const discordID = "223456789012345678"
	if err := s.AssociateSuperadminDiscord(ctx, discordID); err != nil {
		t.Fatal(err)
	}
	u, err := s.UpsertDiscordUser(ctx, DiscordProfile{ID: discordID, Username: "fresh", Avatar: "av1"})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.ListIdentities(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ids[0]["provider_username"] != "fresh" || ids[0]["avatar_hash"] != "av1" {
		t.Fatalf("sign-in did not refresh profile: %v", ids)
	}
	if _, err := s.UpsertDiscordUser(ctx, DiscordProfile{ID: discordID}); err != nil {
		t.Fatal(err)
	}
	ids, _ = s.ListIdentities(ctx, u.ID)
	if ids[0]["provider_username"] != "fresh" {
		t.Fatalf("empty profile overwrote username: %v", ids)
	}
}

func TestLookupTimesDue(t *testing.T) {
	var l lookupTimes
	if !l.due("u", false, time.Hour) {
		t.Fatal("first lookup should run")
	}
	if l.due("u", false, time.Hour) {
		t.Fatal("second lookup inside ttl should wait")
	}
	l.last["u"] = time.Now().Add(-2 * time.Minute)
	if !l.due("u", true, time.Hour) {
		t.Fatal("missing data should retry after a minute")
	}
}
