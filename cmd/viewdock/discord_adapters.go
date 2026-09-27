package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/discordbot/interactions"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/search"
	"github.com/viewdock/viewdock/internal/watchtogether"
)

// discordParties exposes the watch party hub to Discord slash commands.
type discordParties struct{ hub watchtogether.Coordinator }

func (d discordParties) Create(ctx context.Context, p *auth.Principal, kind, id string) (string, string, error) {
	if d.hub == nil {
		return "", "", interactions.ErrForbidden
	}
	room, err := d.hub.Create(ctx, p, kind, id)
	if err != nil {
		return "", "", interactions.ErrForbidden
	}
	return room.ID, room.InviteCode, nil
}

func (d discordParties) State(roomID string) map[string]any {
	if d.hub == nil {
		return nil
	}
	return d.hub.State(roomID)
}

func (d discordParties) Control(ctx context.Context, p *auth.Principal, roomID, action string) error {
	if d.hub == nil || d.hub.State(roomID) == nil {
		return interactions.ErrRoomNotFound
	}
	if p == nil {
		return interactions.ErrNotHost
	}
	typ := "pause"
	if action == interactions.ActionResume {
		typ = "play"
	}
	d.hub.ReclaimOwner(ctx, p, roomID)
	if err := d.hub.Control(roomID, p.ID(), typ); err != nil {
		return interactions.ErrNotHost
	}
	return nil
}

func (d discordParties) Resolve(code string) (string, bool) {
	if d.hub == nil {
		return "", false
	}
	if room := d.hub.Invite(code); room != nil {
		return room.ID, true
	}
	return "", false
}

// discordCatalog searches and names titles with the same library grants and
// content restrictions as the HTTP catalogue.
type discordCatalog struct {
	search *search.Service
	grants library.LibraryGrants
	libs   *library.Service
	hub    watchtogether.Coordinator
}

func (c discordCatalog) viewer(ctx context.Context, p *auth.Principal) (context.Context, []string, error) {
	if p == nil || !p.IsUser() || p.PartyOnly {
		return nil, nil, interactions.ErrForbidden
	}
	ctx = library.WithUserID(ctx, p.UserID)
	if p.IsAdmin {
		return ctx, nil, nil
	}
	ids, err := c.grants.GrantedLibraryIDs(ctx, p.UserID)
	if err != nil {
		return nil, nil, err
	}
	if ids == nil {
		ids = []string{}
	}
	return library.WithGrantedIDs(ctx, ids), ids, nil
}

func (c discordCatalog) SearchTitles(ctx context.Context, p *auth.Principal, query string, limit int) ([]interactions.Title, error) {
	vctx, ids, err := c.viewer(ctx, p)
	if err != nil {
		return nil, err
	}
	hits, err := c.search.Query(vctx, query, ids)
	if err != nil {
		return nil, err
	}
	out := make([]interactions.Title, 0, min(limit, len(hits)))
	for _, h := range hits {
		if h.ItemKind != "movie" && h.ItemKind != "episode" {
			continue
		}
		out = append(out, interactions.Title{Kind: h.ItemKind, ID: h.ItemID, Name: h.Title, Year: h.Year})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (c discordCatalog) TitleOf(ctx context.Context, p *auth.Principal, kind, id string) (string, error) {
	if p != nil && p.IsUser() && p.PartyOnly {
		if c.hub == nil || !c.hub.PartyAccess(p.ID(), kind, id) {
			return "", interactions.ErrForbidden
		}
		return titleOf(ctx, c.libs, kind, id)
	}
	vctx, _, err := c.viewer(ctx, p)
	if err != nil {
		return "", err
	}
	name, err := titleOf(vctx, c.libs, kind, id)
	if err != nil {
		return "", interactions.ErrForbidden
	}
	return name, nil
}

func titleOf(ctx context.Context, libs *library.Service, kind, id string) (string, error) {
	switch kind {
	case "movie":
		m, err := libs.GetMovie(ctx, id)
		if err != nil {
			return "", err
		}
		return m.Title, nil
	case "episode":
		ep, err := libs.GetEpisode(ctx, id)
		if err != nil {
			return "", err
		}
		if ep.Title != "" {
			return ep.Title, nil
		}
		return fmt.Sprintf("Season %d, episode %d", ep.Season, ep.Number), nil
	}
	return "", errors.New("unsupported title kind")
}
