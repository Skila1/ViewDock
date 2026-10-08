package playback

import (
	"context"
	"errors"
	"testing"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/progress"
)

type continueCatalog struct {
	titles map[string]string
	libs   map[string]string
}

func (c continueCatalog) ItemTitle(_ context.Context, _, id string) (string, error) {
	t, ok := c.titles[id]
	if !ok {
		return "", errors.New("missing")
	}
	return t, nil
}

func (c continueCatalog) Exists(_ context.Context, _, id string) bool {
	_, ok := c.titles[id]
	return ok
}

func (c continueCatalog) LibraryIDForItem(_ context.Context, _, id string) (string, error) {
	return c.libs[id], nil
}

func (c continueCatalog) ItemPoster(_ context.Context, _, id string) *string {
	u := "/api/v1/artwork/poster/movie/" + id
	return &u
}

type continueGrants struct{ readable map[string]bool }

func (g continueGrants) CanRead(_ context.Context, _, libraryID string) bool {
	return g.readable[libraryID]
}

func (g continueGrants) CanDownload(context.Context, string, string) bool { return false }

func (g continueGrants) GrantedLibraryIDs(context.Context, string) ([]string, error) { return nil, nil }

func TestContinueSkipsMissingAndHiddenTitles(t *testing.T) {
	a := &API{
		Catalog: continueCatalog{
			titles: map[string]string{"kept": "Scarface", "hidden": "Secret"},
			libs:   map[string]string{"kept": "lib-a", "hidden": "lib-b"},
		},
		Grants: continueGrants{readable: map[string]bool{"lib-a": true}},
	}
	list := []progress.Record{
		{ItemKind: "movie", ItemID: "gone"},
		{ItemKind: "movie", ItemID: "hidden"},
		{ItemKind: "movie", ItemID: "kept"},
	}
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u1"}
	out := a.continueItems(context.Background(), user, list)
	if len(out) != 1 || out[0].ItemID != "kept" || out[0].Title != "Scarface" || out[0].PosterURL == nil {
		t.Fatalf("continue = %+v", out)
	}
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "a1", IsAdmin: true}
	if out := a.continueItems(context.Background(), admin, list); len(out) != 2 {
		t.Fatalf("admin continue = %+v, want the two existing titles", out)
	}
}
